package rendezvous

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterResolve(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	body := `{"peerId":"123","alias":"host","status":"online"}`
	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// resolve
	r, err := http.Get(ts.URL + "/resolve?peerId=123")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("resolve status %d", r.StatusCode)
	}
	var info PeerInfo
	json.NewDecoder(r.Body).Decode(&info)
	if info.PeerID != "123" {
		t.Fatal("peerid")
	}
}

func TestListAndHeartbeat(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	http.Post(ts.URL+"/register", "application/json", strings.NewReader(`{"peerId":"1","alias":"a"}`))
	http.Post(ts.URL+"/register", "application/json", strings.NewReader(`{"peerId":"2","alias":"b"}`))
	r, err := http.Get(ts.URL + "/peers")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out []PeerInfo
	json.NewDecoder(r.Body).Decode(&out)
	if len(out) != 2 {
		t.Fatalf("list len %d", len(out))
	}
	// heartbeat
	hr, err := http.Post(ts.URL+"/heartbeat?peerId=1", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hr.StatusCode != 204 {
		t.Fatalf("heartbeat %d", hr.StatusCode)
	}
	hr.Body.Close()
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
	body := `{"from":"a","to":"b","code":"123456"}`
	resp, _ := http.Post(ts.URL+"/pair/start", "application/json", strings.NewReader(body))
	resp.Body.Close()
	vbody := `{"from":"a","to":"b","code":"123456"}`
	vresp, err := http.Post(ts.URL+"/pair/verify", "application/json", strings.NewReader(vbody))
	if err != nil { t.Fatal(err) }
	defer vresp.Body.Close()
	if vresp.StatusCode != 200 {
		t.Fatalf("verify status %d", vresp.StatusCode)
	}
	// second verify should fail
	vresp2, err := http.Post(ts.URL+"/pair/verify", "application/json", strings.NewReader(vbody))
	if err != nil { t.Fatal(err) }
	defer vresp2.Body.Close()
	if vresp2.StatusCode == 200 {
		t.Fatal("expected failure on reuse")
	}
}
