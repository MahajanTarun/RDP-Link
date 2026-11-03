package host

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestInfoEndpoint(t *testing.T) {
	a := New("123", "host", "secret", 18099)
	go func() { _ = a.Serve() }()
	time.Sleep(200 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:18099/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info map[string]any
	json.NewDecoder(resp.Body).Decode(&info)
	if info["peerId"] != "123" {
		t.Fatalf("peerId %v", info["peerId"])
	}
}
