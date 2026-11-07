// Package host implements the host agent: screen capture + HTTP stream + input.
package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eminentailabs/office-rdp/core/clipboard"
	"github.com/eminentailabs/office-rdp/core/input"
	"github.com/pion/mediadevices"
	_ "github.com/pion/mediadevices/pkg/driver/screen"
)

type Agent struct {
	PeerID   string
	Alias    string
	Password string
	ReadOnly bool
	// TestPattern streams a synthetic frame instead of capturing the screen.
	// It exists so the host stack can be validated in environments where GDI
	// or X11 capture is unavailable (CI, Wine, headless runners).
	TestPattern bool
	// ForceLegacyCapture uses the synchronous CoreGraphics capture path on
	// macOS instead of ScreenCaptureKit. Useful when SCK blocks.
	ForceLegacyCapture bool
	// Display is the monitor index being captured. It can be changed while
	// the agent is running through POST /display.
	Display int
	// Device bridge configuration (all optional).
	AudioInput   string
	CameraDevice string
	SerialPath   string
	Forwards     map[int]bool
	// VirtualCamera is a v4l2loopback device (Linux) fed by guest camera frames.
	VirtualCamera string
	// USBIP enables USB/IP export controls.
	USBIP bool
	LANIP string
	WANIP string
	Port  int

	mu        sync.Mutex
	raw       *image.RGBA
	pngCache  []byte
	pngSeq    uint64
	frameJPG  []byte
	frameSeq  uint64
	frameHash uint64
	vt        *mediadevices.VideoTrack

	// Capture health: ScreenCaptureKit can block forever on some macOS
	// builds, so the HTTP loop uses a watchdog and then falls back to the
	// synchronous CoreGraphics path.
	captureMu     sync.Mutex
	captureLegacy bool
	captureBusy   bool

	clipMu   sync.Mutex
	clipText string
	// clipReadAt is when the operating system clipboard was last read.
	clipReadAt time.Time

	recordMu     sync.Mutex
	recording    bool
	recordCancel context.CancelFunc
	recordPath   string

	done     chan struct{}
	stopOnce sync.Once

	dispMu sync.RWMutex

	guestCamMu  sync.Mutex
	guestCamJPG []byte
	guestCamSeq uint64
	guestCamAt  time.Time

	failMu sync.Mutex
	fails  map[string]*failEntry

	// Sessions are short-lived tokens handed out by POST /session. They let
	// a browser authenticate without putting the access password into a URL.
	sessMu   sync.Mutex
	sessions map[string]time.Time

	// mediaTokens are purpose-scoped, very short-lived tokens for players
	// (ffplay, native media elements) that can only be given a URL. They are
	// only valid for read-only media paths, so a leaked URL cannot drive
	// input, read the clipboard, or manage devices.
	mediaMu     sync.Mutex
	mediaTokens map[string]mediaGrant

	// relaySecret proves that a request was forwarded by this process'
	// relay client. Only those requests may set X-Forwarded-For.
	relaySecret string
}

type failEntry struct {
	count int
	first time.Time
}

const (
	maxInputBody  = 64 << 10
	maxUploadSize = 512 << 20
	maxUploadFile = 256 << 20
	authWindow    = time.Minute
	authMaxFails  = 20
	// maxFailEntries bounds the throttle table so a flood of failed logins
	// from spoofed addresses cannot grow it without limit.
	maxFailEntries = 4096
	// sessionTTL is how long a browser session stays valid.
	sessionTTL = 12 * time.Hour
	// maxSessions bounds the session table.
	maxSessions = 64
	// mediaTokenTTL is intentionally short: a media URL may end up in a
	// process list or a player's log, so it expires quickly.
	mediaTokenTTL = 2 * time.Minute
	// maxMediaTokens bounds the media token table.
	maxMediaTokens = 64
	// sessionCookie is the name of the browser session cookie.
	sessionCookie = "rdplink_sid"
	// clipboardCacheTTL is how long a clipboard read is reused.
	clipboardCacheTTL = 750 * time.Millisecond
)

