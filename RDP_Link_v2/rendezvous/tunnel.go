package rendezvous

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// The tunnel is a reverse HTTP proxy: the host dials out to the rendezvous and
// keeps a WebSocket open, so it works behind NAT without port forwarding or a
// stable public IP. Guests talk to /t/<peerId>/... and the rendezvous forwards
// each request over the host's own connection.

const maxRelayBody = 32 << 20 // 32 MiB per request/response over the relay

type tunnelRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	// Stream asks the host to forward the response incrementally (audio,
	// MJPEG camera, long downloads).
	Stream bool `json:"stream,omitempty"`
	// Cancel tells the host to abandon the request with this ID. It is sent
	// when the guest disconnects, so the host can stop capturing and close
	// the local stream instead of streaming into the void.
	Cancel bool `json:"cancel,omitempty"`
}

type tunnelReply struct {
	Type    string            `json:"type,omitempty"` // "", "stream", "chunk", "end"
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Data    string            `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type tunnel struct {
	peerID  string
	ws      *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan tunnelReply
	closed  chan struct{}
	once    sync.Once
}

func (t *tunnel) close() {
	t.once.Do(func() { close(t.closed); _ = t.ws.Close() })
}

func (t *tunnel) readLoop(s *Server) {
	defer func() {
		s.mu.Lock()
		if s.tunnels[t.peerID] == t {
			delete(s.tunnels, t.peerID)
		}
		s.mu.Unlock()
		t.close()
	}()
	for {
		_, data, err := t.ws.ReadMessage()
		if err != nil {
			return
		}
		var reply tunnelReply
		if json.Unmarshal(data, &reply) != nil || reply.ID == "" {
			continue
		}
		t.mu.Lock()
		ch := t.pending[reply.ID]
		// Streamed replies keep the channel until the host sends "end".
		if reply.Type == "" || reply.Type == "end" {
			delete(t.pending, reply.ID)
		}
		t.mu.Unlock()
		if ch == nil {
			continue
		}
		// Never block the reader: a guest that vanished mid-stream would
		// otherwise stall the whole tunnel (and every other guest) once the
		// buffer filled up.
		select {
		case ch <- reply:
		default:
			// The consumer is gone or too slow. Live streams may drop a
			// frame; a missing "end" is recovered by the cancel frame.
		}
	}
}

func (t *tunnel) send(req tunnelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.ws.WriteMessage(websocket.TextMessage, data)
}

var tunnelSeq uint64

// handleTunnel upgrades a host connection and registers it under its peer ID.
// The upgrade request must carry a valid Ed25519 signature for the peer ID,
// so nobody else can take over a host's tunnel.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	peerID := normalizeID(r.URL.Query().Get("peerId"))
	if peerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	signed, ok := s.verifyPeer(r, nil)
	if !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	if signed != peerID {
		http.Error(w, "peer id does not match the signature", http.StatusForbidden)
		return
	}

	s.mu.Lock()
	if _, exists := s.tunnels[peerID]; exists {
		s.mu.Unlock()
		http.Error(w, "already connected", http.StatusConflict)
		return
	}
	s.mu.Unlock()

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	t := &tunnel{peerID: peerID, ws: ws, pending: map[string]chan tunnelReply{},
		closed: make(chan struct{})}
	s.mu.Lock()
	s.tunnels[peerID] = t
	s.mu.Unlock()
	go t.readLoop(s)
}

func copyHeaders(src http.Header, allow map[string]bool) map[string]string {
	out := map[string]string{}
	for k, v := range src {
		if allow[strings.ToLower(k)] && len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

var requestHeaders = map[string]bool{
	"content-type":        true,
	"x-office-password":   true,
	"if-none-match":       true,
	"accept":              true,
	"content-disposition": true,
	"x-forwarded-for":     true,
	// The session cookie lets a browser guest authenticate through the
	// relay without putting the password in a URL.
	"cookie": true,
}

var responseHeaders = map[string]bool{
	"content-type":        true,
	"etag":                true,
	"cache-control":       true,
	"content-disposition": true,
	"x-forwarded-for":     true,
	// The host's session cookie must reach the guest through the relay.
	"set-cookie": true,
}

// handleProxy forwards /t/<peerId>/<path> to the host over its tunnel.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/t/")
	peerID, tail, hasTail := strings.Cut(rest, "/")
	peerID = normalizeID(peerID)
	if peerID == "" || strings.Trim(peerID, "0123456789") != "" {
		http.Error(w, "invalid peer id", http.StatusBadRequest)
		return
	}
	// Redirect only the tunnel root (/t/<id>) so relative URLs resolve under
	// the prefix; sub-paths such as frame.jpg must not gain a slash. Build the
	// target from the validated peer ID rather than the raw request path.
	if !hasTail {
		target := "/t/" + peerID + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		// #nosec G710 -- target is rebuilt from a digits-only peer ID.
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if peerID == "" {
		http.Error(w, "peer id required", http.StatusBadRequest)
		return
	}
	if tail == "" {
		tail = "/"
	} else {
		tail = "/" + tail
	}

	s.mu.RLock()
	t := s.tunnels[peerID]
	s.mu.RUnlock()
	if t == nil {
		http.Error(w, "host is not connected to the relay", http.StatusBadGateway)
		return
	}

	streaming := tail == "/audio" || tail == "/camera.mjpg"

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRelayBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&tunnelSeq, 1))
	ch := make(chan tunnelReply, 32)
	t.mu.Lock()
	t.pending[id] = ch
	t.mu.Unlock()
	// The pending entry is always removed, whatever happens below: a leaked
	// entry would keep the reply channel (and its buffer) alive forever.
	defer func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
	}()

	headers := copyHeaders(r.Header, requestHeaders)
	clientHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	if clientHost == "" {
		clientHost = r.RemoteAddr
	}
	headers["X-Forwarded-For"] = clientHost
	req := tunnelRequest{
		ID:      id,
		Method:  r.Method,
		Path:    tail,
		Query:   r.URL.RawQuery,
		Headers: headers,
		Body:    base64.StdEncoding.EncodeToString(body),
		Stream:  streaming,
	}
	if err := t.send(req); err != nil {
		http.Error(w, "relay send failed", http.StatusBadGateway)
		return
	}

	if streaming {
		s.proxyStream(w, r, t, id, peerID, ch)
		return
	}

	select {
	case reply := <-ch:
		if reply.Error != "" {
			http.Error(w, reply.Error, http.StatusBadGateway)
			return
		}
		for k, v := range reply.Headers {
			if !responseHeaders[strings.ToLower(k)] {
				continue
			}
			if strings.EqualFold(k, "Set-Cookie") {
				v = scopeSetCookie(v, "/t/"+peerID)
			}
			w.Header().Set(k, v)
		}
		data, _ := base64.StdEncoding.DecodeString(reply.Body)
		w.WriteHeader(reply.Status)
		_, _ = w.Write(data)
	case <-time.After(30 * time.Second):
		http.Error(w, "relay timeout", http.StatusGatewayTimeout)
	case <-t.closed:
		http.Error(w, "host disconnected", http.StatusBadGateway)
	}
}

// proxyStream relays an incremental response (audio/MJPEG) to the guest.
//
// When the guest goes away the stream is cancelled: the host is told to stop
// capturing and the local request is aborted, so neither side keeps a camera
// or an audio device busy for a viewer that is no longer there.
func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request, t *tunnel,
	id, peerID string, ch chan tunnelReply) {
	flusher, _ := w.(http.Flusher)
	started := false
	cancelled := false
	defer func() {
		if !cancelled {
			t.cancel(id)
		}
	}()
	for {
		select {
		case frame := <-ch:
			switch frame.Type {
			case "stream":
				for k, v := range frame.Headers {
					if !responseHeaders[strings.ToLower(k)] {
						continue
					}
					if strings.EqualFold(k, "Set-Cookie") {
						v = scopeSetCookie(v, "/t/"+peerID)
					}
					w.Header().Set(k, v)
				}
				w.WriteHeader(frame.Status)
				started = true
				if flusher != nil {
					flusher.Flush()
				}
			case "chunk":
				data, _ := base64.StdEncoding.DecodeString(frame.Data)
				if _, err := w.Write(data); err != nil {
					cancelled = true
					t.cancel(id)
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			case "end":
				cancelled = true
				return
			default:
				if frame.Error != "" && !started {
					http.Error(w, frame.Error, http.StatusBadGateway)
					cancelled = true
					t.cancel(id)
					return
				}
			}
		case <-r.Context().Done():
			cancelled = true
			t.cancel(id)
			return
		case <-time.After(2 * time.Minute):
			cancelled = true
			t.cancel(id)
			if !started {
				http.Error(w, "stream timeout", http.StatusGatewayTimeout)
			}
			return
		}
	}
}

// scopeSetCookie limits a cookie to the tunnelled host.
//
// The host sends its session cookie with Path=/. On the relay that would
// cover every other tunnelled host as well: a guest that connects to two
// devices on the same relay would send the first cookie to the second one
// and overwrite it when logging in.
//
// The value is rewritten textually instead of being parsed and rebuilt, so
// the host's own attributes (HttpOnly, SameSite, Secure, Expires) survive
// untouched. Set-Cookie attributes are separated by semicolons and an
// Expires date never contains one, so splitting on ";" is safe.
func scopeSetCookie(raw, prefix string) string {
	parts := strings.Split(raw, ";")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(trimmed), "path=") {
			continue
		}
		value := strings.TrimSpace(trimmed[len("path="):])
		if value == "" || value == "/" {
			parts[i] = " Path=" + prefix + "/"
			return strings.Join(parts, ";")
		}
		return raw
	}
	return raw
}

// cancel asks the host to abandon a request.
func (t *tunnel) cancel(id string) {
	_ = t.send(tunnelRequest{ID: id, Cancel: true})
}

// relayURL builds the public URL a guest should open for a tunnelled host.
func (s *Server) relayURL(r *http.Request, peerID string) string {
	base := s.publicURL
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return strings.TrimRight(base, "/") + "/t/" + url.PathEscape(peerID) + "/"
}
