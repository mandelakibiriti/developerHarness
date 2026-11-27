package improve

import (
	"testing"

	"github.com/devex/harness/internal/config"
)

func newCfg(t *testing.T) *config.Config {
	t.Helper()
	c := &config.Config{Schema: config.SchemaVersion}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestValidateCandidate(t *testing.T) {
	cfg := newCfg(t)
	cases := []struct {
		name string
		c    Candidate
		want bool // want == nil error
	}{
		{"learnable", Candidate{ID: "p1", File: "context/system/build.md", Content: "x"}, true},
		{"new learnable file", Candidate{ID: "p2", File: "rules/python.md", Content: "# pack"}, true},
		{"guardrail file", Candidate{ID: "p3", File: "harness.json", Content: "{}"}, false},
		{"the test", Candidate{ID: "p4", File: "practice-problems/go-api-versioning.json", Content: "{}"}, false},
		{"eval set", Candidate{ID: "p5", File: "evals/tasks/x.json", Content: "{}"}, false},
		{"judge log", Candidate{ID: "p6", File: ".harness/events.jsonl", Content: "{}"}, false},
		{"traversal", Candidate{ID: "p7", File: "../outside.md", Content: "x"}, false},
		{"absolute", Candidate{ID: "p8", File: "/etc/passwd", Content: "x"}, false},
		{"non-learnable", Candidate{ID: "p9", File: "main.go", Content: "x"}, false},
		{"empty content", Candidate{ID: "p10", File: "rules/go.md", Content: "  "}, false},
		{"no id", Candidate{File: "rules/go.md", Content: "x"}, false},
	}
	for _, tc := range cases {
		err := ValidateCandidate(cfg, tc.c)
		if (err == nil) != tc.want {
			t.Errorf("%s: err = %v, want nil=%v", tc.name, err, tc.want)
		}
	}
}
