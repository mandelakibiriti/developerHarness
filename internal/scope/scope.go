// Package scope validates an offensive tool's scope intent. ARTEX runs real
// attack tools, so the harness refuses anything that could target a third
// party: the allow-list must be loopback-only, and there must be a deny-all
// default. The returned description is printed before every task.
package scope

import (
	"fmt"
	"net"
	"strings"
)

// Loopback reports whether every allow entry is a loopback host.
func Loopback(allow []string) bool {
	if len(allow) == 0 {
		return false
	}
	for _, a := range allow {
		if !isLoopback(a) {
			return false
		}
	}
	return true
}

func isLoopback(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimPrefix(h, "https://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		return true
	}
	if strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// Validate checks the scope is safe and returns a canonical, printable
// description: allow=<hosts> ports=<ports> deny=<deny>.
func Validate(allow []string, ports []int, deny []string) (string, error) {
	if len(allow) == 0 {
		return "", fmt.Errorf("scope.allow is empty")
	}
	if !Loopback(allow) {
		return "", fmt.Errorf("scope.allow must be loopback-only (got %v); an offensive tool must never target a third party", allow)
	}
	portStrs := make([]string, len(ports))
	for i, p := range ports {
		portStrs[i] = fmt.Sprintf("%d", p)
	}
	return fmt.Sprintf("allow=%s ports=%s deny=%s",
		strings.Join(allow, ","), strings.Join(portStrs, ","), strings.Join(deny, ",")), nil
}
