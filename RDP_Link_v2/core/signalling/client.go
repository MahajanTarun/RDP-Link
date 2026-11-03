// Package signalling provides a WebSocket signalling client to the rendezvous.
package signalling

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Client is a signalling WebSocket client.
type Client struct {
	URL    string
	PeerID string
	Conn   *websocket.Conn
	Send   chan []byte
	Recv   chan []byte
}

// Connect dials the signalling endpoint.
func Connect(baseURL, peerID string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("peerId", peerID)
	u.RawQuery = q.Encode()
	u.Scheme = "ws"
	if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	// allow http base
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(u.String(), http.Header{})
	if err != nil {
		return nil, err
	}
	c := &Client{URL: u.String(), PeerID: peerID, Conn: conn, Send: make(chan []byte, 64), Recv: make(chan []byte, 64)}
	go c.readLoop()
	go c.writeLoop()
	return c, nil
}

func (c *Client) readLoop() {
	defer c.Conn.Close()
	for {
		_, msg, err := c.Conn.ReadMessage()
		if err != nil {
			return
		}
		c.Recv <- msg
	}
}

func (c *Client) writeLoop() {
	for msg := range c.Send {
		if err := c.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// SendJSON marshals and queues a message.
func (c *Client) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case c.Send <- b:
	default:
		return fmt.Errorf("send buffer full")
	}
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	close(c.Send)
	return c.Conn.Close()
}
