package rendezvous

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eminentailabs/office-rdp/core/peerauth"
)

type testPeer struct {
	priv ed25519.PrivateKey
	id   string
}

func newTestPeer(t *testing.T) testPeer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testPeer{priv: priv, id: peerauth.PeerID(pub)}
}

// signed builds a request that carries a valid signature.
func (p testPeer) signed(t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := peerauth.RequestURI(url)
	if err != nil {
		t.Fatal(err)
	}
	peerauth.Apply(req.Header, p.priv, method, uri, peerauth.Timestamp(), peerauth.Nonce(), body)
	return req
}

func TestRegisterRequiresSignature(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/register", "application/json",
		strings.NewReader(`{"peerId":"123","alias":"host"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned register must fail, got %d", resp.StatusCode)
	}
}

func TestRegisterResolve(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	body := []byte(`{"peerId":"` + peer.id + `","alias":"host","status":"online"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("register status %d", resp.StatusCode)
	}
	r, err := http.Get(ts.URL + "/resolve?peerId=" + peer.id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("resolve status %d", r.StatusCode)
	}
	var info PeerInfo
	json.NewDecoder(r.Body).Decode(&info)
	if info.PeerID != peer.id {
		t.Fatalf("peerid %s", info.PeerID)
	}
}

func TestListRequiresSignature(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	body := []byte(`{"peerId":"` + peer.id + `","alias":"a"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Anonymous listing is refused.
	r, err := http.Get(ts.URL + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous list must fail, got %d", r.StatusCode)
	}

	// A signed listing works.
	r2, err := http.DefaultClient.Do(peer.signed(t, http.MethodGet, ts.URL+"/peers", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var out []PeerInfo
	json.NewDecoder(r2.Body).Decode(&out)
	if len(out) != 1 {
		t.Fatalf("list len %d", len(out))
	}
}

func TestPeerCannotClaimAnotherPeerID(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	other := newTestPeer(t)
	// The signature is valid, but the body claims a different peer ID.
	body := []byte(`{"peerId":"` + other.id + `","alias":"host"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("register status %d", resp.StatusCode)
	}
	s.mu.RLock()
	registered, ok := s.peers[other.id]
	s.mu.RUnlock()
	if ok {
		t.Fatalf("peer %s registered as %s", peer.id, registered.PeerID)
	}
	s.mu.RLock()
	_, ok = s.peers[peer.id]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("peer was not registered under its own ID")
	}
}

func TestPolicyIsPerPeer(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	other := newTestPeer(t)
	body := []byte(`{"peerId":"` + other.id + `","policy":{"readOnly":true}}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/policy", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-peer policy must fail, got %d", resp.StatusCode)
	}

	body2 := []byte(`{"peerId":"` + peer.id + `","policy":{"readOnly":true}}`)
	resp2, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/policy", body2))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("policy status %d", resp2.StatusCode)
	}

	// Reading another peer's policy is refused as well.
	r, err := http.DefaultClient.Do(peer.signed(t, http.MethodGet,
		ts.URL+"/policy?peerId="+other.id, nil))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-peer policy read must fail, got %d", r.StatusCode)
	}
}

func TestListHidesAddressesWithoutAdminToken(t *testing.T) {
	s := NewServer()
	s.SetAdminToken("s3cret")
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	body := []byte(`{"peerId":"` + peer.id + `","alias":"a","addr":"192.168.1.5:8088"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Without the admin token the LAN address stays hidden.
	r, err := http.DefaultClient.Do(peer.signed(t, http.MethodGet, ts.URL+"/peers", nil))
	if err != nil {
		t.Fatal(err)
	}
	var out []PeerInfo
	json.NewDecoder(r.Body).Decode(&out)
	r.Body.Close()
	if len(out) != 1 {
		t.Fatalf("list len %d", len(out))
	}
	if out[0].Addr != "" {
		t.Fatalf("address leaked without the admin token: %q", out[0].Addr)
	}

	// With the admin token it is returned.
	req := peer.signed(t, http.MethodGet, ts.URL+"/peers", nil)
	req.Header.Set("X-Rdp-Admin-Token", "s3cret")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out2 []PeerInfo
	json.NewDecoder(r2.Body).Decode(&out2)
	r2.Body.Close()
	if len(out2) != 1 || out2[0].Addr != "192.168.1.5:8088" {
		t.Fatalf("admin listing failed: %+v", out2)
	}
}

func TestHeartbeatNeedsSignature(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	body := []byte(`{"peerId":"` + peer.id + `","alias":"a"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	hr, err := http.Post(ts.URL+"/heartbeat?peerId="+peer.id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	hr.Body.Close()
	if hr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned heartbeat must fail, got %d", hr.StatusCode)
	}

	hr2, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost,
		ts.URL+"/heartbeat?peerId="+peer.id, nil))
	if err != nil {
		t.Fatal(err)
	}
	hr2.Body.Close()
	if hr2.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat %d", hr2.StatusCode)
	}
}

func TestTunnelRequiresSignature(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	resp, err := http.Get(ts.URL + "/tunnel?peerId=" + peer.id)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned tunnel must fail, got %d", resp.StatusCode)
	}
}

func TestScopeSetCookie(t *testing.T) {
	raw := "rdplink_sid=abc123; Path=/; Max-Age=43200; HttpOnly; SameSite=Strict"
	got := scopeSetCookie(raw, "/t/123456789")
	if !strings.Contains(got, "Path=/t/123456789/") {
		t.Fatalf("cookie path not scoped: %q", got)
	}
	if !strings.Contains(got, "HttpOnly") || !strings.Contains(got, "SameSite=Strict") {
		t.Fatalf("cookie flags were lost: %q", got)
	}
	// A cookie that is already scoped is left alone.
	scoped := scopeSetCookie(raw, "/t/123456789")
	if again := scopeSetCookie(scoped, "/t/999999999"); again != scoped {
		t.Fatalf("scoped cookie was rewritten: %q -> %q", scoped, again)
	}
}

func TestHealth(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	r, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("health")
	}
}

func TestPairing(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	from := newTestPeer(t)
	to := newTestPeer(t)
	// The target must be registered, and its key is pinned by that
	// registration.
	reg, err := http.DefaultClient.Do(to.signed(t, http.MethodPost, ts.URL+"/register",
		[]byte(`{"alias":"b"}`)))
	if err != nil {
		t.Fatal(err)
	}
	reg.Body.Close()
	if reg.StatusCode != http.StatusOK {
		t.Fatalf("register target: %d", reg.StatusCode)
	}
	body := []byte(fmt.Sprintf(`{"from":%q,"to":%q,"code":"123456"}`, from.id, to.id))

	// Unsigned pairing is refused.
	anon, err := http.Post(ts.URL+"/pair/start", "application/json",
		strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned pairing must fail, got %d", anon.StatusCode)
	}

	resp, err := http.DefaultClient.Do(from.signed(t, http.MethodPost, ts.URL+"/pair/start", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	vresp, err := http.DefaultClient.Do(from.signed(t, http.MethodPost, ts.URL+"/pair/verify", body))
	if err != nil {
		t.Fatal(err)
	}
	defer vresp.Body.Close()
	if vresp.StatusCode != 200 {
		t.Fatalf("verify %d", vresp.StatusCode)
	}
	// second verify should fail
	vresp2, err := http.DefaultClient.Do(from.signed(t, http.MethodPost, ts.URL+"/pair/verify", body))
	if err != nil {
		t.Fatal(err)
	}
	defer vresp2.Body.Close()
	if vresp2.StatusCode == 200 {
		t.Fatal("expected failure on reuse")
	}
}

func TestResolveIsRateLimited(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	peer := newTestPeer(t)
	body := []byte(`{"peerId":"` + peer.id + `","alias":"host"}`)
	resp, err := http.DefaultClient.Do(peer.signed(t, http.MethodPost, ts.URL+"/register", body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Two resolves in quick succession: the second one is throttled.
	first, err := http.Get(ts.URL + "/resolve?peerId=" + peer.id)
	if err != nil {
		t.Fatal(err)
	}
	first.Body.Close()
	second, err := http.Get(ts.URL + "/resolve?peerId=" + peer.id)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("resolve must be rate limited, got %d", second.StatusCode)
	}
	// The window expires.
	time.Sleep(2100 * time.Millisecond)
	third, err := http.Get(ts.URL + "/resolve?peerId=" + peer.id)
	if err != nil {
		t.Fatal(err)
	}
	third.Body.Close()
	if third.StatusCode != 200 {
		t.Fatalf("resolve after the window failed: %d", third.StatusCode)
	}
}
