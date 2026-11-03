package host

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestPortProxyKeepsTheAppWorking checks that the allow-listed port proxy
// behaves like a real reverse proxy: relative assets, redirects and cookies
// keep working under the /port/<port>/ prefix.
func TestPortProxyKeepsTheAppWorking(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:18123")
	if err != nil {
		t.Skipf("port 18123 is busy: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("js"))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "1", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	a := New("123", "host", "secret", 18124)
	a.Forwards[18123] = true
	go func() { _ = a.Serve() }()
	time.Sleep(200 * time.Millisecond)

	// The client must not follow the redirect itself: the test inspects the
	// rewritten Location header.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	get := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18124"+path, nil)
		req.Header.Set("X-Office-Password", "secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// A relative asset is served.
	resp := get("/port/18123/app.js")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "js" {
		t.Fatalf("asset proxy failed: %d %q", resp.StatusCode, body)
	}

	// A redirect stays inside the prefix.
	resp = get("/port/18123/login")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("redirect status %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "/port/18123/dashboard") {
		t.Fatalf("redirect was not rewritten: %q", location)
	}

	// A cookie is scoped to the prefix.
	resp = get("/port/18123/cookie")
	resp.Body.Close()
	cookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "Path=/port/18123") {
		t.Fatalf("cookie path was not rewritten: %q", cookie)
	}

	// A port that is not allow-listed is refused.
	resp = get("/port/18125/app.js")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unlisted port status %d", resp.StatusCode)
	}
}