func New(peerID, alias, password string, port int) *Agent {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		secret = []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	a := &Agent{PeerID: peerID, Alias: alias, Password: password, Port: port,
		fails:       map[string]*failEntry{},
		sessions:    map[string]time.Time{},
		mediaTokens: map[string]mediaGrant{},
		relaySecret: hex.EncodeToString(secret),
		Forwards:    map[int]bool{}, done: make(chan struct{})}
	a.LANIP = lanIP()
	a.WANIP = wanIP()
	return a
}

// RelaySecret returns the per-process secret that marks relay-forwarded
// requests. It is handed to the in-process relay client.
func (a *Agent) RelaySecret() string { return a.relaySecret }

// Done is closed when the agent stops. Background workers use it to shut
// down together with the host.
func (a *Agent) Done() <-chan struct{} { return a.done }

// FramePNG returns the latest frame as PNG, encoding lazily on the first
// request for each captured frame.
// Stop shuts the host agent down gracefully.
func (a *Agent) Stop() {
	a.stopOnce.Do(func() { close(a.done) })
}

func (a *Agent) FramePNG() ([]byte, uint64) {
	a.mu.Lock()
	seq := a.frameSeq
	if a.pngSeq == seq && len(a.pngCache) > 0 {
		defer a.mu.Unlock()
		return a.pngCache, seq
	}
	raw := a.raw
	a.mu.Unlock()
	if raw == nil {
		return nil, seq
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, raw); err != nil {
		return nil, seq
	}
	a.mu.Lock()
	a.pngCache = buf.Bytes()
	a.pngSeq = seq
	a.mu.Unlock()
	return buf.Bytes(), seq
}

// FrameJPEG returns the latest JPEG frame and a content hash. The hash only
// changes when the captured pixels change, so a viewer polling a static screen
// receives 304 Not Modified instead of re-downloading identical bytes.
// SetDisplay switches the captured monitor. Out-of-range indexes are ignored.
func (a *Agent) SetDisplay(index int) bool {
	if index < 0 || index >= displayCount() {
		return false
	}
	a.dispMu.Lock()
	a.Display = index
	a.dispMu.Unlock()
	return true
}

// CurrentDisplay returns the monitor index currently captured.
func (a *Agent) CurrentDisplay() int {
	a.dispMu.RLock()
	defer a.dispMu.RUnlock()
	return a.Display
}

func (a *Agent) FrameJPEG() ([]byte, string, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.frameJPG) > 0 {
		return a.frameJPG, "image/jpeg", a.frameHash
	}
	return nil, "image/jpeg", a.frameHash
}

// auth enforces the access password, with the same throttling used by every
// endpoint (including the device bridge).
//
// Three credentials are accepted:
//
//   - X-Office-Password: the access password (desktop viewers, CLI tools);
//   - the rdplink_sid cookie: a session handed out by POST /session
//     (browsers, where media elements cannot set headers);
//   - ?token=: a purpose-scoped media token from POST /media-token, only on
//     read-only media paths. A full session token is never accepted in a URL.
//
// The access password is never accepted as a URL query parameter, so it does
// not end up in proxy logs, browser history or the process list.
func (a *Agent) auth(w http.ResponseWriter, r *http.Request) bool {
	// Every endpoint goes through here, so the origin check lives here and
	// not in a wrapper that the device bridge could bypass.
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return false
	}
	ip := clientIP(r, a.relaySecret)
	if a.Password == "" {
		http.Error(w, "host password not configured", http.StatusForbidden)
		return false
	}
	// Evaluate the credential before applying the throttle. A client that
	// knows the password must not stay locked out because another client on
	// the same address guessed wrong; wrong attempts still get 429.
	if supplied := r.Header.Get("X-Office-Password"); supplied != "" {
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(a.Password)) == 1 {
			a.clearFailures(ip)
			return true
		}
		if a.throttled(ip) {
			a.tooManyAttempts(w)
			return false
		}
		a.noteFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if token := cookieSessionToken(r); token != "" && a.validSession(token) {
		a.clearFailures(ip)
		return true
	}
	if token := r.URL.Query().Get("token"); token != "" &&
		mediaPathAllowed(r.URL.Path) && a.validMediaToken(token, mediaScope) {
		a.clearFailures(ip)
		return true
	}
	if a.throttled(ip) {
		a.tooManyAttempts(w)
		return false
	}
	a.noteFailure(ip)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// tooManyAttempts writes a distinct 429 so clients can tell a temporary
