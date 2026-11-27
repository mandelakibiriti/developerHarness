package scrub

import (
	"strings"
	"testing"
)

func TestFilterStripsSensitive(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"LANG=en_US.UTF-8",
		"HARNESS_RUN=r1",
		"ANTHROPIC_API_KEY=sk-123",
		"OPENAI_API_KEY=sk-456",
		"GITHUB_TOKEN=ghp_abc",
		"npm_config_token=secret",
		"AWS_SECRET_ACCESS_KEY=wJalr",
		"CLAUDE_CODE_API_KEY=cck",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
	}
	got := Filter(env)
	keep := map[string]bool{"PATH": true, "HOME": true, "LANG": true, "HARNESS_RUN": true}
	seen := map[string]bool{}
	for _, kv := range got {
		name, _, _ := strings.Cut(kv, "=")
		if !keep[name] {
			t.Errorf("leaked %q", kv)
		}
		seen[name] = true
	}
	for _, name := range []string{"PATH", "HOME", "LANG", "HARNESS_RUN"} {
		if !seen[name] {
			t.Errorf("benign variable %q was stripped", name)
		}
	}
}

func TestEnvironScrubsCurrent(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("HARNESS_RUN", "keepme")
	got := strings.Join(Environ(), "\n")
	if strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Error("Environ leaked ANTHROPIC_API_KEY")
	}
	if !strings.Contains(got, "HARNESS_RUN") {
		t.Error("Environ stripped HARNESS_RUN")
	}
}

func TestMatchCI(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*_token", "GITHUB_TOKEN", true},
		{"*_token", "github_token", true},
		{"*_token", "PATH", false},
		{"*_api_key", "anthropic_api_key", true},
		{"openai_*", "OPENAI_API_KEY", true},
		{"claude_*", "CLAUDE_PROJECTS_DIR", true},
		{"*_secret", "OAUTH_CLIENT_SECRET", true},
		{"*_secret", "SECRET", false}, // must end in _secret
		{"*_auth", "DOCKER_AUTH", true},
	}
	for _, c := range cases {
		if got := matchCI(c.pattern, c.name); got != c.want {
			t.Errorf("matchCI(%q,%q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
