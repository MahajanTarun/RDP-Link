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
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

	clipMu   sync.Mutex
	clipText string

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
	// a browser (or a media player that cannot set headers) authenticate
	// without putting the access password into a URL.
	sessMu   sync.Mutex
	sessions map[string]time.Time

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
	// sessionCookie is the name of the browser session cookie.
	sessionCookie = "rdplink_sid"
)

func New(peerID, alias, password string, port int) *Agent {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		secret = []byte(fmt.Sprintf("%d", time.Now().UnixNano()))
	}
	a := &Agent{PeerID: peerID, Alias: alias, Password: password, Port: port,
		fails:    map[string]*failEntry{},
		sessions: map[string]time.Time{}, relaySecret: hex.EncodeToString(secret),
		Forwards: map[int]bool{}, done: make(chan struct{})}
	a.LANIP = lanIP()
	a.WANIP = wanIP()
	return a
}

// RelaySecret returns the per-process secret that marks relay-forwarded
// requests. It is handed to the in-process relay client.
func (a *Agent) RelaySecret() string { return a.relaySecret }

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
//   - ?token=: the same session token, for players such as ffplay that can
//     only be given a URL.
//
// The access password is never accepted as a URL query parameter, so it does
// not end up in proxy logs, browser history or the process list.
func (a *Agent) auth(w http.ResponseWriter, r *http.Request) bool {
	ip := clientIP(r, a.relaySecret)
	if a.throttled(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return false
	}
	if a.Password == "" {
		http.Error(w, "host password not configured", http.StatusForbidden)
		return false
	}
	if supplied := r.Header.Get("X-Office-Password"); supplied != "" {
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(a.Password)) == 1 {
			a.clearFailures(ip)
			return true
		}
		a.noteFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if token := sessionToken(r); token != "" && a.validSession(token) {
		a.clearFailures(ip)
		return true
	}
	a.noteFailure(ip)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// sessionToken extracts the session token from the cookie or the query.
func sessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return r.URL.Query().Get("token")
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
	ip := clientIP(r, a.relaySecret)
	if a.throttled(ip) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
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
	// auth accepts the access password, a session cookie or a session token.
	// State-changing requests additionally have to come from this host's own
	// pages, so another site cannot drive the host with a leaked password.
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return false
		}
		return a.auth(w, r)
	}
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
			fmt.Printf("input inject: %v\n", err)
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
		// it inline.
		w.Header().Set("Content-Disposition",
			"attachment; filename=\""+sanitizeName(r.URL.Query().Get("name"))+"\"")
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
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			name := sanitizeName(part.FileName())
			if requested != "" {
				name = requested
			}
			path, ok := safeJoin(a.InboxDir(), name)
			if ok && !strings.HasPrefix(name, ".") {
				// #nosec G304 G703 -- safeJoin rejects separators and parent traversal.
				f, err := os.Create(path)
				if err == nil {
					_, _ = io.Copy(f, io.LimitReader(part, maxUploadFile))
					f.Close()
				}
			}
			part.Close()
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

func (a *Agent) GetClipboard() string {
	a.clipMu.Lock()
	defer a.clipMu.Unlock()
	return a.clipText
}

func (a *Agent) SetClipboard(text string) {
	a.clipMu.Lock()
	a.clipText = text
	a.clipMu.Unlock()
	clipboard.Write(text)
}

func (a *Agent) StartClipboardSync() {
	go func() {
		for {
			t := clipboard.Read()
			a.clipMu.Lock()
			if t != a.clipText {
				a.clipText = t
			}
			a.clipMu.Unlock()
			time.Sleep(500 * time.Millisecond)
		}
	}()
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
