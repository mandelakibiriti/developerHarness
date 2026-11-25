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
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Resolver maps a subdomain (tool id) to its upstream URL.
type Resolver func(subdomain string) (upstream string, ok bool)

// Handler routes by Host header: the root host serves fallback; a
// <subdomain>.localhost host reverse-proxies to the tool.
func Handler(resolve Resolver, fallback http.Handler) http.Handler {
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
		serveReverse(w, r, upstream)
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

func serveReverse(w http.ResponseWriter, r *http.Request, upstream string) {
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" {
		http.Error(w, "bad upstream", http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		ModifyResponse: sanitize,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("upstream unreachable: %v", err), http.StatusBadGateway)
		},
		FlushInterval: 100 * time.Millisecond,
	}
	proxy.ServeHTTP(w, r)
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
