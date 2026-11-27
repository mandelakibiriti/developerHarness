// Package scrub removes credentials from the environment before the harness
// spawns an untrusted subprocess — the eval test (the judge) today, the
// improver and acting agent in the V4 loop — so none of them inherits the
// developer's API keys, cloud tokens, or host credentials. This is harness2's
// SCRUB_ENV.
package scrub

import (
	"os"
	"strings"
)

// Sensitive is the list of environment variable name patterns that are
// stripped. Matching is case-insensitive; '*' matches any run of characters.
// It is code, not config: a guardrail must not be loosenable from harness.json.
var Sensitive = []string{
	// credential-shaped suffixes — the bulk of tokens and keys
	"*_api_key", "*_apikey", "*_api_token", "*_token", "*_secret",
	"*_secret_key", "*_password", "*_passwd", "*_credentials", "*_credential",
	"*_private_key", "*_access_key", "*_auth", "*_authorization",
	// model providers and their SDKs
	"anthropic_api_key", "openai_*", "gemini_api_key", "cohere_*",
	"mistral_*", "groq_*", "deepseek_*", "vertex_*", "bedrock_*",
	// clouds
	"aws_access_key_id", "aws_secret_access_key", "aws_session_token",
	"google_application_credentials", "gcp_*", "azure_*", "az_*",
	// source hosts and package registries
	"github_token", "gh_token", "gitlab_token", "npm_token", "npm_config_token",
	"hf_token", "huggingface_token", "pypi_token",
	// agent / CLI session credentials
	"claude_*", "codex_*",
	// ssh agent forwarding
	"ssh_*",
}

// Filter returns environ with every variable whose name matches Sensitive
// removed. A nil environ is treated as os.Environ().
func Filter(environ []string) []string {
	if environ == nil {
		environ = os.Environ()
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !sensitive(name) {
			out = append(out, kv)
		}
	}
	return out
}

// Environ returns a scrubbed copy of the current environment.
func Environ() []string {
	return Filter(nil)
}

func sensitive(name string) bool {
	for _, p := range Sensitive {
		if matchCI(p, name) {
			return true
		}
	}
	return false
}

// matchCI is a case-insensitive glob over environment variable names: '*' is
// the only metacharacter (it matches any run of characters, including none).
func matchCI(pattern, name string) bool {
	return globMatch(strings.ToLower(pattern), strings.ToLower(name))
}

func globMatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			for len(pattern) > 0 && pattern[0] == '*' {
				pattern = pattern[1:]
			}
			if pattern == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(pattern, s[i:]) {
					return true
				}
			}
			return false
		default:
			if len(s) == 0 || s[0] != pattern[0] {
				return false
			}
			s, pattern = s[1:], pattern[1:]
		}
	}
	return len(s) == 0
}
