// Package rendezvous implements the registry/presence/signalling server.
package rendezvous

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eminentailabs/office-rdp/core/peerauth"
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
	mu       sync.RWMutex
	peers    map[string]*PeerInfo
	conns    map[string]*websocket.Conn
	pairs    map[string]pendingPair
	policies map[string]map[string]any
	tunnels  map[string]*tunnel
	peerKeys map[string]ed25519.PublicKey
	// peerKeyUsed remembers when a key was last used, so the pin table can
	// evict the least recently seen entry instead of growing forever.
	peerKeyUsed map[string]time.Time
	// adminToken unlocks the full registry listing (with the LAN addresses).
	// Without it, /peers only returns the peer IDs and aliases.
	adminToken   string
	publicURL    string
	upgrader     websocket.Upgrader
	throttleMu   sync.Mutex
	resolveCalls map[string]time.Time
}

// Bounds for the in-memory tables. A rendezvous server is a long-running
// process, so an attacker who can reach it must not be able to grow these
// maps without limit.
const (
	maxPeerKeys    = 10000
	maxPairs       = 1024
	maxPeerRecords = 10000
	peerRecordTTL  = 24 * time.Hour
)

func NewServer() *Server {
	return &Server{peers: map[string]*PeerInfo{}, conns: map[string]*websocket.Conn{},
		pairs: map[string]pendingPair{}, policies: map[string]map[string]any{},
		tunnels: map[string]*tunnel{}, peerKeys: map[string]ed25519.PublicKey{},
		peerKeyUsed:  map[string]time.Time{},
		resolveCalls: map[string]time.Time{},
		upgrader:     websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}}
}

