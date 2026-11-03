package host

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
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
	switch {
	case haveTool("pactl"):
		return "pulse:default"
	case os.Getenv("XDG_SESSION_TYPE") != "":
		return "pulse:default"
	default:
		// avfoundation index 0 is the default audio input on macOS. Without a
		// loopback device this is the microphone, not system output.
		return "avfoundation::0"
	}
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
	input, err := ffmpegInput(a.defaultAudioInput())
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
	if strings.HasPrefix(a.defaultAudioInput(), "lavfi:") {
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
	player := "ffplay"
	if !haveTool(player) && !haveTool("ffmpeg") {
		http.Error(w, "no audio player on the host", http.StatusServiceUnavailable)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	cmd := exec.Command(player, "-hide_banner", "-loglevel", "quiet",
		"-nodisp", "-autoexit", "-f", "webm", "-i", "pipe:0")
	if player == "ffmpeg" {
		cmd = exec.Command("ffmpeg", "-hide_banner", "-loglevel", "quiet",
			"-f", "webm", "-i", "pipe:0", "-f", "audiotoolbox", "-")
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
	if device == "" {
		device = "0"
	}
	format := "avfoundation"
	input := device
	args := []string{"-hide_banner", "-loglevel", "error"}
	switch {
	case device == "testsrc":
		// Synthetic source for CI/validation without a camera or permission.
		args = append(args, "-re", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=15")
	case strings.HasPrefix(device, "/dev/"):
		args = append(args, "-f", "v4l2", "-i", input)
	default:
		args = append(args, "-f", format, "-framerate", "30", "-i", input)
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
	target := "http://127.0.0.1:" + strconv.Itoa(port) + "/" + tail
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	// #nosec G704 -- the destination is always 127.0.0.1 and a port that was
	// explicitly allow-listed with --forward.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Content-Type", "Accept", "Accept-Language"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req) // #nosec G704 -- loopback + allow-listed port
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		if k == "Content-Length" || k == "Transfer-Encoding" || k == "Connection" {
			continue
		}
		for _, item := range v {
			w.Header().Add(k, item)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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
