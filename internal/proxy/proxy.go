// Package proxy routes requests by subdomain so each tool keeps its root path:
//
//	http://harness.localhost:4400   -> the shell (fallback handler)
//	http://trail.localhost:4400     -> reverse-proxied to the trail upstream
//
// Because every upstream is loopback-only, the proxy strips the frame-blocking
// headers (X-Frame-Options, CSP frame-ancestors) that would stop a tool from
// rendering inside an iframe. The "open in window" fallback bypasses the proxy
// entirely and keeps each tool's own headers.
package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Injector returns markup to add to a tool's HTML pages ("" for none).
type Injector func(subdomain string) string

// maxInjectBody bounds the HTML held in memory while adding markup.
const maxInjectBody = 8 << 20

// Resolver maps a subdomain (tool id) to its upstream URL.
type Resolver func(subdomain string) (upstream string, ok bool)

// Handler routes by Host header: the root host serves fallback; a
// <subdomain>.localhost host reverse-proxies to the tool.
func Handler(resolve Resolver, fallback http.Handler, inject ...Injector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, isRoot := splitHost(r.Host)
		if isRoot {
			fallback.ServeHTTP(w, r)
			return
		}
		upstream, ok := resolve(sub)
		if !ok {
			http.Error(w, fmt.Sprintf("no tool for subdomain %q", sub), http.StatusNotFound)
			return
		}
		snippet := ""
		if len(inject) > 0 {
			snippet = inject[0](sub)
		}
		serveReverse(w, r, sub, upstream, snippet)
	})
}

// splitHost returns the subdomain and whether the host is the root (no tool).
func splitHost(host string) (sub string, root bool) {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh // handles [::1]:4400; a bare host (no port) is kept as-is
	}
	h = strings.TrimSuffix(h, ".")
	switch h {
	case "localhost", "harness.localhost", "127.0.0.1", "0.0.0.0", "::1", "":
		return "", true
	}
	if strings.HasSuffix(h, ".localhost") {
		return strings.TrimSuffix(h, ".localhost"), false
	}
	return h, false
}

func serveReverse(w http.ResponseWriter, r *http.Request, sub, upstream, snippet string) {
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	pl := &proxyLog{prefix: fmt.Sprintf("proxy %s: %s %s: ", sub, r.Method, r.URL.Path)}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			if snippet != "" {
				// without these the transport hands us plain, complete HTML to edit
				req.Header.Del("Accept-Encoding")
				req.Header.Del("If-None-Match")
				req.Header.Del("If-Modified-Since")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			pl.stream = strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
			if err := sanitize(resp); err != nil {
				return err
			}
			return addToHead(resp, snippet)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("upstream unreachable: %v", err), http.StatusBadGateway)
		},
		FlushInterval: 100 * time.Millisecond,
		ErrorLog:      log.New(pl, "", 0),
	}
	proxy.ServeHTTP(w, r)
}

// proxyLog names the tool and path on ReverseProxy's log lines, and drops them
// for event streams: a tool restarting cuts its SSE streams ("unexpected EOF
// during body copy") and the browser's EventSource simply reconnects.
type proxyLog struct {
	prefix string
	stream bool // set by ModifyResponse, read by the same request's goroutine
}

func (l *proxyLog) Write(p []byte) (int, error) {
	if !l.stream {
		log.Print(l.prefix + strings.TrimPrefix(strings.TrimSpace(string(p)), "httputil: "))
	}
	return len(p), nil
}

// addToHead inserts snippet before </head> of an uncompressed HTML response.
// Anything else (scripts, JSON, event streams) passes through untouched.
func addToHead(resp *http.Response, snippet string) error {
	if snippet == "" || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxInjectBody {
		return fmt.Errorf("HTML page too large to theme")
	}
	resp.Body.Close()
	if i := bytes.Index(body, []byte("</head>")); i >= 0 {
		body = append(body[:i:i], append([]byte(snippet), body[i:]...)...)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Del("ETag")
	return nil
}

// sanitize strips frame-blocking headers. Called only for loopback upstreams.
func sanitize(resp *http.Response) error {
	resp.Header.Del("X-Frame-Options")
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
		resp.Header.Set("Content-Security-Policy", stripDirective(csp, "frame-ancestors"))
	}
	return nil
}

// stripDirective removes a directive (and its value) from a CSP header string.
func stripDirective(csp, name string) string {
	parts := strings.Split(csp, ";")
	out := parts[:0]
	for _, p := range parts {
		if strings.Contains(strings.ToLower(strings.TrimSpace(p)), name) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ";")
}

// SameOrigin blocks cross-site writes (CSRF) to the harness API. A browser
// sends Origin on every cross-origin POST, so a write whose Origin is not the
// shell itself (a loopback host on the harness port) is refused. Requests
// without Origin (curl, the CLI) and safe methods pass through.
func SameOrigin(port string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Port() != port {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		switch u.Hostname() {
		case "harness.localhost", "localhost", "127.0.0.1", "::1":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
		}
	})
}
