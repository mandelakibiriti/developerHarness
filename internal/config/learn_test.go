package config

import "testing"

func TestGlobToRegex(t *testing.T) {
	cases := map[string]string{
		"context/**": "^context/.*$",
		"**/foo":     "^(?:.*/)?foo$",
		"a/**/b":     "^a/(?:.*/)?b$",
		"*.md":       "^[^/]*\\.md$",
		"harness.json": "^harness\\.json$",
	}
	for pat, want := range cases {
		re, err := globToRegex(pat)
		if err != nil {
			t.Fatalf("%s: %v", pat, err)
		}
		if re.String() != want {
			t.Errorf("%s: got %s, want %s", pat, re.String(), want)
		}
	}
}

func TestLearnable(t *testing.T) {
	c := &Config{Schema: SchemaVersion}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Learn.Deny = []string{"context/memory/**"} // lessons are written by adoption, not proposed

	cases := []struct {
		path string
		want bool
	}{
		{"context/system/build.md", true},
		{"context/tools/guidance.md", true},
		{"context/scripts/helper.js", true},
		{"rules/go.md", true},
		{"tracks/beginner.json", true},
		{".claude/skills/go-tutor/SKILL.md", true},
		{"context/memory/lessons/go.jsonl", false}, // deny wins over allow
		{"main.go", false},                         // not in the allow set
		{"harness.json", false},                    // protected
		{".harness/events.jsonl", false},           // protected
		{"evals/tasks/x.json", false},              // protected
		{"practice-problems/go-api-versioning.json", false}, // protected
		{"runs/v1/judged.json", false},             // protected
		{".git/config", false},                     // protected
		{"../outside.md", false},                   // traversal
		{"/etc/passwd", false},                     // absolute
		{"", false},
		{".", false},
	}
	for _, tc := range cases {
		if got := c.Learnable(tc.path); got != tc.want {
			t.Errorf("Learnable(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestLearnableProtectedWinsOverAllow(t *testing.T) {
	c := &Config{Schema: SchemaVersion, Learn: Learn{Allow: []string{"**"}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Learnable("main.go") {
		t.Error("allow-all should make main.go learnable")
	}
	if c.Learnable("harness.json") {
		t.Error("the guardrail file must stay protected even under allow-all")
	}
	if c.Learnable("practice-problems/ts-schema-validation.json") {
		t.Error("the practice problems must stay protected even under allow-all")
	}
}

func TestValidateRejectsBadLearnGlob(t *testing.T) {
	p := writeCfg(t, `{"$schema":"harness@1","profile":"core","listen":"127.0.0.1:4400",
		"learn":{"allow":[""]}}`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for an empty learn glob")
	}
}
