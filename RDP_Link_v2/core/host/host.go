// Package host implements the host agent: screen capture + HTTP stream + input.
package host

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eminentailabs/office-rdp/core/clipboard"
	"github.com/eminentailabs/office-rdp/core/input"
	"github.com/eminentailabs/office-rdp/core/protocol"
	"github.com/eminentailabs/office-rdp/core/signalling"
	"github.com/eminentailabs/office-rdp/core/transport"
	"github.com/pion/mediadevices"
	_ "github.com/pion/mediadevices/pkg/driver/screen"
	"github.com/pion/webrtc/v4"
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
	LANIP       string
	WANIP       string
	Port        int

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

	inputHolder string
	inputMu     sync.Mutex

	recordMu     sync.Mutex
	recording    bool
	recordCancel context.CancelFunc
	recordPath   string

	done     chan struct{}
	stopOnce sync.Once

	muPeers sync.Mutex
	peers   map[string]*transport.Peer

	failMu sync.Mutex
	fails  map[string]*failEntry
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
)

func New(peerID, alias, password string, port int) *Agent {
	a := &Agent{PeerID: peerID, Alias: alias, Password: password, Port: port,
		peers: map[string]*transport.Peer{}, fails: map[string]*failEntry{},
		done: make(chan struct{})}
	a.LANIP = lanIP()
	a.WANIP = wanIP()
	return a
}

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
func (a *Agent) FrameJPEG() ([]byte, string, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.frameJPG) > 0 {
		return a.frameJPG, "image/jpeg", a.frameHash
	}
	return nil, "image/jpeg", a.frameHash
}

func (a *Agent) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"peerId": a.PeerID,
			"alias":  a.Alias,
			"lanIp":  a.LANIP,
			"wanIp":  a.WANIP,
			"port":   a.Port,
		})
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		ip := clientIP(r)
		if a.throttled(ip) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return false
		}
		if a.Password == "" {
			http.Error(w, "host password not configured", http.StatusForbidden)
			return false
		}
		supplied := r.URL.Query().Get("password")
		if supplied == "" {
			supplied = r.Header.Get("X-Office-Password")
		}
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(a.Password)) == 1 {
			a.clearFailures(ip)
			return true
		}
		a.noteFailure(ip)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
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
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			name := sanitizeName(part.FileName())
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

// RunSignaling connects to rendezvous and handles WebRTC offers.
func (a *Agent) RunSignaling(rendezvousURL string) {
	c, err := signalling.Connect(rendezvousURL, a.PeerID)
	if err != nil {
		fmt.Println("signalling connect:", err)
		return
	}
	defer c.Close()
	for msg := range c.Recv {
		t := protocol.ParseType(msg)
		switch t {
		case "offer":
			var offer protocol.Offer
			_ = json.Unmarshal(msg, &offer)
			a.handleOffer(c, offer)
		case "ice":
			var ic protocol.ICECandidate
			_ = json.Unmarshal(msg, &ic)
			a.handleICE(ic)
		}
	}
}

func (a *Agent) handleOffer(c *signalling.Client, offer protocol.Offer) {
	if subtle.ConstantTimeCompare([]byte(offer.Password), []byte(a.Password)) != 1 {
		fmt.Println("rejected offer: bad password from", offer.From)
		auditLog("rejected " + offer.From)
		return
	}
	auditLog("connect " + offer.From)
	peer, err := transport.NewPeer(transport.Config{STUNServers: []string{"stun:stun.l.google.com:19302"}})
	if err != nil {
		fmt.Println("peer:", err)
		return
	}
	if a.vt != nil {
		if _, err := peer.PC.AddTrack(a.vt); err != nil {
			fmt.Println("add track:", err)
		}
	}
	// data channel handler for input
	peer.PC.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if a.ReadOnly {
				return
			}
			var ev map[string]any
			_ = json.Unmarshal(m.Data, &ev)
			if err := input.Inject(ev); err != nil {
				fmt.Println("inject:", err)
			}
		})
	})
	// ICE candidates -> signalling
	peer.PC.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		ice := protocol.ICECandidate{Type: "ice", From: a.PeerID, To: offer.From, Candidate: cand.ToJSON().Candidate}
		_ = c.SendJSON(ice)
	})
	answer, err := peer.AcceptOffer(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP})
	if err != nil {
		fmt.Println("accept offer:", err)
		peer.Close()
		return
	}
	a.muPeers.Lock()
	a.peers[offer.From] = peer
	a.muPeers.Unlock()
	resp := protocol.Answer{Type: "answer", From: a.PeerID, To: offer.From, SDP: answer.SDP}
	_ = c.SendJSON(resp)
}

func (a *Agent) handleICE(ic protocol.ICECandidate) {
	a.muPeers.Lock()
	peer, ok := a.peers[ic.From]
	a.muPeers.Unlock()
	if !ok || peer == nil {
		return
	}
	_ = peer.AddICECandidate(webrtc.ICECandidateInit{Candidate: ic.Candidate})
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

func (a *Agent) RequestInput(peerID string) bool {
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	if a.inputHolder == "" || a.inputHolder == peerID {
		a.inputHolder = peerID
		return true
	}
	return false
}

func (a *Agent) ReleaseInput(peerID string) {
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	if a.inputHolder == peerID {
		a.inputHolder = ""
	}
}

func (a *Agent) InputHolder() string {
	a.inputMu.Lock()
	defer a.inputMu.Unlock()
	return a.inputHolder
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
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