// throttle from a wrong password and back off for the right amount of time.
func (a *Agent) tooManyAttempts(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(authWindow/time.Second)))
	http.Error(w, "too many attempts", http.StatusTooManyRequests)
}

// cookieSessionToken extracts a browser session token from the cookie only.
func cookieSessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

// mediaGrant is a short-lived credential limited to one purpose.
type mediaGrant struct {
	expiry time.Time
	scope  string
}

const mediaScope = "media"

// mediaPathAllowed lists the read-only endpoints that a URL token may reach.
// Everything else (input, clipboard, files, devices) requires a session
// cookie or the access password header.
func mediaPathAllowed(path string) bool {
	switch path {
	case "/frame.jpg", "/frame.png", "/stream.mjpg", "/audio", "/camera.mjpg":
		return true
	default:
		return false
	}
}

// newMediaToken issues a purpose-scoped token for header-less players.
func (a *Agent) newMediaToken(scope string) string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	token := hex.EncodeToString(buf)
	now := time.Now()
	a.mediaMu.Lock()
	defer a.mediaMu.Unlock()
	for key, grant := range a.mediaTokens {
		if now.After(grant.expiry) {
			delete(a.mediaTokens, key)
		}
	}
	if len(a.mediaTokens) >= maxMediaTokens {
		oldestKey, oldest := "", now
		for key, grant := range a.mediaTokens {
			if grant.expiry.Before(oldest) {
				oldestKey, oldest = key, grant.expiry
			}
		}
		delete(a.mediaTokens, oldestKey)
	}
	a.mediaTokens[token] = mediaGrant{expiry: now.Add(mediaTokenTTL), scope: scope}
	return token
}

// validMediaToken reports whether a media token is active and in scope.
func (a *Agent) validMediaToken(token, scope string) bool {
	a.mediaMu.Lock()
	defer a.mediaMu.Unlock()
	grant, ok := a.mediaTokens[token]
	if !ok {
		return false
	}
	if time.Now().After(grant.expiry) || grant.scope != scope {
		delete(a.mediaTokens, token)
		return false
	}
	return true
}

// newSession issues a session token.
func (a *Agent) newSession() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	token := hex.EncodeToString(buf)
	now := time.Now()
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	for key, expiry := range a.sessions {
		if now.After(expiry) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= maxSessions {
		// Drop the oldest session when the table is full.
		oldestKey, oldest := "", now
		for key, expiry := range a.sessions {
			if expiry.Before(oldest) {
				oldestKey, oldest = key, expiry
			}
		}
		delete(a.sessions, oldestKey)
	}
	a.sessions[token] = now.Add(sessionTTL)
	return token
}

// validSession reports whether a session token is still active.
func (a *Agent) validSession(token string) bool {
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	expiry, ok := a.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(a.sessions, token)
		return false
	}
	return true
}

// handleSession exchanges the access password for a session cookie.
func (a *Agent) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	ip := clientIP(r, a.relaySecret)
	if a.Password == "" {
		http.Error(w, "host password not configured", http.StatusForbidden)
		return
	}
	supplied := r.Header.Get("X-Office-Password")
	if supplied == "" {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err == nil {
			supplied = body.Password
		}
	}
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(a.Password)) != 1 {
		if a.throttled(ip) {
			a.tooManyAttempts(w)
			return
		}
		a.noteFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	a.clearFailures(ip)
	token := a.newSession()
	if token == "" {
		http.Error(w, "session failed", http.StatusInternalServerError)
		return
	}
	// The cookie is marked Secure whenever the connection is encrypted. On a
	// plain LAN connection it has to stay usable, so the flag follows the
	// transport instead of being hard-coded.
	// #nosec G124 -- Secure is set for every HTTPS request (see above).
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"token":     token,
		"expiresIn": int(sessionTTL / time.Second),
	})
}

