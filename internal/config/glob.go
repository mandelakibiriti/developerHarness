package config

import (
	"regexp"
	"strings"
)

// globToRegex compiles a slash-separated path glob into an anchored regexp.
// Supported metacharacters:
//
//	'*'   matches any run of non-slash characters
//	'?'   matches one non-slash character
//	'**'  matches any run of characters including '/'
//	'**/' also matches the empty prefix, so "a/**/b" matches "a/b" and "a/x/y/b"
//
// Everything else is matched literally.
func globToRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// matchGlob reports whether the slash-separated path matches the glob. A glob
// that fails to compile never matches (Validate rejects such globs up front).
func matchGlob(pattern, path string) bool {
	re, err := globToRegex(pattern)
	return err == nil && re.MatchString(path)
}
