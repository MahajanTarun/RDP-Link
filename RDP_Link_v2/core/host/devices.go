package host

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader only accepts WebSocket handshakes from this host's own pages.
// Native clients (no Origin header) are still allowed, but a web page on
// another site cannot open a device bridge.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return sameOrigin(r) },
}

// Device bridge: audio, camera, serial and allow-listed TCP ports. Everything
// is opt-in through host flags, authenticated like the screen stream, and
// degrades to "unavailable" when the platform tools/devices are missing.
//
// Generic USB device redirection is NOT possible without OS kernel drivers
// (DriverKit/DirectShow/usbip). USB *storage* is exposed through the normal
// file-transfer API instead; see docs/DEVICES.md.

type deviceCaps struct {
	Audio         bool     `json:"audio"`
	Mic           bool     `json:"mic"`
	Camera        bool     `json:"camera"`
	Serial        string   `json:"serial,omitempty"`
	Ports         []int    `json:"ports"`
	Tools         []string `json:"tools"`
	VirtualCamera string   `json:"virtualCamera,omitempty"`
	USBIP         bool     `json:"usbip"`
}

var audioSem = make(chan struct{}, 2)

// startStreamed runs cmd and waits up to 4s for its first output byte, so a
// missing device or permission prompt becomes a 503 instead of an empty 200.
func startStreamed(w http.ResponseWriter, cmd *exec.Cmd, contentType string) (io.ReadCloser, []byte, bool) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, nil, false
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		http.Error(w, "device unavailable", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	first := make([]byte, 1)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(stdout, first)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			http.Error(w, "device produced no data (permission or busy)", http.StatusServiceUnavailable)
			return nil, nil, false
		}
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		http.Error(w, "device did not start (permission prompt?)", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	return stdout, first, true
}

func haveTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ffmpegInput turns "avfoundation::0" into ["-f","avfoundation","-i",":0"].
func ffmpegInput(spec string) ([]string, error) {
	format, input, ok := strings.Cut(spec, ":")
	if !ok || format == "" || input == "" {
		return nil, errors.New("expected format:input")
	}
	return []string{"-f", format, "-i", input}, nil
}

func (a *Agent) defaultAudioInput() string {
	if a.AudioInput != "" {
		return a.AudioInput
	}
	switch runtime.GOOS {
	case "darwin":
		// A loopback device is the only way to capture system output on
		// macOS. Prefer one when it is installed; otherwise fall back to the
		// default input and let the operator choose a device explicitly.
		if index, ok := findAVFoundationAudio("blackhole", "soundflower", "loopback"); ok {
			return "avfoundation::" + index
		}
		return "avfoundation::0"
	case "windows":
		// DirectShow has no "default" alias. Prefer a loopback/virtual cable
		// device, then the first capture device the driver reports.
		if name, ok := findDShowAudio("stereo mix", "virtual-audio-capturer", "cable output"); ok {
			return "dshow:audio=" + name
		}
		if name, ok := firstDShowAudio(); ok {
			return "dshow:audio=" + name
		}
		return "dshow:audio=Stereo Mix"
	default:
		// On PulseAudio/PipeWire the default *source* is usually a
		// microphone. The sink's monitor source carries what the machine is
		// playing, which is what "host audio" means here.
		if haveTool("pactl") {
			if source := defaultMonitorSource(); source != "" {
				return "pulse:" + source
			}
		}
		return "pulse:default"
	}
}

// findAVFoundationAudio returns the avfoundation index of the first audio
// device whose name contains one of the given needles.
func findAVFoundationAudio(needles ...string) (string, bool) {
	if !haveTool("ffmpeg") {
		return "", false
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-f", "avfoundation",
		"-list_devices", "true", "-i", "")
	out, _ := cmd.CombinedOutput()
	audioSection := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), "avfoundation audio devices") {
			audioSection = true
			continue
		}
		if strings.Contains(strings.ToLower(line), "avfoundation video devices") {
			audioSection = false
			continue
		}
		if !audioSection {
			continue
		}
		m := avDeviceLine.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}
		lower := strings.ToLower(m[2])
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				return m[1], true
			}
		}
	}
	return "", false
}

