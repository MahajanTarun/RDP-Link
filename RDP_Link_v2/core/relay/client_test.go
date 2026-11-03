package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStreamStopsWhenCancelled makes sure the host side of the tunnel stops
// reading a stream when the guest goes away. Without this the host kept
// capturing (and sending) frames for a viewer that had left.
func TestStreamStopsWhenCancelled(t *testing.T) {
	// A local "host" that streams bytes forever.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/webm")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 10000; i++ {
			if _, err := w.Write([]byte("chunk")); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer local.Close()
	port := 0
	if _, err := strconv.Atoi(strings.TrimPrefix(local.URL, "http://127.0.0.1:")); err == nil {
		port, _ = strconv.Atoi(strings.TrimPrefix(local.URL, "http://127.0.0.1:"))
	}

	var mu sync.Mutex
	var replies []reply
	send := func(rep reply) {
		mu.Lock()
		replies = append(replies, rep)
		mu.Unlock()
	}

	client := &Client{LocalPort: port}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		client.stream(ctx, request{ID: "1", Method: http.MethodGet, Path: "/audio"}, send)
		close(done)
	}()

	// Let the stream start, then cancel it.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after the guest left")
	}

	mu.Lock()
	defer mu.Unlock()
	sawEnd := false
	for _, rep := range replies {
		if rep.Type == "end" {
			sawEnd = true
		}
	}
	if !sawEnd {
		t.Fatal("no end reply was sent")
	}
}

// TestApplyHeadersMarksRelayRequests checks that forwarded requests carry the
// internal secret, and that a guest cannot inject one.
func TestApplyHeadersMarksRelayRequests(t *testing.T) {
	client := &Client{Secret: "s3cret"}
	header := http.Header{}
	client.applyHeaders(header, map[string]string{
		"X-Forwarded-For":    "203.0.113.7",
		"X-Rdp-Relay-Secret": "forged",
	})
	if header.Get("X-Rdp-Relay-Secret") != "s3cret" {
		t.Fatalf("relay secret was not applied: %q", header.Get("X-Rdp-Relay-Secret"))
	}
	if header.Get("X-Forwarded-For") != "203.0.113.7" {
		t.Fatal("forwarded address was dropped")
	}
}
