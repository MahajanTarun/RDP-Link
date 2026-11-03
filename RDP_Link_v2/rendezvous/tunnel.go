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
		if ch != nil {
			ch <- reply
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
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	peerID := normalizeID(r.URL.Query().Get("peerId"))
	token := r.URL.Query().Get("token")
	if peerID == "" || token == "" {
		http.Error(w, "peerId and token required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if _, exists := s.tunnels[peerID]; exists {
		s.mu.Unlock()
		http.Error(w, "already connected", http.StatusConflict)
		return
	}
	if saved, ok := s.tunnelTokens[peerID]; ok && saved != token {
		s.mu.Unlock()
		http.Error(w, "invalid tunnel token", http.StatusForbidden)
		return
	}
	s.tunnelTokens[peerID] = token
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
}

var responseHeaders = map[string]bool{
	"content-type":        true,
	"etag":                true,
	"cache-control":       true,
	"content-disposition": true,
	"x-forwarded-for":     true,
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
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		http.Error(w, "relay send failed", http.StatusBadGateway)
		return
	}

	if streaming {
		s.proxyStream(w, r, ch)
		return
	}

	select {
	case reply := <-ch:
		if reply.Error != "" {
			http.Error(w, reply.Error, http.StatusBadGateway)
			return
		}
		for k, v := range reply.Headers {
			if responseHeaders[strings.ToLower(k)] {
				w.Header().Set(k, v)
			}
		}
		data, _ := base64.StdEncoding.DecodeString(reply.Body)
		w.WriteHeader(reply.Status)
		_, _ = w.Write(data)
	case <-time.After(30 * time.Second):
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		http.Error(w, "relay timeout", http.StatusGatewayTimeout)
	case <-t.closed:
		http.Error(w, "host disconnected", http.StatusBadGateway)
	}
}

// proxyStream relays an incremental response (audio/MJPEG) to the guest.
func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request, ch chan tunnelReply) {
	flusher, _ := w.(http.Flusher)
	started := false
	for {
		select {
		case frame := <-ch:
			switch frame.Type {
			case "stream":
				for k, v := range frame.Headers {
					if responseHeaders[strings.ToLower(k)] {
						w.Header().Set(k, v)
					}
				}
				w.WriteHeader(frame.Status)
				started = true
				if flusher != nil {
					flusher.Flush()
				}
			case "chunk":
				data, _ := base64.StdEncoding.DecodeString(frame.Data)
				if _, err := w.Write(data); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			case "end":
				return
			default:
				if frame.Error != "" && !started {
					http.Error(w, frame.Error, http.StatusBadGateway)
					return
				}
			}
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Minute):
			if !started {
				http.Error(w, "stream timeout", http.StatusGatewayTimeout)
			}
			return
		}
	}
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