var avDeviceLine = regexp.MustCompile(`\[(\d+)\]\s+(.+)$`)

// findDShowAudio returns the first DirectShow audio device whose name
// contains one of the given needles.
func findDShowAudio(needles ...string) (string, bool) {
	name, ok := scanDShowAudio(needles...)
	return name, ok
}

// firstDShowAudio returns the first DirectShow audio capture device.
func firstDShowAudio() (string, bool) {
	return scanDShowAudio()
}

func scanDShowAudio(needles ...string) (string, bool) {
	if !haveTool("ffmpeg") {
		return "", false
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-list_devices", "true",
		"-f", "dshow", "-i", "dummy")
	out, _ := cmd.CombinedOutput()
	audioSection := false
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "directshow audio devices") {
			audioSection = true
			continue
		}
		if strings.Contains(lower, "directshow video devices") {
			audioSection = false
			continue
		}
		if !audioSection {
			continue
		}
		m := dshowDeviceLine.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		name := m[1]
		if len(needles) == 0 {
			return name, true
		}
		lowerName := strings.ToLower(name)
		for _, needle := range needles {
			if strings.Contains(lowerName, needle) {
				return name, true
			}
		}
	}
	return "", false
}

var dshowDeviceLine = regexp.MustCompile(`"([^"]+)"\s+\(audio\)`)

// defaultMonitorSource returns the monitor source of the default PulseAudio
// sink, which is the machine's playback rather than the microphone.
func defaultMonitorSource() string {
	out, err := exec.Command("pactl", "get-default-sink").Output()
	if err != nil {
		return ""
	}
	sink := strings.TrimSpace(string(out))
	if sink == "" {
		return ""
	}
	monitor := sink + ".monitor"
	// Only return a source that actually exists.
	list, err := exec.Command("pactl", "list", "short", "sources").Output()
	if err != nil {
		return monitor
	}
	for _, line := range strings.Split(string(list), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == monitor {
			return monitor
		}
	}
	return ""
}

func (a *Agent) capabilities() deviceCaps {
	caps := deviceCaps{
		Audio:         haveTool("ffmpeg"),
		Mic:           haveTool("ffplay") || haveTool("ffmpeg"),
		Camera:        haveTool("ffmpeg"),
		Serial:        a.SerialPath,
		Tools:         []string{},
		Ports:         []int{},
		VirtualCamera: a.VirtualCamera,
		USBIP:         a.USBIP,
	}
	if haveTool("ffmpeg") {
		caps.Tools = append(caps.Tools, "ffmpeg")
	}
	if haveTool("ffplay") {
		caps.Tools = append(caps.Tools, "ffplay")
	}
	for p := range a.Forwards {
		caps.Ports = append(caps.Ports, p)
	}
	return caps
}

func (a *Agent) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.capabilities())
}

// ---- audio: host -> guest -------------------------------------------------

