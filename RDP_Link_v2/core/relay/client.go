// Package relay keeps an outbound tunnel to the rendezvous server so guests can
// reach this host across NAT and changing public IPs.
package relay

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/gorilla/websocket"
)

const maxBody = 32 << 20

// Token derives the tunnel credential from the peer ID and access password.
// The rendezvous stores the first token it sees for a peer ID, so a guest
// cannot take over someone else's tunnel without knowing the password.
func Token(peerID, password string) string {
	sum := sha256.Sum256([]byte("rdp-link-tunnel:" + peerID + ":" + password))
	return hex.EncodeToString(sum[:])
}

// Client maintains the tunnel connection.
type Client struct {
	Rendezvous string
	PeerID     string
	Password   string
	LocalPort  int
	Insecure   bool
	CAFile     string
}

type request struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Stream  bool              `json:"stream,omitempty"`
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

// Run connects and reconnects until the process exits.
func (c *Client) Run() {
	backoff := 3 * time.Second
	for {
		if err := c.connectOnce(); err != nil {
			log.Printf("relay: %v (retrying in %s)", err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) wsURL() (string, error) {
	u, err := url.Parse(c.Rendezvous)
	if err != nil {
		return "", err
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
	q.Set("token", Token(c.PeerID, c.Password))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) connectOnce() error {
	target, err := c.wsURL()
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
	conn, resp, err := dialer.Dial(target, http.Header{})
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
		sem <- struct{}{}
		go func(req request) {
			defer func() { <-sem }()
			send := func(rep reply) {
				out, _ := json.Marshal(rep)
				writeMu.Lock()
				_ = conn.WriteMessage(websocket.TextMessage, out)
				writeMu.Unlock()
			}
			if req.Stream {
				c.stream(req, send)
				return
			}
			send(c.execute(req))
		}(req)
	}
}

// stream forwards an incremental response (audio/MJPEG) as frames.
func (c *Client) stream(req request, send func(reply)) {
	target := fmt.Sprintf("http://127.0.0.1:%d%s", c.LocalPort, req.Path)
	if req.Query != "" {
		target += "?" + req.Query
	}
	httpReq, err := http.NewRequest(req.Method, target, nil)
	if err != nil {
		send(reply{Type: "end", ID: req.ID, Error: err.Error()})
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		send(reply{Type: "end", ID: req.ID, Error: err.Error()})
		return
	}
	defer resp.Body.Close()
	headers := map[string]string{}
	for _, k := range []string{"Content-Type", "ETag", "Cache-Control", "Content-Disposition"} {
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
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	headers := map[string]string{}
	for _, k := range []string{"Content-Type", "ETag", "Cache-Control", "Content-Disposition"} {
		if v := resp.Header.Get(k); v != "" {
			headers[k] = v
		}
	}
	rep.Status = resp.StatusCode
	rep.Headers = headers
	rep.Body = base64.StdEncoding.EncodeToString(data)
	return rep
}
