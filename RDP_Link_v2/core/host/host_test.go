package host

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func startAgent(t *testing.T, port int) *Agent {
	t.Helper()
	a := New("123", "host", "secret", port)
	go func() { _ = a.Serve() }()
	time.Sleep(200 * time.Millisecond)
	return a
}

func TestInfoEndpointNeedsCredentials(t *testing.T) {
	startAgent(t, 18091)
	resp, err := http.Get("http://127.0.0.1:18091/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", resp.StatusCode)
	}
}

func TestInfoEndpointWithPasswordHeader(t *testing.T) {
	startAgent(t, 18092)
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18092/info", nil)
	req.Header.Set("X-Office-Password", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var info map[string]any
	json.NewDecoder(resp.Body).Decode(&info)
	if info["peerId"] != "123" {
		t.Fatalf("peerId %v", info["peerId"])
	}
}

func TestSessionCookieAndToken(t *testing.T) {
	startAgent(t, 18093)
	// Exchange the password for a session.
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18093/session", nil)
	req.Header.Set("X-Office-Password", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("session status %d", resp.StatusCode)
	}
	var payload struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&payload)
	if payload.Token == "" {
		t.Fatal("no session token")
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	// The cookie authenticates.
	req2, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18093/info", nil)
	req2.AddCookie(cookie)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("cookie auth status %d", resp2.StatusCode)
	}

	// The token in the query works too (media players cannot set headers).
	resp3, err := http.Get("http://127.0.0.1:18093/info?token=" + payload.Token)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Fatalf("token auth status %d", resp3.StatusCode)
	}

	// A wrong password never yields a session.
	bad, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18093/session", nil)
	bad.Header.Set("X-Office-Password", "wrong")
	badResp, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", badResp.StatusCode)
	}
}

func TestPasswordInQueryIsRejected(t *testing.T) {
	startAgent(t, 18094)
	resp, err := http.Get("http://127.0.0.1:18094/info?password=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("password in query must not authenticate, got %d", resp.StatusCode)
	}
}

func TestCrossOriginRequestRejected(t *testing.T) {
	startAgent(t, 18095)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18095/input",
		strings.NewReader(`{"type":"click","x":1,"y":1}`))
	req.Header.Set("X-Office-Password", "secret")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin request must be rejected, got %d", resp.StatusCode)
	}
}

func TestForwardedForNeedsRelaySecret(t *testing.T) {
	a := New("123", "host", "secret", 18096)
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/info", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if ip := clientIP(req, a.relaySecret); ip != "127.0.0.1" {
		t.Fatalf("spoofed X-Forwarded-For was trusted: %s", ip)
	}
	req.Header.Set("X-Rdp-Relay-Secret", a.relaySecret)
	if ip := clientIP(req, a.relaySecret); ip != "203.0.113.9" {
		t.Fatalf("relay X-Forwarded-For ignored: %s", ip)
	}
}

func TestFailureTableIsBounded(t *testing.T) {
	a := New("123", "host", "secret", 18097)
	for i := 0; i < maxFailEntries+500; i++ {
		a.noteFailure("10.0.0." + strconv.Itoa(i%250) + ":" + strconv.Itoa(i))
	}
	a.failMu.Lock()
	size := len(a.fails)
	a.failMu.Unlock()
	if size > maxFailEntries {
		t.Fatalf("failure table grew to %d entries", size)
	}
}

func TestDeviceBridgeRejectsCrossOrigin(t *testing.T) {
	startAgent(t, 18098)
	for _, path := range []string{"/capabilities", "/usb/list", "/port/8080/", "/audio"} {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18098"+path, nil)
		req.Header.Set("X-Office-Password", "secret")
		req.Header.Set("Origin", "http://evil.example")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s must reject cross-origin requests, got %d", path, resp.StatusCode)
		}
	}
}

func TestSessionRejectsCrossOrigin(t *testing.T) {
	startAgent(t, 18097)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18097/session",
		strings.NewReader(`{"password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin session request must be rejected, got %d", resp.StatusCode)
	}
}

func TestClipboardReadsAreCached(t *testing.T) {
	a := New("123", "host", "secret", 18096)
	a.clipText = "cached value"
	a.clipReadAt = time.Now()
	if got := a.GetClipboard(); got != "cached value" {
		t.Fatalf("cached clipboard not returned: %q", got)
	}
	// An expired cache is refreshed from the operating system.
	a.clipReadAt = time.Now().Add(-2 * clipboardCacheTTL)
	_ = a.GetClipboard()
	a.clipMu.Lock()
	fresh := time.Since(a.clipReadAt) < time.Second
	a.clipMu.Unlock()
	if !fresh {
		t.Fatal("clipboard cache was not refreshed")
	}
}

func TestDownloadIsAnAttachment(t *testing.T) {
	a := New("123", "host", "secret", 18095)
	dir := a.InboxDir()
	name := `quote"name.txt`
	if err := os.WriteFile(filepath.Join(dir, name), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(dir, name))
	go func() { _ = a.Serve() }()
	time.Sleep(200 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet,
		"http://127.0.0.1:18095/download?name="+url.QueryEscape(name), nil)
	req.Header.Set("X-Office-Password", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	disposition := resp.Header.Get("Content-Disposition")
	mediaType, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatalf("invalid Content-Disposition %q: %v", disposition, err)
	}
	if mediaType != "attachment" {
		t.Fatalf("download is not an attachment: %q", disposition)
	}
	if params["filename"] != name {
		t.Fatalf("file name was not preserved: %q != %q", params["filename"], name)
	}
}