// handleMediaToken issues a short-lived, read-only token for players that
// cannot send an Authorization header (ffplay). It requires a real session
// cookie or the access password, so a media token can never mint another.
func (a *Agent) handleMediaToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	ip := clientIP(r, a.relaySecret)
	if a.Password == "" {
		http.Error(w, "host password not configured", http.StatusForbidden)
		return
	}
	authorized := false
	if supplied := r.Header.Get("X-Office-Password"); supplied != "" {
		authorized = subtle.ConstantTimeCompare([]byte(supplied), []byte(a.Password)) == 1
	} else if token := cookieSessionToken(r); token != "" {
		authorized = a.validSession(token)
	}
	if !authorized {
		if a.throttled(ip) {
			a.tooManyAttempts(w)
			return
		}
		a.noteFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	a.clearFailures(ip)
	token := a.newMediaToken(mediaScope)
	if token == "" {
		http.Error(w, "token failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"token":     token,
		"scope":     mediaScope,
		"expiresIn": int(mediaTokenTTL / time.Second),
	})
}

// sameOrigin reports whether a request comes from this host's own pages.
// Browsers send Origin on state-changing requests, so this blocks a web page
// on another site from driving the host with a stolen or guessed password.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // native clients do not send Origin
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func (a *Agent) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/session", a.handleSession)
	mux.HandleFunc("/media-token", a.handleMediaToken)
	// auth accepts the access password, a session cookie or a media token,
	// and rejects cross-origin requests. It lives on the Agent so that the
	// device bridge endpoints are covered as well.
	auth := a.auth
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"peerId": a.PeerID,
			"alias":  a.Alias,
			"lanIp":  a.LANIP,
			"wanIp":  a.WANIP,
			"port":   a.Port,
			"input":  input.PermissionError() == nil,
		})
	})
	mux.HandleFunc("/displays", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		type display struct {
			Index   int  `json:"index"`
			Width   int  `json:"width"`
			Height  int  `json:"height"`
			Primary bool `json:"primary"`
		}
		count := displayCount()
		list := make([]display, 0, count)
		for i := 0; i < count; i++ {
			w, h := displaySize(i)
			list = append(list, display{Index: i, Width: w, Height: h, Primary: i == 0})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": count, "active": a.CurrentDisplay(), "displays": list,
		})
	})
	mux.HandleFunc("/display", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Index int `json:"index"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !a.SetDisplay(body.Index) {
			http.Error(w, "no such display", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/frame.png", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f, seq := a.FramePNG()
		if len(f) == 0 {
			http.Error(w, "no frame", http.StatusServiceUnavailable)
			return
		}
		etag := fmt.Sprintf("\"p%d\"", seq)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(f)
	})
	mux.HandleFunc("/frame.jpg", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f, mime, seq := a.FrameJPEG()
		if len(f) == 0 {
			http.Error(w, "no frame", http.StatusServiceUnavailable)
			return
		}
		etag := fmt.Sprintf("\"j%d\"", seq)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Write(f)
	})
	mux.HandleFunc("/input", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if a.ReadOnly {
			http.Error(w, "read-only", http.StatusForbidden)
			return
		}
		var ev map[string]any
		json.NewDecoder(http.MaxBytesReader(w, r.Body, maxInputBody)).Decode(&ev)
		if err := input.Inject(ev); err != nil {
			if errors.Is(err, input.ErrPermissionDenied) {
				http.Error(w,
					"accessibility permission required: grant RDP Link access in "+
						"System Settings → Privacy & Security → Accessibility",
					http.StatusForbidden)
				return
			}
			fmt.Printf("input inject: %v\n", err)
			http.Error(w, "input injection failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// clipboard sync
	mux.HandleFunc("/clipboard", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"text": a.GetClipboard()})
			return
		}
		if r.Method == http.MethodPost {
			var body struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			a.SetClipboard(body.Text)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "method", http.StatusMethodNotAllowed)
	})

	// file transfer
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		entries, _ := os.ReadDir(a.InboxDir())
		names := []string{}
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(names)
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		path, ok := safeJoin(a.InboxDir(), r.URL.Query().Get("name"))
		if !ok {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		// safeJoin rejects separators and parent traversal.
		info, err := os.Lstat(path) // #nosec G304 G703
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// The file is a download, not a page: never let the browser render
		// it inline. mime.FormatMediaType quotes the name safely, even when
		// it contains a double quote.
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment",
			map[string]string{"filename": sanitizeName(r.URL.Query().Get("name"))}))
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The guest may name the file explicitly (?name=). Otherwise the
		// multipart filename is used.
		requested := sanitizeName(r.URL.Query().Get("name"))
		saved := 0
		var failure error
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, "invalid multipart body", http.StatusBadRequest)
				return
			}
			name := sanitizeName(part.FileName())
			if requested != "" {
				name = requested
			}
			path, ok := safeJoin(a.InboxDir(), name)
			if !ok || strings.HasPrefix(name, ".") {
				_ = part.Close()
				continue
			}
			// #nosec G304 G703 -- safeJoin rejects separators and parent traversal.
			f, err := os.Create(path)
			if err != nil {
				failure = err
				_ = part.Close()
				continue
			}
			written, copyErr := io.Copy(f, io.LimitReader(part, maxUploadFile))
			closeErr := f.Close()
			_ = part.Close()
			if copyErr != nil {
				failure = copyErr
				_ = os.Remove(path)
				continue
			}
			if closeErr != nil {
				failure = closeErr
				_ = os.Remove(path)
				continue
			}
			if written >= maxUploadFile {
				_ = os.Remove(path)
				http.Error(w, "file exceeds the upload limit", http.StatusRequestEntityTooLarge)
				return
			}
			saved++
		}
		if failure != nil {
			http.Error(w, "upload failed: "+failure.Error(), http.StatusInternalServerError)
			return
		}
		if saved == 0 {
			http.Error(w, "no file part in the upload", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// session recording (consent-gated by host running the agent)
	mux.HandleFunc("/record/start", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("name")
		if err := a.StartRecording(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/record/stop", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		a.StopRecording()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/record/status", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		rec, path := a.RecordingStatus()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"recording": rec, "path": path})
	})

	mux.HandleFunc("/capabilities", a.handleCapabilities)
	mux.HandleFunc("/audio", a.handleAudio)
	mux.HandleFunc("/mic", a.handleMicWS)
	mux.HandleFunc("/camera.mjpg", a.handleCamera)
	mux.HandleFunc("/camera/in", a.handleGuestCameraWS)
	mux.HandleFunc("/usb/list", a.handleUSBList)
	mux.HandleFunc("/usb/bind", a.handleUSBBind)
	mux.HandleFunc("/usb/unbind", a.handleUSBUnbind)
	mux.HandleFunc("/serial", a.handleSerialWS)
	mux.HandleFunc("/port/", a.handlePortProxy)

	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(logoPNG)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(viewerHTML))
	})
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ui" && r.URL.Path != "/ui/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(uiHTML))
	})
	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if !auth(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go a.Stop()
	})
	addr4 := fmt.Sprintf("0.0.0.0:%d", a.Port)
	ln4, err := net.Listen("tcp4", addr4)
	if err != nil {
		return err
	}
	listeners := []net.Listener{ln4}
	// Listen on IPv6 separately. A single ":port" listener asks for a
	// dual-stack socket, and macOS 26 completes the TCP handshake for LAN
	// IPv4 clients on that socket without ever delivering them to accept().
	// Two explicit sockets (tcp4 + tcp6 with IPV6_V6ONLY) work everywhere.
	if ln6, err := net.Listen("tcp6", fmt.Sprintf("[::]:%d", a.Port)); err == nil {
		listeners = append(listeners, ln6)
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// #nosec G102 -- the host is intentionally reachable on the local network.
	errCh := make(chan error, len(listeners))
	for _, ln := range listeners {
		go func(l net.Listener) { errCh <- server.Serve(l) }(ln)
	}
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-a.done:
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	}
}

//go:embed viewer.html
var viewerHTML string

//go:embed ui.html
var uiHTML string

//go:embed logo.png
var logoPNG []byte

func lanIP() string {
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

func wanIP() string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

// GetClipboard returns the host clipboard.
//
// The operating system clipboard is read on demand and cached for a moment.
// A background poll every 500 ms would spawn a helper process twice a second
// on every platform (powershell.exe on Windows) even when no guest is
// connected.
func (a *Agent) GetClipboard() string {
	a.clipMu.Lock()
	if !a.clipReadAt.IsZero() && time.Since(a.clipReadAt) < clipboardCacheTTL {
		text := a.clipText
		a.clipMu.Unlock()
		return text
	}
	a.clipMu.Unlock()

	text := clipboard.Read()
	a.clipMu.Lock()
	a.clipText = text
	a.clipReadAt = time.Now()
	a.clipMu.Unlock()
	return text
}

// SetClipboard writes text to the host clipboard and to the cache.
func (a *Agent) SetClipboard(text string) {
	a.clipMu.Lock()
	a.clipText = text
	a.clipReadAt = time.Now()
	a.clipMu.Unlock()
	clipboard.Write(text)
}

func (a *Agent) InboxDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "office-rdp", "inbox")
	os.MkdirAll(dir, 0o700)
	return dir
}

// safeJoin resolves name inside base and rejects traversal.
func safeJoin(base, name string) (string, bool) {
	name = sanitizeName(name)
	if name == "" {
		return "", false
	}
	path := filepath.Join(base, name)
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return path, true
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return name
}

func (a *Agent) StartRecording(name string) error {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	if a.recording {
		return nil
	}
	if a.vt == nil {
		return fmt.Errorf("no capture track")
	}
	if name == "" {
		name = fmt.Sprintf("recording-%d.h264", time.Now().Unix())
	}
	name = sanitizeName(name)
	if name == "" {
		return fmt.Errorf("invalid name")
	}
	dir := filepath.Join(a.InboxDir(), "recordings")
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, name)
	ctx, cancel := context.WithCancel(context.Background())
	a.recordCancel = cancel
	a.recording = true
	a.recordPath = path
	go func() {
		reader, err := a.vt.NewEncodedIOReader("video/h264")
		if err != nil {
			auditLog("record error: " + err.Error())
			a.StopRecording()
			return
		}
		defer reader.Close()
		f, err := os.Create(path) // #nosec G304 G703 -- path is sanitized in StartRecording
		if err != nil {
			auditLog("record file error: " + err.Error())
			a.StopRecording()
			return
		}
		defer f.Close()
		buf := make([]byte, 64*1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := reader.Read(buf)
			if n > 0 {
				f.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	auditLog("recording started " + path)
	return nil
}

func (a *Agent) StopRecording() {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	if !a.recording {
		return
	}
	if a.recordCancel != nil {
		a.recordCancel()
	}
	a.recording = false
	auditLog("recording stopped " + a.recordPath)
}

func (a *Agent) RecordingStatus() (bool, string) {
	a.recordMu.Lock()
	defer a.recordMu.Unlock()
	return a.recording, a.recordPath
}

// clientIP returns the address used for throttling. X-Forwarded-For is only
// believed when the request carries this process' relay secret, because any
// local process (or a guest tunnelled in through adb/ssh port forwarding)
// could otherwise spoof an address and bypass the limiter.
func clientIP(r *http.Request, relaySecret string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if relaySecret == "" {
		return host
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Rdp-Relay-Secret")),
		[]byte(relaySecret)) != 1 {
		return host
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.Split(xff, ",")[0]; strings.TrimSpace(first) != "" {
			return strings.TrimSpace(first)
		}
	}
	return host
}

func (a *Agent) throttled(ip string) bool {
	a.failMu.Lock()
	defer a.failMu.Unlock()
	entry, ok := a.fails[ip]
	if !ok {
		return false
	}
	if time.Since(entry.first) > authWindow {
		delete(a.fails, ip)
		return false
	}
	return entry.count >= authMaxFails
}

func (a *Agent) noteFailure(ip string) {
	a.failMu.Lock()
	defer a.failMu.Unlock()
	entry, ok := a.fails[ip]
	if !ok || time.Since(entry.first) > authWindow {
		if len(a.fails) >= maxFailEntries {
			// Keep the table bounded: drop the oldest entries instead of
			// growing without limit.
			now := time.Now()
			for key, item := range a.fails {
				if now.Sub(item.first) > authWindow {
					delete(a.fails, key)
				}
			}
			if len(a.fails) >= maxFailEntries {
				for key := range a.fails {
					delete(a.fails, key)
					break
				}
			}
		}
		a.fails[ip] = &failEntry{count: 1, first: time.Now()}
		return
	}
	entry.count++
}

func (a *Agent) clearFailures(ip string) {
	a.failMu.Lock()
	defer a.failMu.Unlock()
	delete(a.fails, ip)
}
