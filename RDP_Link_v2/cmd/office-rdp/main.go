package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/eminentailabs/office-rdp/core/host"
	"github.com/eminentailabs/office-rdp/core/identity"
	"github.com/eminentailabs/office-rdp/core/mdns"
	"github.com/eminentailabs/office-rdp/core/pairing"
	"github.com/eminentailabs/office-rdp/core/peers"
	"github.com/eminentailabs/office-rdp/core/relay"
	"github.com/kbinani/screenshot"
	"github.com/pion/mediadevices"
)

// relayHTTPClient returns a client that trusts the relay's certificate (or
// skips verification for local testing).
func relayHTTPClient(insecure bool, caFile string) *http.Client {
	// #nosec G402 -- explicit testing opt-in (--relay-insecure).
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure}
	if caFile != "" {
		if pem, err := os.ReadFile(caFile); err == nil { // #nosec G304 -- operator-supplied CA path
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				tlsConfig.RootCAs = pool
			}
		}
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}
}

func main() {
	version := flag.Bool("version", false, "print version")
	idFlag := flag.Bool("id", false, "print peer id")
	connect := flag.String("connect", "", "connect to peer id or alias (stub)")
	wake := flag.String("wake", "", "send Wake-on-LAN magic packet to MAC address")
	pairCode := flag.Bool("pair-code", false, "generate a one-time pairing code")
	agent := flag.Bool("agent", false, "run host agent (stub)")
	hostMode := flag.Bool("host", false, "run host agent (screen sharing)")
	doctor := flag.Bool("doctor", false, "run diagnostics")
	install := flag.Bool("install-service", false, "install host agent as user service")
	displays := flag.Bool("displays", false, "list available displays as index")
	port := flag.Int("port", 8088, "host agent HTTP port")
	display := flag.Int("display", 0, "display index to capture")
	record := flag.String("record", "", "record session to file (H.264)")
	password := flag.String("password", "", "unattended password")
	readOnly := flag.Bool("read-only", false, "disable remote input (view-only)")
	rendezvous := flag.String("rendezvous", "", "rendezvous base URL (e.g. http://host:8080)")
	list := flag.Bool("list", false, "list peers")
	alias := flag.String("alias", "", "override alias")
	ui := flag.Bool("ui", false, "open the control panel in a browser")
	relayInsecure := flag.Bool("relay-insecure", false, "skip TLS verification for the relay (testing only)")
	relayCA := flag.String("relay-ca", "", "custom CA bundle for the relay's TLS certificate")
	testPattern := flag.Bool("test-pattern", false, "stream a synthetic test pattern instead of the screen")
	flag.Parse()

	if *version {
		fmt.Println("office-rdp 0.2.0")
		return
	}

	id, err := identity.LoadOrCreate("", *alias)
	if err != nil {
		fmt.Fprintln(os.Stderr, "identity:", err)
		os.Exit(1)
	}
	if *idFlag {
		fmt.Println(id.Formatted())
		return
	}

	store := peers.New("")
	if *list {
		for _, p := range store.Peers {
			fmt.Printf("%s  %s  (%s)\n", p.PeerID, p.Alias, p.Pairing.State)
		}
		return
	}

	if *agent || *hostMode || *ui {
		fmt.Println("host agent starting — peer id:", id.Formatted())
		pw := *password
		if pw == "" {
			pw = randomPassword()
			fmt.Println("No password supplied — generated one for this session.")
		}
		ag := host.New(id.Formatted(), id.Alias, pw, *port)
		ag.ReadOnly = *readOnly
		ag.TestPattern = *testPattern
		if err := ag.StartCapture(*display); err != nil {
			fmt.Fprintln(os.Stderr, "capture:", err)
		}
		ag.StartClipboardSync()
		if *record != "" {
			if err := ag.StartRecording(*record); err != nil {
				fmt.Fprintln(os.Stderr, "record:", err)
			} else {
				fmt.Println("Recording to", *record)
			}
		}
		fmt.Printf("LAN:  http://%s:%d/\n", ag.LANIP, *port)
		fmt.Printf("WAN:  http://%s:%d/\n", ag.WANIP, *port)
		fmt.Printf("Password: %s\n", pw)
		if *ui {
			url := fmt.Sprintf("http://127.0.0.1:%d/ui#pw=%s", *port, pw)
			// Wait for Serve to bind before handing the URL to the browser.
			go func() {
				time.Sleep(700 * time.Millisecond)
				if err := openBrowser(url); err != nil {
					fmt.Fprintln(os.Stderr, "open browser:", err)
					fmt.Println("Open this URL:", url)
				}
			}()
		}
		hn, _ := os.Hostname()
		if adv, err := mdns.Advertise(id.Alias, hn+".", *port, []string{"peerId=" + id.Formatted()}); err == nil {
			defer adv.Server.Shutdown()
			fmt.Println("mDNS: advertised _office-rdp._tcp")
		} else {
			fmt.Println("mDNS: failed to advertise:", err)
		}
		if *rendezvous != "" {
			rendezvousClient := relayHTTPClient(*relayInsecure, *relayCA)
			go func() {
				for {
					body, _ := json.Marshal(map[string]any{
						"peerId": ag.PeerID, "alias": ag.Alias, "status": "online",
						"caps": map[string]any{"host": true},
						"addr": fmt.Sprintf("%s:%d", ag.LANIP, *port),
					})
					resp, err := rendezvousClient.Post(*rendezvous+"/register", "application/json", bytes.NewReader(body))
					if err == nil {
						resp.Body.Close()
					}
					time.Sleep(30 * time.Second)
				}
			}()
			fmt.Printf("Rendezvous: %s\n", *rendezvous)
			fmt.Printf("Relay: %s (tunnel for %s)\n", *rendezvous, ag.PeerID)
			go (&relay.Client{
				Rendezvous: *rendezvous,
				PeerID:     ag.PeerID,
				Password:   pw,
				LocalPort:  *port,
				Insecure:   *relayInsecure,
				CAFile:     *relayCA,
			}).Run()
			go ag.RunSignaling(*rendezvous)
			go func() {
				time.Sleep(2 * time.Second)
				resp, err := rendezvousClient.Get(*rendezvous + "/policy?peerId=" + id.Formatted())
				if err != nil || resp.StatusCode != 200 {
					if resp != nil {
						resp.Body.Close()
					}
					return
				}
				var pol map[string]any
				json.NewDecoder(resp.Body).Decode(&pol)
				resp.Body.Close()
				if v, ok := pol["readOnly"].(bool); ok {
					ag.ReadOnly = v
					fmt.Println("Policy applied: readOnly =", v)
				}
			}()
		}
		if err := ag.Serve(); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
		return
	}

	if *connect != "" {
		fmt.Printf("connecting to %s (stub) — local id %s\n", *connect, id.Formatted())
		return
	}
	if *wake != "" {
		mac, err := net.ParseMAC(*wake)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad mac:", err)
			os.Exit(1)
		}
		packet := make([]byte, 6, 6+6*16)
		for i := 0; i < 6; i++ {
			packet[i] = 0xFF
		}
		for i := 0; i < 16; i++ {
			packet = append(packet, mac...)
		}
		conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: 9})
		if err != nil {
			fmt.Fprintln(os.Stderr, "dial:", err)
			os.Exit(1)
		}
		defer conn.Close()
		if _, err := conn.Write(packet); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Println("Wake-on-LAN packet sent to", *wake)
		return
	}

	if *doctor {
		fmt.Println("Peer ID:", id.Formatted())
		fmt.Println("Alias:", id.Alias)
		n := screenshot.NumActiveDisplays()
		fmt.Printf("Displays: %d\n", n)
		stream, err := mediadevices.GetDisplayMedia(mediadevices.MediaStreamConstraints{
			Video: func(c *mediadevices.MediaTrackConstraints) {},
		})
		if err != nil {
			fmt.Println("Capture: FAIL -", err)
		} else {
			fmt.Println("Capture: OK")
			for _, t := range stream.GetVideoTracks() {
				t.Close()
			}
		}
		ln, err := net.Listen("tcp", ":8088") // #nosec G102 -- local port probe
		if err != nil {
			fmt.Println("Port 8088: IN USE")
		} else {
			fmt.Println("Port 8088: free")
			ln.Close()
		}
		return
	}

	if *displays {
		n := screenshot.NumActiveDisplays()
		for i := 0; i < n; i++ {
			fmt.Println(i)
		}
		return
	}

	if *install {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "install:", err)
			return
		}
		password := servicePassword()
		switch runtime.GOOS {
		case "darwin":
			dir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
			if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- fixed per-user dir
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			path := filepath.Join(dir, "com.eminentailabs.officerdp.plist")
			content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.eminentailabs.officerdp</string>
<key>ProgramArguments</key><array>
<string>%s</string><string>--host</string><string>--port</string><string>8088</string>
<string>--password</string><string>%s</string>
</array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
</dict></plist>`, html.EscapeString(exe), html.EscapeString(password))
			// Fixed user LaunchAgents path; password comes from a 0600 file.
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G306 G703
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			fmt.Println("Installed LaunchAgent:", path)
			fmt.Println("Access password:", password)
			fmt.Println("Load with: launchctl load", path)
		case "linux":
			dir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
			if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- fixed per-user dir
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			path := filepath.Join(dir, "office-rdp.service")
			content := fmt.Sprintf(`[Unit]
Description=RDP Link host agent
[Service]
ExecStart=%s --host --port 8088 --password %s
Restart=always
[Install]
WantedBy=default.target
`, exe, password)
			// Fixed systemd user unit path; password comes from a 0600 file.
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G306 G703
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			fmt.Println("Installed systemd user service:", path)
			fmt.Println("Access password:", password)
			fmt.Println("Enable with: systemctl --user enable --now office-rdp")
		default:
			fmt.Println("Service install not supported on", runtime.GOOS)
		}
		return
	}

	if *pairCode {
		c := pairing.NewCode(5 * time.Minute)
		fmt.Println("Pairing code:", c.Value, "(expires in 5 minutes)")
		return
	}

	// default: print id
	fmt.Printf("My ID: %s  (alias: %s)\n", id.Formatted(), id.Alias)
	fmt.Println("Use --help for commands.")
}

// servicePassword persists a password for unattended access so restarts keep
// the same credential.
func servicePassword() string {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "office-rdp", "service-password")
	if data, err := os.ReadFile(path); err == nil { // #nosec G304
		if pw := strings.TrimSpace(string(data)); pw != "" {
			return pw
		}
	}
	pw := randomPassword()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		_ = os.WriteFile(path, []byte(pw+"\n"), 0o600) // #nosec G304 G306
	}
	return pw
}

func randomPassword() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	n := binary.BigEndian.Uint32(buf) % 1000000
	return fmt.Sprintf("%06d", n)
}

// openBrowser launches the system default browser for the control panel.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 -- url is built from the agent's own listen address.
		cmd = exec.Command("open", url)
	case "windows":
		// #nosec G204 -- url is built from the agent's own listen address.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		// #nosec G204 -- url is built from the agent's own listen address.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