func (a *Agent) handleAudio(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if !haveTool("ffmpeg") {
		http.Error(w, "ffmpeg is not installed on the host", http.StatusServiceUnavailable)
		return
	}
	audioInput := a.defaultAudioInput()
	input, err := ffmpegInput(audioInput)
	if err != nil {
		http.Error(w, "invalid audio input", http.StatusServiceUnavailable)
		return
	}
	select {
	case audioSem <- struct{}{}:
		defer func() { <-audioSem }()
	default:
		http.Error(w, "too many audio listeners", http.StatusTooManyRequests)
		return
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	// -re paces synthetic/lavfi inputs; real devices are already realtime.
	if strings.HasPrefix(audioInput, "lavfi:") {
		args = append(args, "-re")
	}
	args = append(args, input...)
	args = append(args, "-vn", "-ac", "1", "-ar", "48000",
		"-c:a", "libopus", "-b:a", "64k", "-f", "webm", "-")
	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
	stdout, first, ok := startStreamed(w, cmd, "audio/webm")
	if !ok {
		return
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_, _ = w.Write(first)
	_, _ = io.Copy(w, stdout)
}

// ---- microphone: guest -> host -------------------------------------------

func (a *Agent) handleMicWS(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	// ffplay plays the stream directly; when it is missing ffmpeg does the
	// decoding and writes to the platform's default output.
	player := "ffplay"
	if !haveTool(player) {
		if !haveTool("ffmpeg") {
			http.Error(w, "no audio player on the host", http.StatusServiceUnavailable)
			return
		}
		player = "ffmpeg"
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	var cmd *exec.Cmd
	if player == "ffmpeg" {
		// #nosec G204 -- ffmpeg and the output format come from a fixed list.
		cmd = exec.Command("ffmpeg", "-hide_banner", "-loglevel", "quiet",
			"-f", "webm", "-i", "pipe:0", "-f", defaultAudioOutput(), "-")
	} else {
		cmd = exec.Command("ffplay", "-hide_banner", "-loglevel", "quiet",
			"-nodisp", "-autoexit", "-f", "webm", "-i", "pipe:0")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	go func() {
		_ = cmd.Wait()
		_ = ws.Close()
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if _, err := stdin.Write(data); err != nil {
			break
		}
	}
	_ = stdin.Close()
}

// defaultAudioOutput is the ffmpeg output device for the platform.
func defaultAudioOutput() string {
	switch runtime.GOOS {
	case "darwin":
		return "audiotoolbox"
	case "windows":
		return "waveout"
	default:
		return "pulse"
	}
}

// ---- camera: host -> guest (MJPEG) ---------------------------------------

func (a *Agent) handleCamera(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if !haveTool("ffmpeg") {
		http.Error(w, "ffmpeg is not installed on the host", http.StatusServiceUnavailable)
		return
	}
	device := a.CameraDevice
	args := []string{"-hide_banner", "-loglevel", "error"}
	switch {
	case device == "testsrc":
		// Synthetic source for CI/validation without a camera or permission.
		args = append(args, "-re", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=15")
	case strings.HasPrefix(device, "/dev/"):
		args = append(args, "-f", "v4l2", "-i", device)
	case runtime.GOOS == "linux":
		// The default Linux capture device is the first video node.
		if device == "" {
			device = "/dev/video0"
		}
		args = append(args, "-f", "v4l2", "-i", device)
	case runtime.GOOS == "windows":
		// DirectShow takes the device name, for example "HD Webcam".
		if device == "" {
			device = "video=0"
		}
		args = append(args, "-f", "dshow", "-i", device)
	default:
		// macOS: the first AVFoundation capture device.
		if device == "" {
			device = "0"
		}
		args = append(args, "-f", "avfoundation", "-framerate", "30", "-i", device)
	}
	args = append(args, "-vf", "scale=640:-2", "-r", "15", "-q:v", "7",
		"-f", "mpjpeg", "-")
	cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
	stdout, first, ok := startStreamed(w, cmd, "multipart/x-mixed-replace; boundary=--ffmpeg")
	if !ok {
		return
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_, _ = w.Write(first)
	_, _ = io.Copy(w, stdout)
}

// ---- serial ---------------------------------------------------------------

func (a *Agent) handleSerialWS(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if a.SerialPath == "" {
		http.Error(w, "serial not configured", http.StatusServiceUnavailable)
		return
	}
	var conn net.Conn
	var err error
	if strings.HasPrefix(a.SerialPath, "tcp:") {
		conn, err = net.DialTimeout("tcp", strings.TrimPrefix(a.SerialPath, "tcp:"), 5*time.Second)
	} else {
		var f *os.File
		f, err = os.OpenFile(a.SerialPath, os.O_RDWR, 0) // #nosec G304 -- operator-configured device
		if err == nil {
			conn = fileConn{f}
		}
	}
	if err != nil {
		http.Error(w, "serial open failed", http.StatusServiceUnavailable)
		return
	}
	defer conn.Close()

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	var once sync.Once
	closeBoth := func() { once.Do(func() { _ = ws.Close(); _ = conn.Close() }) }
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		closeBoth()
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if _, err := conn.Write(data); err != nil {
			break
		}
	}
	closeBoth()
}

// fileConn adapts an *os.File to net.Conn for the serial pipe.
type fileConn struct{ *os.File }

func (fileConn) LocalAddr() net.Addr              { return dummyAddr("file") }
func (fileConn) RemoteAddr() net.Addr             { return dummyAddr("file") }
func (fileConn) SetDeadline(time.Time) error      { return nil }
func (fileConn) SetReadDeadline(time.Time) error  { return nil }
func (fileConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return string(d) }
func (d dummyAddr) String() string  { return string(d) }

// ---- allow-listed TCP ports ----------------------------------------------

func (a *Agent) handlePortProxy(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/port/")
	portStr, tail, _ := strings.Cut(rest, "/")
	port, err := strconv.Atoi(portStr)
	if err != nil || !a.Forwards[port] {
		http.Error(w, "port not allowed", http.StatusForbidden)
		return
	}
	// A reverse proxy keeps cookies, redirects and relative assets working,
	// which a hand-rolled copy did not.
	prefix := "/port/" + portStr
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// #nosec G704 -- the destination is always 127.0.0.1 and a port
			// that was explicitly allow-listed with --forward.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "127.0.0.1:" + portStr
			pr.Out.URL.Path = "/" + strings.TrimPrefix(tail, "/")
			pr.Out.URL.RawPath = ""
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			if loc := resp.Header.Get("Location"); loc != "" {
				if u, err := url.Parse(loc); err == nil {
					if u.IsAbs() {
						u.Path = prefix + "/" + strings.TrimPrefix(u.Path, "/")
						u.RawPath = ""
						u.Host = ""
						u.Scheme = ""
					} else if strings.HasPrefix(u.Path, "/") {
						u.Path = prefix + u.Path
						u.RawPath = ""
					}
					resp.Header.Set("Location", u.String())
				}
			}
			if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 {
				resp.Header.Del("Set-Cookie")
				for _, raw := range cookies {
					cookie, err := http.ParseSetCookie(raw)
					if err != nil {
						resp.Header.Add("Set-Cookie", raw)
						continue
					}
					if cookie.Path == "" || cookie.Path == "/" {
						cookie.Path = prefix + "/"
					}
					resp.Header.Add("Set-Cookie", cookie.String())
				}
			}
			return nil
		},
	}
	proxy.ServeHTTP(w, r)
}

// ---- guest camera -> host virtual camera (v4l2loopback) -------------------

func (a *Agent) handleGuestCameraWS(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	if a.VirtualCamera == "" {
		http.Error(w, "virtual camera not configured", http.StatusServiceUnavailable)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if len(data) < 2 || len(data) > 8<<20 {
			continue
		}
		a.guestCamMu.Lock()
		a.guestCamJPG = append(a.guestCamJPG[:0], data...)
		a.guestCamSeq++
		a.guestCamAt = time.Now()
		a.guestCamMu.Unlock()
	}
}

// PublishVirtualCamera feeds guest frames (or the host screen until a guest
// connects) into a v4l2loopback device so host applications see a camera.
func (a *Agent) PublishVirtualCamera() {
	if a.VirtualCamera == "" {
		return
	}
	go func() {
		for {
			select {
			case <-a.done:
				return
			default:
			}
			// #nosec G204 -- ffmpeg with a fixed argument list; the device
			// path comes from the operator's --virtual-camera flag.
			cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
				"-f", "mjpeg", "-i", "pipe:0",
				"-vf", "scale=640:480", "-r", "15", "-pix_fmt", "yuv420p",
				"-f", "v4l2", a.VirtualCamera)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			if err := cmd.Start(); err != nil {
				log.Printf("virtual camera: %v", err)
				time.Sleep(3 * time.Second)
				continue
			}
			for {
				select {
				case <-a.done:
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return
				default:
				}
				a.guestCamMu.Lock()
				frame := append([]byte(nil), a.guestCamJPG...)
				fresh := time.Since(a.guestCamAt) < 5*time.Second
				a.guestCamMu.Unlock()
				// Guest camera wins while it is streaming; otherwise show the
				// host screen so the device always has a signal.
				if len(frame) == 0 || !fresh {
					a.mu.Lock()
					frame = append([]byte(nil), a.frameJPG...)
					a.mu.Unlock()
				}
				if len(frame) > 0 {
					if _, err := stdin.Write(frame); err != nil {
						break
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			time.Sleep(time.Second)
		}
	}()
}

// ---- USB/IP export (Linux) ------------------------------------------------

type usbDevice struct {
	BusID    string `json:"busid"`
	VendorID string `json:"vendorId,omitempty"`
	Product  string `json:"product"`
}

func parseUSBIPList(out string) []usbDevice {
	devices := []usbDevice{}
	var current *usbDevice
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- busid ") {
			fields := strings.Fields(trimmed) // ["-", "busid", "3-10", "(vid:pid)"]
			if len(fields) >= 3 {
				dev := usbDevice{BusID: fields[2]}
				if len(fields) > 3 {
					dev.VendorID = strings.Trim(fields[3], "()")
				}
				devices = append(devices, dev)
				current = &devices[len(devices)-1]
			}
			continue
		}
		if current != nil && trimmed != "" && !strings.HasPrefix(trimmed, "-") {
			if current.Product == "" {
				parts := strings.SplitN(trimmed, "(", 2)
				current.Product = strings.TrimSpace(parts[0])
			}
		}
	}
	return devices
}

func (a *Agent) handleUSBList(w http.ResponseWriter, r *http.Request) {
	if !a.auth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !a.USBIP {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "devices": []usbDevice{}})
		return
	}
	out, err := exec.Command("usbip", "list", "-l").Output()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled": true, "error": "usbip unavailable", "devices": []usbDevice{}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"enabled": true, "devices": parseUSBIPList(string(out)),
	})
}

