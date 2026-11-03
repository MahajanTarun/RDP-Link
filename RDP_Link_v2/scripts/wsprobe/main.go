// Command wsprobe is a tiny WebSocket client used to verify the serial and
// microphone bridges from scripts and docs.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := flag.String("url", "", "websocket URL")
	send := flag.String("send", "ping\n", "text to send")
	timeout := flag.Duration("timeout", 5*time.Second, "read timeout")
	file := flag.String("file", "", "send this file as a binary message instead of -send")
	noWait := flag.Bool("no-wait", false, "exit after sending without waiting for a reply")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "usage: wsprobe -url ws://... [-send text]")
		os.Exit(2)
	}
	conn, _, err := websocket.DefaultDialer.Dial(*url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	var payload []byte
	if *file != "" {
		payload, err = os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			os.Exit(1)
		}
	} else {
		payload = []byte(*send)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	if *noWait {
		time.Sleep(300 * time.Millisecond)
		fmt.Printf("sent %d bytes\n", len(payload))
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(*timeout))
	_, data, err := conn.ReadMessage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	fmt.Printf("reply: %q\n", string(data))
}
