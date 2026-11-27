package proxy

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestInjectsIntoHTMLOnly(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out io.Writer = w
		w.Header().Set("ETag", "abc")
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"a":"</head>"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") { // real tools compress
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			out = gz
		}
		io.WriteString(out, "<html><head><title>t</title></head><body>hi</body></html>")
	}))
	defer up.Close()
	h := Handler(func(string) (string, bool) { return up.URL, true }, http.NotFoundHandler(),
		func(sub string) string {
			if sub == "trail" {
				return "<style>x</style>"
			}
			return ""
		})
	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		req.Header.Set("Accept-Encoding", "gzip, br")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	themed := get("trail.localhost:4400", "/")
	want := "<html><head><title>t</title><style>x</style></head><body>hi</body></html>"
	if themed.Body.String() != want || themed.Header().Get("ETag") != "" || themed.Header().Get("Content-Length") != strconv.Itoa(len(want)) {
		t.Fatalf("themed = %q headers %v", themed.Body.String(), themed.Header())
	}
	if got := get("trail.localhost:4400", "/api.json").Body.String(); got != `{"a":"</head>"}` {
		t.Fatalf("JSON altered: %q", got)
	}
	if got := get("map.localhost:4400", "/"); strings.Contains(got.Header().Get("Content-Encoding"), "br") || got.Header().Get("ETag") != "abc" {
		t.Fatalf("other tool altered: %v", got.Header())
	}
}
