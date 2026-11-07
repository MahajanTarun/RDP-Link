package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/eminentailabs/office-rdp/core/host"
	"github.com/eminentailabs/office-rdp/core/identity"
	"github.com/eminentailabs/office-rdp/core/input"
	"github.com/eminentailabs/office-rdp/core/mdns"
	"github.com/eminentailabs/office-rdp/core/pairing"
	"github.com/eminentailabs/office-rdp/core/peerauth"
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
	connect := flag.String("connect", "",
		"open the browser viewer for a peer ID or an http(s) URL")
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
	passwordFile := flag.String("password-file", "",
		"read the unattended password from a file (keeps it out of the process list)")
	listPeers := flag.String("list-peers", "",
		"list the peers registered with a rendezvous server")
	adminToken := flag.String("admin-token", "",
		"admin token for the rendezvous (also read from RDPLINK_ADMIN_TOKEN)")
	readOnly := flag.Bool("read-only", false, "disable remote input (view-only)")
	rendezvous := flag.String("rendezvous", "", "rendezvous base URL (e.g. http://host:8080)")
	list := flag.Bool("list", false, "list peers")
	alias := flag.String("alias", "", "override alias")
	ui := flag.Bool("ui", false, "open the control panel in a browser")
	relayInsecure := flag.Bool("relay-insecure", false, "skip TLS verification for the relay (testing only)")
	relayCA := flag.String("relay-ca", "", "custom CA bundle for the relay's TLS certificate")
	audioInput := flag.String("audio-input", "", "ffmpeg audio input as format:input (e.g. avfoundation::0)")
	cameraDevice := flag.String("camera-device", "", "camera device for MJPEG (default 0 / /dev/video0)")
	serialPath := flag.String("serial", "", "serial device (or tcp:host:port) to expose to guests")
	forwardList := flag.String("forward", "", "comma-separated host TCP ports guests may reach")
	virtualCamera := flag.String("virtual-camera", "", "v4l2loopback device fed by guest camera frames (Linux)")
	enableUSBIP := flag.Bool("usbip", false, "enable USB/IP export controls (Linux)")
	testPattern := flag.Bool("test-pattern", false, "stream a synthetic test pattern instead of the screen")
	captureLegacy := flag.Bool("capture-legacy", false, "use the CoreGraphics capture path on macOS (workaround for ScreenCaptureKit stalls)")
	flag.Parse()

	if *version {
		fmt.Println("office-rdp 0.2.1")
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

	if *listPeers != "" {
		// Listing the registry needs a signed request, so the launcher calls
		// the agent instead of fetching the list itself.
		client := relayHTTPClient(*relayInsecure, *relayCA)
		token := *adminToken
		if token == "" {
			token = strings.TrimSpace(os.Getenv("RDPLINK_ADMIN_TOKEN"))
		}
		var extra map[string]string
		if token != "" {
			extra = map[string]string{"X-Rdp-Admin-Token": token}
		}
		resp, err := peerauth.SendWith(client, http.MethodGet,
			strings.TrimRight(*listPeers, "/")+"/peers", nil, id.PrivateKey, "", extra)
		if err != nil {
			fmt.Fprintln(os.Stderr, "list-peers:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "list-peers:", resp.Status, strings.TrimSpace(string(body)))
			os.Exit(1)
		}
		fmt.Println(strings.TrimSpace(string(body)))
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
		pw := resolvePassword(*password, *passwordFile)
		if pw == "" {
			pw = randomPassword()
			if pw == "" {
				fmt.Fprintln(os.Stderr, "cannot generate a cryptographically secure access password")
				os.Exit(1)
			}
			path := hostPasswordPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				fmt.Fprintln(os.Stderr, "cannot create private password directory:", err)
				os.Exit(1)
			}
			if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil { // #nosec G306
				fmt.Fprintln(os.Stderr, "cannot save generated password:", err)
				os.Exit(1)
			}
			_ = os.Chmod(path, 0o600)
			fmt.Println("Generated access password saved to:", path)
		}
		ag := host.New(id.Formatted(), id.Alias, pw, *port)
		ag.ReadOnly = *readOnly
		ag.TestPattern = *testPattern
		ag.ForceLegacyCapture = *captureLegacy
		ag.AudioInput = *audioInput
		ag.CameraDevice = *cameraDevice
		ag.SerialPath = *serialPath
		ag.VirtualCamera = *virtualCamera
		ag.USBIP = *enableUSBIP
		for _, item := range strings.Split(*forwardList, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(item)); err == nil && n > 0 && n < 65536 {
				ag.Forwards[n] = true
			}
		}
		if err := ag.StartCapture(*display); err != nil {
			fmt.Fprintln(os.Stderr, "capture:", err)
		}
		ag.PublishVirtualCamera()
		if *record != "" {
			if err := ag.StartRecording(*record); err != nil {
				fmt.Fprintln(os.Stderr, "record:", err)
			} else {
				fmt.Println("Recording to", *record)
			}
		}
		fmt.Printf("LAN:  http://%s:%d/\n", ag.LANIP, *port)
		fmt.Printf("WAN:  http://%s:%d/\n", ag.WANIP, *port)
		if *ui {
			// The control panel asks for the password and exchanges it for a
			// session cookie. Never place the credential in a browser URL or
			// the `open`/`xdg-open` process arguments.
			url := fmt.Sprintf("http://127.0.0.1:%d/ui", *port)
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
					// The registration is signed with the device key, so
					// nobody else can overwrite this peer ID in the
					// rendezvous registry.
					resp, err := peerauth.Send(rendezvousClient, http.MethodPost,
						*rendezvous+"/register", body, id.PrivateKey, "application/json")
					if err == nil {
						resp.Body.Close()
					}
					select {
					case <-ag.Done():
						return
					case <-time.After(30 * time.Second):
					}
				}
			}()
			fmt.Printf("Rendezvous: %s\n", *rendezvous)
			fmt.Printf("Relay: %s (tunnel for %s)\n", *rendezvous, ag.PeerID)
			go (&relay.Client{
				Rendezvous: *rendezvous,
				PeerID:     ag.PeerID,
				PrivateKey: id.PrivateKey,
				LocalPort:  *port,
				Insecure:   *relayInsecure,
				CAFile:     *relayCA,
				Secret:     ag.RelaySecret(),
				Done:       ag.Done(),
			}).Run()
			go func() {
				select {
				case <-ag.Done():
					return
				case <-time.After(2 * time.Second):
				}
				resp, err := peerauth.Send(rendezvousClient, http.MethodGet,
					*rendezvous+"/policy?peerId="+id.Formatted(), nil, id.PrivateKey, "")
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
		target := strings.TrimSpace(*connect)
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			// Accept the same spaced 9-digit form the UI shows.
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, target)
			if len(digits) != 9 {
				fmt.Fprintln(os.Stderr, "connect: expected a 9-digit peer ID or an http(s) URL")
				os.Exit(2)
			}
			if *rendezvous == "" {
				fmt.Fprintln(os.Stderr, "connect: --rendezvous is required to resolve a peer ID")
				os.Exit(2)
			}
			client := relayHTTPClient(*relayInsecure, *relayCA)
			resp, err := client.Get(strings.TrimRight(*rendezvous, "/") + "/resolve?peerId=" + digits)
			if err != nil {
				fmt.Fprintln(os.Stderr, "connect:", err)
				os.Exit(1)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				fmt.Fprintln(os.Stderr, "connect:", resp.Status, strings.TrimSpace(string(body)))
				os.Exit(1)
			}
			var info struct {
				Addr string `json:"addr"`
				URL  string `json:"url"`
			}
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
				fmt.Fprintln(os.Stderr, "connect:", err)
				os.Exit(1)
			}
			// Same LAN is faster, so use it when it answers; the relay is
			// the fallback for NAT and changing addresses.
			target = ""
			if addr := strings.TrimSpace(info.Addr); addr != "" {
				if conn, err := net.DialTimeout("tcp", addr, 700*time.Millisecond); err == nil {
					_ = conn.Close()
					target = "http://" + addr + "/"
				}
			}
			if target == "" {
				target = strings.TrimSpace(info.URL)
			}
			if target == "" && info.Addr != "" {
				target = "http://" + strings.TrimSpace(info.Addr) + "/"
			}
			if target == "" {
				fmt.Fprintln(os.Stderr, "connect: the rendezvous returned no address for this peer")
				os.Exit(1)
			}
		}
		fmt.Println("Opening:", target)
		if err := openBrowser(target); err != nil {
			fmt.Fprintln(os.Stderr, "open browser:", err)
			fmt.Println("Open this URL:", target)
		}
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
		if err := input.PermissionError(); err != nil {
			fmt.Println("Input: FAIL -", err,
				"(System Settings → Privacy & Security → Accessibility)")
		} else {
			fmt.Println("Input: OK")
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
		// Generate/persist the unattended password, then refer to it by path.
		// Printing it here would copy the credential into terminal scrollback
		// and CI logs.
		_ = servicePassword()
		switch runtime.GOOS {
		case "darwin":
			dir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
			if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- fixed per-user dir
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			path := filepath.Join(dir, "com.eminentailabs.officerdp.plist")
			// The password is read from a 0600 file, so it never shows up in
			// the process list.
			content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.eminentailabs.officerdp</string>
<key>ProgramArguments</key><array>
<string>%s</string><string>--host</string><string>--port</string><string>8088</string>
<string>--password-file</string><string>%s</string>
</array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
</dict></plist>`, html.EscapeString(exe), html.EscapeString(servicePasswordPath()))
			// Fixed user LaunchAgents path; password comes from a 0600 file.
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G306 G703
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			fmt.Println("Installed LaunchAgent:", path)
			fmt.Println("Access password file:", servicePasswordPath())
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
ExecStart=%s --host --port 8088 --password-file %s
Restart=always
[Install]
WantedBy=default.target
`, exe, servicePasswordPath())
			// Fixed systemd user unit path; password comes from a 0600 file.
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G306 G703
				fmt.Fprintln(os.Stderr, "install:", err)
				return
			}
			fmt.Println("Installed systemd user service:", path)
			fmt.Println("Access password file:", servicePasswordPath())
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

// servicePasswordPath is where the unattended password is stored.
func servicePasswordPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "office-rdp", "service-password")
}

// hostPasswordPath is the per-user default location for a generated
// interactive host password. The file is created with owner-only permissions.
func hostPasswordPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "office-rdp", "host-password")
}

// servicePassword persists a password for unattended access so restarts keep
// the same credential.
func servicePassword() string {
	path := servicePasswordPath()
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

// resolvePassword reads the access password from the flag, a file or the
// environment, in that order. A file or environment variable keeps the
// password out of the process list.
func resolvePassword(flagValue, file string) string {
	if flagValue != "" {
		return flagValue
	}
	if file != "" {
		if data, err := os.ReadFile(file); err == nil { // #nosec G304 -- operator supplied
			if pw := strings.TrimSpace(string(data)); pw != "" {
				return pw
			}
		}
	}
	return strings.TrimSpace(os.Getenv("RDPLINK_PASSWORD"))
}

func randomPassword() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// Fail closed rather than falling back to a low-entropy time-derived credential.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
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
