package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitHost(t *testing.T) {
	cases := []struct {
		host string
		sub  string
		root bool
	}{
		{"harness.localhost:4400", "", true},
		{"localhost:4400", "", true},
		{"127.0.0.1:4400", "", true},
		{"trail.localhost:4400", "trail", false},
		{"map.localhost", "map", false},
		{"[::1]:4400", "", true},
	}
	for _, c := range cases {
		sub, root := splitHost(c.host)
		if sub != c.sub || root != c.root {
			t.Errorf("splitHost(%q) = %q,%v; want %q,%v", c.host, sub, root, c.sub, c.root)
		}
	}
}

func TestReverseProxyStripsFrameHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; script-src 'self'")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	h := Handler(func(sub string) (string, bool) {
		if sub == "trail" {
			return upstream.URL, true
		}
		return "", false
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fallback", 404)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "trail.localhost:4400"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("X-Frame-Options not stripped: %q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "frame-ancestors") {
		t.Fatalf("frame-ancestors not stripped: %q", csp)
	}
	if !strings.Contains(csp, "default-src") {
		t.Fatalf("other directives lost: %q", csp)
	}
}

func TestRootFallsBack(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	h := Handler(func(s string) (string, bool) { return "", false }, fallback)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "harness.localhost:4400"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestUnknownSubdomain404(t *testing.T) {
	h := Handler(func(s string) (string, bool) { return "", false },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "nope.localhost:4400"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestSameOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := SameOrigin("4400", ok)
	cases := []struct {
		method, origin string
		want           int
	}{
		{"POST", "", 200}, // CLI / curl
		{"POST", "http://harness.localhost:4400", 200}, // the shell
		{"POST", "http://127.0.0.1:4400", 200},
		{"POST", "http://[::1]:4400", 200},
		{"POST", "http://evil.test", 403},
		{"POST", "http://127.0.0.1:8080", 403},      // app under test on another loopback port
		{"PUT", "http://trail.localhost:4400", 403}, // a proxied tool page
		{"POST", "null", 403},                       // sandboxed iframe / file://
		{"GET", "http://evil.test", 200},            // reads are not writes
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/api/chat", strings.NewReader("{}"))
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s Origin=%q: got %d, want %d", c.method, c.origin, rec.Code, c.want)
		}
	}
}
