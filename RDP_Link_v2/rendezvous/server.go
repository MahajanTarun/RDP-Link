// Package rendezvous implements the registry/presence/signalling server.
package rendezvous

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// normalizeID makes "033 976 933" and "033976933" the same peer.
func normalizeID(id string) string { return strings.ReplaceAll(id, " ", "") }

type PeerInfo struct {
	PeerID   string         `json:"peerId"`
	Alias    string         `json:"alias"`
	Status   string         `json:"status"`
	Caps     map[string]any `json:"caps"`
	LastSeen time.Time      `json:"lastSeen"`
	Addr     string         `json:"addr,omitempty"`
	Relay    bool           `json:"relay,omitempty"`
	URL      string         `json:"url,omitempty"`
}

type Server struct {
	mu           sync.RWMutex
	peers        map[string]*PeerInfo
	conns        map[string]*websocket.Conn
	pairs        map[string]pendingPair
	policies     map[string]map[string]any
	tunnels      map[string]*tunnel
	tunnelTokens map[string]string
	publicURL    string
	upgrader     websocket.Upgrader
}

func NewServer() *Server {
	return &Server{peers: map[string]*PeerInfo{}, conns: map[string]*websocket.Conn{},
		pairs: map[string]pendingPair{}, policies: map[string]map[string]any{},
		tunnels: map[string]*tunnel{}, tunnelTokens: map[string]string{},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}
}

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var info PeerInfo
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if info.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	info.PeerID = normalizeID(info.PeerID)
	info.LastSeen = time.Now()
	if info.Status == "" {
		info.Status = "online"
	}
	s.mu.Lock()
	s.peers[info.PeerID] = &info
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) Resolve(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("peerId")
	if id == "" {
		// allow path /resolve/{id}
		id = r.URL.Path[len("/resolve/"):]
	}
	id = normalizeID(id)
	s.mu.RLock()
	p, ok := s.peers[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// expiry: 2 min
	if time.Since(p.LastSeen) > 2*time.Minute {
		http.Error(w, "offline", http.StatusGone)
		return
	}
	out := *p
	s.mu.RLock()
	_, tunnelled := s.tunnels[id]
	s.mu.RUnlock()
	if tunnelled {
		out.Relay = true
		out.URL = s.relayURL(r, id)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) List(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []PeerInfo{}
	for _, p := range s.peers {
		if alias != "" && p.Alias != alias {
			continue
		}
		out = append(out, *p)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) Heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("peerId")
	s.mu.Lock()
	if p, ok := s.peers[id]; ok {
		p.LastSeen = time.Now()
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Signal(w http.ResponseWriter, r *http.Request) {
	peerID := r.URL.Query().Get("peerId")
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conns[peerID] = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, peerID)
		s.mu.Unlock()
		conn.Close()
	}()
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		// parse target
		var m struct {
			To string `json:"to"`
		}
		_ = json.Unmarshal(msg, &m)
		if m.To != "" {
			s.mu.RLock()
			target, ok := s.conns[m.To]
			s.mu.RUnlock()
			if ok {
				_ = target.WriteMessage(websocket.TextMessage, msg)
			}
		}
	}
}

// pairing state
type pendingPair struct {
	From string
	To   string
	Code string
	Exp  time.Time
}

// add field to Server struct? We'll store in a map via separate; instead add to Server.
// We'll extend Server struct below by editing NewServer? Need field. We'll use a package-level? Better add field.

func (s *Server) PairStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.From == "" || req.To == "" || req.Code == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.pairs == nil {
		s.pairs = map[string]pendingPair{}
	}
	key := req.From + "|" + req.To
	s.pairs[key] = pendingPair{From: req.From, To: req.To, Code: req.Code, Exp: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
}

func (s *Server) PairVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pairs == nil {
		s.pairs = map[string]pendingPair{}
	}
	key := req.From + "|" + req.To
	p, ok := s.pairs[key]
	if !ok || time.Now().After(p.Exp) || p.Code != req.Code {
		http.Error(w, "invalid or expired code", http.StatusUnauthorized)
		return
	}
	delete(s.pairs, key)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "paired", "certFp": "stub"})
}

