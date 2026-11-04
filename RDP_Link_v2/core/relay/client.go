// Package relay keeps an outbound tunnel to the rendezvous server so guests can
// reach this host across NAT and changing public IPs.
package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eminentailabs/office-rdp/core/peerauth"
	"github.com/gorilla/websocket"
)

const maxBody = 32 << 20

// Client maintains the tunnel connection.
type Client struct {
	Rendezvous string
	PeerID     string
	// PrivateKey signs the tunnel handshake. The peer ID is derived from
	// this key, so the rendezvous can verify that the host owns the peer ID
	// it claims.
	PrivateKey ed25519.PrivateKey
	LocalPort  int
	Insecure   bool
	CAFile     string
	// Secret proves to the local host agent that a forwarded request really
	// came from this process. Only requests carrying it may set
	// X-Forwarded-For.
	Secret string
	// Done stops the reconnect loop when the channel is closed.
	Done <-chan struct{}
}

type request struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Stream  bool              `json:"stream,omitempty"`
	Cancel  bool              `json:"cancel,omitempty"`
}

type reply struct {
	Type    string            `json:"type,omitempty"`
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Data    string            `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// Run connects and reconnects until the process exits or Done is closed.
func (c *Client) Run() {
	backoff := 3 * time.Second
	for {
		if c.stopped() {
			return
		}
		if err := c.connectOnce(); err != nil {
			if c.stopped() {
				return
			}
			log.Printf("relay: %v (retrying in %s)", err, backoff)
		}
		if !c.sleep(backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// stopped reports whether the client was asked to stop.
func (c *Client) stopped() bool {
	if c.Done == nil {
		return false
	}
	select {
	case <-c.Done:
		return true
	default:
		return false
	}
}

// sleep waits for the given duration and returns false when the client was
// asked to stop in the meantime.
func (c *Client) sleep(d time.Duration) bool {
	if c.Done == nil {
		time.Sleep(d)
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-c.Done:
		return false
	case <-timer.C:
		return true
	}
}

// wsURL builds the tunnel URL and the signature that covers it.
func (c *Client) wsURL() (string, http.Header, error) {
	u, err := url.Parse(c.Rendezvous)
	if err != nil {
		return "", nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/tunnel"
	q := u.Query()
	q.Set("peerId", c.PeerID)
	u.RawQuery = q.Encode()

	header := http.Header{}
	if len(c.PrivateKey) == ed25519.PrivateKeySize {
		timestamp := peerauth.Timestamp()
		nonce := peerauth.Nonce()
		uri := u.RequestURI()
		peerauth.Apply(header, c.PrivateKey, http.MethodGet, uri, timestamp, nonce, nil)
	}
	return u.String(), header, nil
}

func (c *Client) connectOnce() error {
	target, header, err := c.wsURL()
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 12 * time.Second}
	if strings.HasPrefix(target, "wss") {
		// #nosec G402 -- --relay-insecure is an explicit operator opt-in for
		// self-signed relays during testing.
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.Insecure}
		if c.CAFile != "" {
			pem, err := os.ReadFile(c.CAFile)
			if err != nil {
				return err
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return fmt.Errorf("relay: no certificates in %s", c.CAFile)
			}
			tlsConfig.RootCAs = pool
		}
		dialer.TLSClientConfig = tlsConfig
	}
	conn, resp, err := dialer.Dial(target, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: %w (%s)", target, err, resp.Status)
		}
		return fmt.Errorf("dial %s: %w", target, err)
	}
	log.Printf("relay: tunnel connected to %s", c.Rendezvous)

	_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	})
	var writeMu sync.Mutex
	sem := make(chan struct{}, 6)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeMu.Lock()
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				writeMu.Unlock()
			case <-done:
				return
			}
		}
	}()
	defer close(done)
	stopReader := make(chan struct{})
	if c.Done != nil {
		go func() {
			select {
			case <-c.Done:
				_ = conn.Close()
			case <-stopReader:
			}
		}()
	}
	defer close(stopReader)

	// Streams that are still running, so the guest can cancel them.
	var streamMu sync.Mutex
	streams := map[string]context.CancelFunc{}
	defer func() {
		streamMu.Lock()
		for _, cancel := range streams {
			cancel()
		}
		streamMu.Unlock()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			_ = conn.Close()
			return err
		}
		var req request
		if json.Unmarshal(data, &req) != nil || req.ID == "" {
			continue
		}
		if req.Cancel {
			streamMu.Lock()
			cancel := streams[req.ID]
			delete(streams, req.ID)
			streamMu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		send := func(rep reply) {
			out, _ := json.Marshal(rep)
			writeMu.Lock()
			_ = conn.WriteMessage(websocket.TextMessage, out)
			writeMu.Unlock()
		}
		// Never block the WebSocket reader waiting for a worker slot. If all
		// slots are occupied by long streams, blocking here would prevent the
		// reader from seeing their Cancel frames and could pin device captures.
		select {
		case sem <- struct{}{}:
		default:
			if req.Stream {
				send(reply{Type: "stream", ID: req.ID, Status: http.StatusTooManyRequests,
					Headers: map[string]string{"Content-Type": "text/plain"}})
				send(reply{Type: "end", ID: req.ID})
			} else {
				send(reply{ID: req.ID, Status: http.StatusTooManyRequests,
					Headers: map[string]string{"Content-Type": "text/plain"},
					Body:    base64.StdEncoding.EncodeToString([]byte("relay is busy"))})
			}
			continue
		}
		if req.Stream {
			// The cancel function is registered before the goroutine is
			// started. Otherwise a cancel frame that arrives while the
			// goroutine is still queued would be lost and the host would
			// keep the device open.
			ctx, cancel := context.WithCancel(context.Background())
			streamMu.Lock()
			streams[req.ID] = cancel
			streamMu.Unlock()
			go func(req request) {
				defer func() { <-sem }()
				c.stream(ctx, req, send)
				streamMu.Lock()
				delete(streams, req.ID)
				streamMu.Unlock()
				cancel()
			}(req)
			continue
		}
		go func(req request) {
			defer func() { <-sem }()
			send(c.execute(req))
		}(req)
	}
}

// applyHeaders copies the tunnelled request headers and marks the request as
// coming from this process.
func (c *Client) applyHeaders(dst http.Header, src map[string]string) {
	for k, v := range src {
		if strings.EqualFold(k, "x-rdp-relay-secret") {
			// Never trust a forwarded secret.
			continue
		}
		dst.Set(k, v)
	}
	if c.Secret != "" {
		dst.Set("X-Rdp-Relay-Secret", c.Secret)
	}
}

// stream forwards an incremental response (audio/MJPEG) as frames.
func (c *Client) stream(ctx context.Context, req request, send func(reply)) {
	target := fmt.Sprintf("http://127.0.0.1:%d%s", c.LocalPort, req.Path)
	if req.Query != "" {
		target += "?" + req.Query
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, nil)
	if err != nil {
		send(reply{Type: "end", ID: req.ID, Error: err.Error()})
		return
	}
	c.applyHeaders(httpReq.Header, req.Headers)
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			// The guest left; the host already stopped the device.
			send(reply{Type: "end", ID: req.ID})
			return
		}
		send(reply{Type: "end", ID: req.ID, Error: err.Error()})
		return
	}
	defer resp.Body.Close()
	headers := map[string]string{}
	for _, k := range []string{"Content-Type", "ETag", "Cache-Control",
		"Content-Disposition", "Set-Cookie"} {
		if v := resp.Header.Get(k); v != "" {
			headers[k] = v
		}
	}
	send(reply{Type: "stream", ID: req.ID, Status: resp.StatusCode, Headers: headers})
	buf := make([]byte, 16<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			send(reply{Type: "chunk", ID: req.ID,
				Data: base64.StdEncoding.EncodeToString(buf[:n])})
		}
		if err != nil {
			break
		}
	}
	send(reply{Type: "end", ID: req.ID})
}

func (c *Client) execute(req request) reply {
	rep := reply{ID: req.ID}
	target := fmt.Sprintf("http://127.0.0.1:%d%s", c.LocalPort, req.Path)
	if req.Query != "" {
		target += "?" + req.Query
	}
	body, err := base64.StdEncoding.DecodeString(req.Body)
	if err != nil {
		rep.Error = "bad body"
		return rep
	}
	httpReq, err := http.NewRequest(req.Method, target, bytes.NewReader(body))
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	c.applyHeaders(httpReq.Header, req.Headers)
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	if len(data) > maxBody {
		rep.Status = http.StatusRequestEntityTooLarge
		rep.Headers = map[string]string{"Content-Type": "text/plain"}
		rep.Body = base64.StdEncoding.EncodeToString([]byte("response exceeds 32 MiB relay limit"))
		return rep
	}
	headers := map[string]string{}
	for _, k := range []string{"Content-Type", "ETag", "Cache-Control",
		"Content-Disposition", "Set-Cookie"} {
		if v := resp.Header.Get(k); v != "" {
			headers[k] = v
		}
	}
	rep.Status = resp.StatusCode
	rep.Headers = headers
	rep.Body = base64.StdEncoding.EncodeToString(data)
	return rep
}
