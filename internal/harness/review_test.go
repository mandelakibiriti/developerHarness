package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePlannotator writes a script that checks the flags the real CLI requires
// together (--result-file needs --gate --json) and writes a decision.
func fakePlannotator(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "plannotator")
	os.WriteFile(script, []byte(`#!/bin/sh
args="$*"
case "$args" in *--gate*--json*--result-file*) ;; *) echo "--result-file requires --gate --json" >&2; exit 2;; esac
while [ $# -gt 0 ]; do [ "$1" = "--result-file" ] && out="$2"; shift; done
echo '{"decision":"approved","annotations":[]}' > "$out"
`), 0o755)
	orig := reviewCommand
	reviewCommand = script
	t.Cleanup(func() { reviewCommand = orig })
}

func TestReviewPassesGateFlagsAndRecordsDecision(t *testing.T) {
	fakePlannotator(t)
	h, _ := newTestRuntime(t, t.TempDir())
	serveJSON(h, http.MethodPut, "/api/docs?name=architecture.md", `{"content":"# A"}`)

	if rec := serveJSON(h, http.MethodPost, "/api/review", ""); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/review = %d %s", rec.Code, rec.Body.String())
	}
	var st reviewState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		json.Unmarshal(serveJSON(h, http.MethodGet, "/api/review", "").Body.Bytes(), &st)
		if st.Status != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.Status != "done" || st.Decision != "approved" {
		t.Fatalf("review state = %+v, want done/approved", st)
	}
}

func TestReviewDecision(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{`{"decision":"approved"}`, "approved"},
		{`{"approved":false}`, "changes requested"},
		{`{"feedback":"x"}`, "submitted"},
	} {
		var m map[string]any
		json.NewDecoder(strings.NewReader(tc.in)).Decode(&m)
		if got := reviewDecision(m); got != tc.want {
			t.Errorf("%s -> %q, want %q", tc.in, got, tc.want)
		}
	}
}