func (s *Server) SetPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string         `json:"peerId"`
		Policy map[string]any `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.policies[req.PeerID] = req.Policy
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) GetPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("peerId")
	s.mu.RLock()
	p, ok := s.policies[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/tunnel":
		s.handleTunnel(w, r)
	case len(r.URL.Path) > 3 && r.URL.Path[:3] == "/t/":
		s.handleProxy(w, r)
	case r.URL.Path == "/register" && r.Method == http.MethodPost:
		s.Register(w, r)
	case r.URL.Path == "/resolve" || (len(r.URL.Path) > len("/resolve/") && r.URL.Path[:len("/resolve/")] == "/resolve/"):
		s.Resolve(w, r)
	case r.URL.Path == "/peers":
		s.List(w, r)
	case r.URL.Path == "/heartbeat":
		s.Heartbeat(w, r)
	case r.URL.Path == "/signal":
		s.Signal(w, r)
	case r.URL.Path == "/pair/start" && r.Method == http.MethodPost:
		s.PairStart(w, r)
	case r.URL.Path == "/pair/verify" && r.Method == http.MethodPost:
		s.PairVerify(w, r)
	case r.URL.Path == "/policy" && r.Method == http.MethodPost:
		s.SetPolicy(w, r)
	case r.URL.Path == "/policy" && r.Method == http.MethodGet:
		s.GetPolicy(w, r)
	case r.URL.Path == "/health":
		w.Write([]byte("ok"))
	case r.URL.Path == "/client":
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(clientHTML))
	default:
		http.NotFound(w, r)
	}
}

// Run starts the HTTP server.
func Run(addr string) error { return RunOptions(addr, "", "", "") }

// RunOptions starts the server with optional TLS and a public base URL used in
// relay links (for example https://relay.example.com).
func RunOptions(addr, certFile, keyFile, publicURL string) error {
	s := NewServer()
	s.publicURL = publicURL
	fmt.Printf("rendezvous listening on %s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// #nosec G102 -- the rendezvous server is intentionally reachable on its listen address.
	if certFile != "" && keyFile != "" {
		fmt.Printf("relay links: %s\n", publicURL)
		return server.ListenAndServeTLS(certFile, keyFile)
	}
	return server.ListenAndServe()
}

const clientHTML = `<!DOCTYPE html>
<html><body style="margin:0;background:#111;color:#eee;font-family:sans-serif">
<h3 style="margin:8px">RDP Link Web Guest</h3>
<input id="host" placeholder="Host Peer ID" style="margin:8px" />
<button onclick="connect()">Connect</button>
<video id="v" autoplay playsinline style="max-width:100%;max-height:85vh;display:block;margin:auto;background:#000"></video>
<script>
let ws, pc, dc, guestId = 'guest-' + Math.random().toString(36).slice(2);
function connect(){
  const hostId = document.getElementById('host').value.trim();
  const base = location.origin.replace('http','ws');
  ws = new WebSocket(base + '/signal?peerId=' + encodeURIComponent(guestId));
  ws.onmessage = async (ev) => {
    const m = JSON.parse(ev.data);
    if(m.type === 'answer'){
      await pc.setRemoteDescription(new RTCSessionDescription({type:'answer',sdp:m.sdp}));
    } else if(m.type === 'ice'){
      try { await pc.addIceCandidate({candidate:m.candidate}); } catch(e){}
    }
  };
  pc = new RTCPeerConnection({iceServers:[{urls:'stun:stun.l.google.com:19302'}]});
  pc.ontrack = (e) => { document.getElementById('v').srcObject = e.streams[0]; };
  pc.onicecandidate = (e) => {
    if(e.candidate) ws.send(JSON.stringify({type:'ice',from:guestId,to:hostId,candidate:e.candidate.candidate}));
  };
  dc = pc.createDataChannel('input');
  const pwd = prompt('Password (if required):') || '';
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  ws.onopen = () => {
    ws.send(JSON.stringify({type:'offer',from:guestId,to:hostId,sdp:offer.sdp,password:pwd}));
  };
  // send input
  document.addEventListener('mousemove', (e) => {
    if(dc && dc.readyState === 'open') dc.send(JSON.stringify({type:'move',x:e.clientX,y:e.clientY}));
  });
}
</script>
</body></html>`