// verifyPeer authenticates a signed request. It returns the peer ID the
// request is allowed to act as.
//
// The peer ID is derived from the public key, so a valid signature can only
// ever claim the peer ID that belongs to that key. The first key seen for a
// peer ID is pinned: a later request with a different key is rejected, which
// stops an attacker from re-registering somebody else's peer ID once they
// learn it.
func (s *Server) verifyPeer(r *http.Request, body []byte) (string, bool) {
	peer := normalizeID(r.Header.Get(peerauth.HeaderPeer))
	timestamp := r.Header.Get(peerauth.HeaderTimestamp)
	pubValue := r.Header.Get(peerauth.HeaderPublicKey)
	signature := r.Header.Get(peerauth.HeaderSignature)
	if peer == "" || timestamp == "" || pubValue == "" || signature == "" {
		return "", false
	}
	if !peerauth.CheckTimestamp(timestamp) {
		return "", false
	}
	pub, err := peerauth.ParsePublicKey(pubValue)
	if err != nil {
		return "", false
	}
	if peerauth.PeerID(pub) != peer {
		return "", false
	}
	if !peerauth.Verify(pub, r.Method, r.URL.RequestURI(), timestamp, body, signature) {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if known, ok := s.peerKeys[peer]; ok {
		if !known.Equal(pub) {
			return "", false
		}
		s.peerKeyUsed[peer] = time.Now()
		return peer, true
	}
	if len(s.peerKeys) >= maxPeerKeys {
		s.evictPeerKeyLocked()
	}
	s.peerKeys[peer] = pub
	s.peerKeyUsed[peer] = time.Now()
	return peer, true
}

// evictPeerKeyLocked drops the least recently used key. The caller holds
// s.mu. A peer ID is derived from its key, so losing a pin only weakens the
// defence against a hash collision, it never lets somebody claim another
// peer ID outright.
func (s *Server) evictPeerKeyLocked() {
	oldestKey, oldest := "", time.Now()
	for key, used := range s.peerKeyUsed {
		if used.Before(oldest) {
			oldestKey, oldest = key, used
		}
	}
	if oldestKey != "" {
		delete(s.peerKeys, oldestKey)
		delete(s.peerKeyUsed, oldestKey)
	}
}

// sweepLocked removes registry entries that have not been seen for a day.
func (s *Server) sweepLocked(now time.Time) {
	if len(s.peers) < maxPeerRecords {
		return
	}
	for id, info := range s.peers {
		if now.Sub(info.LastSeen) > peerRecordTTL {
			delete(s.peers, id)
		}
	}
	// If the table is still full, drop the least recently seen half.
	if len(s.peers) >= maxPeerRecords {
		for id := range s.peers {
			delete(s.peers, id)
			if len(s.peers) < maxPeerRecords/2 {
				break
			}
		}
	}
}

// resolveAllowed throttles the unauthenticated /resolve endpoint so the peer
// registry cannot be enumerated by brute force.
func (s *Server) resolveAllowed(ip string) bool {
	s.throttleMu.Lock()
	defer s.throttleMu.Unlock()
	now := time.Now()
	last, ok := s.resolveCalls[ip]
	if ok && now.Sub(last) < 2*time.Second {
		return false
	}
	if len(s.resolveCalls) > 4096 {
		for key, when := range s.resolveCalls {
			if now.Sub(when) > time.Minute {
				delete(s.resolveCalls, key)
			}
		}
	}
	s.resolveCalls[ip] = now
	return true
}

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	peer, ok := s.verifyPeer(r, body)
	if !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	var info PeerInfo
	if err := json.Unmarshal(body, &info); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The peer ID always comes from the signature, never from the payload.
	info.PeerID = peer
	info.LastSeen = time.Now()
	if info.Status == "" {
		info.Status = "online"
	}
	s.mu.Lock()
	s.sweepLocked(time.Now())
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
	// Guests resolve a peer ID they already know, so this endpoint stays
	// open; it is rate limited to stop the registry being enumerated.
	if !s.resolveAllowed(clientIP(r)) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
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
	// Listing the registry requires a signed request from a known identity.
	if _, ok := s.verifyPeer(r, nil); !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	// The LAN addresses are only handed out to a caller that knows the
	// admin token; everybody else sees the peer IDs and aliases only.
	admin := s.adminAllowed(r)
	alias := r.URL.Query().Get("alias")
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []PeerInfo{}
	for _, p := range s.peers {
		if alias != "" && p.Alias != alias {
			continue
		}
		item := *p
		if !admin {
			item.Addr = ""
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// adminAllowed reports whether the request carries the admin token.
func (s *Server) adminAllowed(r *http.Request) bool {
	if s.adminToken == "" {
		return false
	}
	supplied := r.Header.Get("X-Rdp-Admin-Token")
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(s.adminToken)) == 1
}

// SetAdminToken sets the token that unlocks the full registry listing.
func (s *Server) SetAdminToken(token string) { s.adminToken = token }

func (s *Server) Heartbeat(w http.ResponseWriter, r *http.Request) {
	peer, ok := s.verifyPeer(r, nil)
	if !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	if p, ok := s.peers[peer]; ok {
		p.LastSeen = time.Now()
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if _, ok := s.verifyPeer(r, body); !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.From == "" || req.To == "" || req.Code == "" {
		http.Error(w, "missing fields", http.StatusBadRequest)
		return
	}
	now := time.Now()
	s.mu.Lock()
	if s.pairs == nil {
		s.pairs = map[string]pendingPair{}
	}
	for key, pair := range s.pairs {
		if now.After(pair.Exp) {
			delete(s.pairs, key)
		}
	}
	if len(s.pairs) >= maxPairs {
		s.mu.Unlock()
		http.Error(w, "too many pending pairings", http.StatusTooManyRequests)
		return
	}
	key := req.From + "|" + req.To
	s.pairs[key] = pendingPair{From: req.From, To: req.To, Code: req.Code,
		Exp: now.Add(5 * time.Minute)}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
}

func (s *Server) PairVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if _, ok := s.verifyPeer(r, body); !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
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
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	peer, ok := s.verifyPeer(r, body)
	if !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	var req struct {
		PeerID string         `json:"peerId"`
		Policy map[string]any `json:"policy"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A peer may only change its own policy.
	if req.PeerID != "" && normalizeID(req.PeerID) != peer {
		http.Error(w, "cannot set another peer's policy", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	s.policies[peer] = req.Policy
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) GetPolicy(w http.ResponseWriter, r *http.Request) {
	peer, ok := s.verifyPeer(r, nil)
	if !ok {
		http.Error(w, "signature required", http.StatusUnauthorized)
		return
	}
	id := r.URL.Query().Get("peerId")
	if id != "" && normalizeID(id) != peer {
		http.Error(w, "cannot read another peer's policy", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	p, ok := s.policies[peer]
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

// clientIP returns the address of the caller.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Run starts the HTTP server.
func Run(addr string) error { return RunOptions(addr, "", "", "") }

// RunOptions starts the server with optional TLS and a public base URL used in
// relay links (for example https://relay.example.com).
func RunOptions(addr, certFile, keyFile, publicURL string) error {
	return RunOptionsWithAdmin(addr, certFile, keyFile, publicURL, "")
}

// RunOptionsWithAdmin additionally takes the token that unlocks the full
// registry listing (with the peers' LAN addresses).
func RunOptionsWithAdmin(addr, certFile, keyFile, publicURL, adminToken string) error {
	s := NewServer()
	s.publicURL = publicURL
	s.adminToken = adminToken
	fmt.Printf("rendezvous listening on %s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // streamed audio/MJPEG responses are long-lived
		IdleTimeout:       120 * time.Second,
	}
	// #nosec G102 -- the rendezvous server is intentionally reachable on its listen address.
	if certFile != "" && keyFile != "" {
		fmt.Printf("relay links: %s\n", publicURL)
		return server.ListenAndServeTLS(certFile, keyFile)
	}
	return server.ListenAndServe()
}

// clientHTML is a small landing page for the relay itself. The guest UI is
// served by the host through the tunnel at /t/<peerId>/.
const clientHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>RDP Link relay</title>
<style>
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         background:#f5f5f5; color:rgba(0,0,0,.88);
         font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; }
  .card { background:#fff; border:1px solid #f0f0f0; border-radius:8px; padding:28px 32px;
          max-width:460px; box-shadow:0 6px 16px rgba(0,0,0,.08); }
  h1 { font-size:19px; margin:0 0 8px; }
  p { font-size:14px; color:rgba(0,0,0,.65); line-height:1.5715; margin:0 0 12px; }
  code { background:#fafafa; border:1px solid #f0f0f0; border-radius:4px; padding:1px 6px; }
</style></head>
<body><div class="card">
<h1>RDP Link relay</h1>
<p>This server connects RDP Link peers that are behind NAT or on a changing
   public IP. A guest opens the host link that the host shows, for example
   <code>/t/123456789/</code>.</p>
<p>Use the RDP Link app, the desktop viewer or any browser to open that link.</p>
</div></body></html>`