func (a *Agent) usbAction(w http.ResponseWriter, r *http.Request, action string) {
	if !a.auth(w, r) {
		return
	}
	if !a.USBIP {
		http.Error(w, "usbip disabled", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		BusID string `json:"busid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if ok, _ := regexp.MatchString(`^[0-9]+-[0-9]+(\.[0-9]+)*$`, body.BusID); !ok {
		http.Error(w, "invalid bus id", http.StatusBadRequest)
		return
	}
	if action == "bind" {
		// The host controller module may not be loaded yet; harmless if it is.
		_ = exec.Command("modprobe", "usbip-host").Run()
	}
	// #nosec G204 -- action is bind/unbind from the two callers and busid is
	// validated as digits-dash above.
	cmd := exec.Command("usbip", action, "-b", body.BusID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		status := http.StatusBadGateway
		if strings.Contains(strings.ToLower(msg), "permission") ||
			strings.Contains(strings.ToLower(msg), "root") ||
			strings.Contains(strings.ToLower(msg), "must be run as") {
			status = http.StatusForbidden
			msg = "usbip needs root on the host: " + msg
		}
		http.Error(w, msg, status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) handleUSBBind(w http.ResponseWriter, r *http.Request) {
	a.usbAction(w, r, "bind")
}

func (a *Agent) handleUSBUnbind(w http.ResponseWriter, r *http.Request) {
	a.usbAction(w, r, "unbind")
}
