package peerauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignAndVerify(t *testing.T) {
	pub, priv := newKey(t)
	timestamp := Timestamp()
	sig := Sign(priv, http.MethodGet, "/peers", timestamp, nil)
	if sig == "" {
		t.Fatal("no signature")
	}
	if !Verify(pub, http.MethodGet, "/peers", timestamp, nil, sig) {
		t.Fatal("valid signature rejected")
	}
}

func TestVerifyRejectsTamperedRequests(t *testing.T) {
	pub, priv := newKey(t)
	timestamp := Timestamp()
	sig := Sign(priv, http.MethodPost, "/register", timestamp, []byte(`{"a":1}`))
	cases := []struct {
		name   string
		method string
		uri    string
		body   []byte
	}{
		{"other path", http.MethodPost, "/policy", []byte(`{"a":1}`)},
		{"other method", http.MethodGet, "/register", []byte(`{"a":1}`)},
		{"other body", http.MethodPost, "/register", []byte(`{"a":2}`)},
		{"other timestamp", http.MethodPost, "/register", []byte(`{"a":1}`)},
	}
	for _, tc := range cases {
		ts := timestamp
		if tc.name == "other timestamp" {
			ts = "1"
		}
		if Verify(pub, tc.method, tc.uri, ts, tc.body, sig) {
			t.Fatalf("%s: tampered request accepted", tc.name)
		}
	}
}

func TestPeerIDIsBoundToTheKey(t *testing.T) {
	pub, _ := newKey(t)
	id := PeerID(pub)
	if len(id) != 9 {
		t.Fatalf("peer id %q", id)
	}
	other, _ := newKey(t)
	if PeerID(other) == id {
		t.Fatal("two keys produced the same peer id")
	}
}

func TestCheckTimestampWindow(t *testing.T) {
	if !CheckTimestamp(Timestamp()) {
		t.Fatal("current timestamp rejected")
	}
	old := time.Now().Add(-10 * time.Minute).Unix()
	if CheckTimestamp(itoa(old)) {
		t.Fatal("old timestamp accepted")
	}
	future := time.Now().Add(10 * time.Minute).Unix()
	if CheckTimestamp(itoa(future)) {
		t.Fatal("future timestamp accepted")
	}
	if CheckTimestamp("not a number") {
		t.Fatal("garbage accepted")
	}
}

func TestSendAddsSignatureHeaders(t *testing.T) {
	pub, priv := newKey(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(HeaderPeer) != PeerID(pub) {
			http.Error(w, "peer", http.StatusUnauthorized)
			return
		}
		if !CheckTimestamp(r.Header.Get(HeaderTimestamp)) {
			http.Error(w, "timestamp", http.StatusUnauthorized)
			return
		}
		body := []byte(`{"peerId":"x"}`)
		if !Verify(pub, r.Method, r.URL.RequestURI(),
			r.Header.Get(HeaderTimestamp), body, r.Header.Get(HeaderSignature)) {
			http.Error(w, "signature", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	resp, err := Send(ts.Client(), http.MethodPost, ts.URL+"/register",
		[]byte(`{"peerId":"x"}`), priv, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestApplySetsAllHeaders(t *testing.T) {
	pub, priv := newKey(t)
	header := http.Header{}
	Apply(header, priv, http.MethodGet, "/peers", Timestamp(), nil)
	for _, name := range []string{HeaderPeer, HeaderTimestamp, HeaderPublicKey, HeaderSignature} {
		if header.Get(name) == "" {
			t.Fatalf("header %s missing", name)
		}
	}
	if header.Get(HeaderPeer) != PeerID(pub) {
		t.Fatal("wrong peer id")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
