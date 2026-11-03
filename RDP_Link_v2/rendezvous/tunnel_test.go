package rendezvous

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestReadLoopNeverBlocksOnAbandonedStream is a regression test: when a guest
// disconnects mid-stream, the host keeps sending frames. The reader must not
// block on the abandoned channel, or the whole tunnel (and every other guest)
// would stall.
func TestReadLoopNeverBlocksOnAbandonedStream(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConns <- conn
		<-r.Context().Done()
	}))
	defer srv.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverConn := <-serverConns
	defer serverConn.Close()

	tun := &tunnel{peerID: "123456789", ws: client,
		pending: map[string]chan tunnelReply{}, closed: make(chan struct{})}
	s := NewServer()
	go tun.readLoop(s)

	abandoned := make(chan tunnelReply, 4)
	tun.pending["abandoned"] = abandoned
	live := make(chan tunnelReply, 4)
	tun.pending["live"] = live

	for i := 0; i < 20; i++ {
		if err := serverConn.WriteJSON(map[string]any{
			"id": "abandoned", "type": "chunk", "data": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := serverConn.WriteJSON(map[string]any{
		"id": "live", "status": 200, "body": "ok"}); err != nil {
		t.Fatal(err)
	}

	select {
	case reply := <-live:
		if reply.Status != 200 {
			t.Fatalf("unexpected reply %+v", reply)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readLoop blocked on an abandoned stream")
	}
}

// TestProxyReleasesPendingEntry makes sure a finished request does not leave
// its reply channel behind.
func TestProxyReleasesPendingEntry(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	hostConns := make(chan *websocket.Conn, 1)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hostConns <- conn
		// Act as the host: answer every request with a small JSON body.
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID string `json:"id"`
			}
			json.Unmarshal(data, &req)
			out, _ := json.Marshal(map[string]any{
				"id": req.ID, "status": 200,
				"headers": map[string]string{"Content-Type": "application/json"},
				"body":    "e30=",
			})
			_ = conn.WriteMessage(websocket.TextMessage, out)
		}
	}))
	defer host.Close()

	relay := NewServer()
	ts := httptest.NewServer(relay)
	defer ts.Close()

	// Register a tunnel that points at the fake host.
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	hostSide := <-hostConns
	defer hostSide.Close()
	tun := &tunnel{peerID: "123456789", ws: client,
		pending: map[string]chan tunnelReply{}, closed: make(chan struct{})}
	relay.mu.Lock()
	relay.tunnels["123456789"] = tun
	relay.mu.Unlock()
	go tun.readLoop(relay)

	resp, err := http.Get(ts.URL + "/t/123456789/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("proxy status %d", resp.StatusCode)
	}
	relay.mu.Lock()
	pending := len(tun.pending)
	relay.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d pending entries left behind", pending)
	}
}
