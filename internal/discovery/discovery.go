// Package discovery resolves the runtime address of session-shaped tools that
// the harness does not own. Those tools publish their address either in a
// documented file (Floorplan's server.log) or through their own CLI
// (plannotator ... --json).
package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// FromFile returns the first regex match (subgroup 1 when present) in a file.
func FromFile(path, pattern string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad regex %q: %w", pattern, err)
	}
	m := re.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("no match for %q in %s", pattern, path)
	}
	if len(m) > 1 {
		return string(m[1]), nil
	}
	return string(m[0]), nil
}

// FromCommand runs args and returns the string at the dot path in its JSON output.
func FromCommand(args []string, jsonPath string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("empty command")
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("run %s: %w", strings.Join(args, " "), err)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("parse JSON from %s: %w", strings.Join(args, " "), err)
	}
	return AtPath(v, jsonPath)
}

// SessionsFromCommand runs args and collects every "url" string in the result.
func SessionsFromCommand(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", strings.Join(args, " "), err)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("parse JSON from %s: %w", strings.Join(args, " "), err)
	}
	var urls []string
	collectURLs(v, &urls)
	return urls, nil
}

// AtPath navigates a decoded JSON value by a dot path (e.g. "url" or "a.b").
func AtPath(v any, path string) (string, error) {
	cur := v
	for _, key := range strings.Split(path, ".") {
		switch n := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = n[key]
			if !ok {
				return "", fmt.Errorf("path %q: key %q not found", path, key)
			}
		default:
			return "", fmt.Errorf("path %q: cannot descend into %T at %q", path, cur, key)
		}
	}
	switch s := cur.(type) {
	case string:
		return s, nil
	default:
		b, err := json.Marshal(cur)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func collectURLs(v any, out *[]string) {
	switch n := v.(type) {
	case map[string]any:
		for k, val := range n {
			if k == "url" {
				if s, ok := val.(string); ok {
					*out = append(*out, s)
				}
			} else {
				collectURLs(val, out)
			}
		}
	case []any:
		for _, item := range n {
			collectURLs(item, out)
		}
	}
}
