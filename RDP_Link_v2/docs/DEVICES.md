# Device bridge: audio, camera, serial, ports and USB

RDP Link exposes optional host devices to guests. Everything is off by default,
requires the host password, and reports availability through
`GET /capabilities` (authenticated).

| Feature | Endpoint | Host requirement | Works over relay |
| --- | --- | --- | --- |
| Speaker (host → guest) | `GET /audio` (WebM/Opus) | `ffmpeg` + capture source | Yes (streamed) |
| Microphone (guest → host) | `WS /mic` (WebM/Opus) | `ffplay` (or `ffmpeg`) | LAN only* |
| Camera (host → guest) | `GET /camera.mjpg` (MJPEG) | `ffmpeg` + camera | Yes (streamed) |
| Serial | `WS /serial` | `--serial <device|tcp:host:port>` | LAN only* |
| TCP ports | `GET/POST /port/<port>/…` | `--forward <ports>` | Yes |
| USB storage | `/files`, `/upload`, `/download` | — | Yes |

\* The relay tunnel is HTTP request/response; WebSocket endpoints (`/mic`,
`/serial`) are bridged only on the direct LAN path. Audio and camera are
streamed over the tunnel with chunked frames and do work through the relay.

---

## 1. Speaker (host audio → guest)

```sh
# Linux/PipeWire: "default" is usually the speaker monitor
office-rdp --host --audio-input pulse:default

# macOS microphone (system output needs a loopback device such as BlackHole)
office-rdp --host --audio-input avfoundation::0

# Any ffmpeg source, e.g. a test tone
office-rdp --host --audio-input "lavfi:sine=frequency=440"
```

The endpoint streams **WebM/Opus**, which the browser plays natively and the
Tk viewer plays through `ffplay` (the **Audio** button). The browser drawer has
Play/Stop controls.

System audio on macOS requires a loopback driver (BlackHole/Loopback); without
one, `avfoundation::0` is the microphone. On Linux, PulseAudio/PipeWire expose
a `.monitor` source. On Windows, use a loopback capture device with `dshow`.

## 2. Microphone (guest → host)

The browser uses `getUserMedia` + `MediaRecorder` (WebM/Opus) and streams
binary frames to `WS /mic`; the host pipes them to `ffplay`.

**Browser requirement:** microphone capture needs a secure context — HTTPS or
`http://localhost`. On a plain `http://192.168.x.x` page the browser blocks it;
the drawer reports the error. Use the relay's TLS URL or `localhost` for mic
testing.

**Android:** the app declares `RECORD_AUDIO` and grants WebView
`RESOURCE_AUDIO_CAPTURE` after the runtime prompt, but Chromium still requires
a secure context for `getUserMedia`; use an HTTPS relay URL for microphone on
Android.

## 3. Camera (host → guest)

```sh
office-rdp --host --camera-device 0            # macOS/Windows index
office-rdp --host --camera-device /dev/video0  # Linux
office-rdp --host --camera-device testsrc       # synthetic, for CI
```

The stream is `multipart/x-mixed-replace` MJPEG (640 px wide, 15 fps, q=7) and
is displayed by the browser drawer or any MJPEG viewer. macOS must grant
Camera permission to the terminal/app running the host; until then the endpoint
returns `503 device did not start (permission prompt?)`.

This is a **camera preview**, not a virtual camera inside host applications.
Making the guest camera appear as a device in host apps requires a kernel
driver (CoreMediaIO extension on macOS, DirectShow filter on Windows,
v4l2loopback on Linux) and is out of scope for a user-space HTTP bridge.

## 4. Serial

```sh
office-rdp --host --serial /dev/tty.usbserial-1420
office-rdp --host --serial tcp:192.168.1.50:4001
```

`WS /serial` bridges raw bytes; the drawer provides a simple console
(`Connect`, output pane, line input). The device keeps its current line
settings; configure baud/parity with `stty` (Unix) or the device driver first.
Verified with a TCP echo and a PTY.

## 5. TCP ports

```sh
office-rdp --host --forward 9099,5432
```

`/port/9099/...` proxies HTTP to `127.0.0.1:9099` **only if 9099 is
allow-listed**. It works through the relay, so a guest can reach a host-local
web admin page without opening a firewall port. Arbitrary TCP (SSH, VNC) is not
proxied; use the serial/TCP tunnel or a dedicated forwarder.

## 6. USB

True USB device passthrough (a guest USB stick appearing as a host device)
requires OS drivers (usbip on Linux, usbip-win on Windows; macOS has no
supported equivalent without a DriverKit extension). This build instead
provides **USB storage sharing**: mount the volume on the host and use the
existing file transfer (`/files`, `/upload`, `/download`) — including from the
Android app.

The host does not enumerate or forward arbitrary USB devices, and does not
claim to.

---

## Verification (2026-11-03, macOS host)

```
GET /capabilities            {"audio":true,"mic":true,"camera":true,
                              "serial":"tcp:127.0.0.1:9101","ports":[9099]}
GET /audio (2 s)             200, 18,785 bytes, Opus 48 kHz mono
GET /audio via relay (2 s)   200, 18,785 bytes
GET /camera.mjpg (testsrc)   200, 505,915 bytes / 2 s, 640×480 JPEG frames
GET /camera.mjpg via relay   200, 505,915 bytes / 2 s
GET /port/9099/ (direct)     200 text/html
GET /port/9099/ (relay)      200
WS /serial + TCP echo        reply "serial-over-relay\n" (direct)
WS /mic + 10 KB WebM         host spawned ffplay and consumed the stream
```

A physical camera returned `503` until macOS Camera permission is granted; the
`testsrc` source validates the full MJPEG path without a device. Real serial
hardware uses the same WebSocket path as the TCP echo.

## Security notes

- Every endpoint requires the host password; `/capabilities` too.
- Ports are deny-by-default and must be allow-listed at startup.
- The relay forwards the real client IP (`X-Forwarded-For`) so throttling is
  per guest, not per relay.
- Audio/camera/serial are only exposed when the corresponding flag is set.
