package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain lets this test binary stand in for the plannotator CLI, which works
// on every platform (a shell-script fake would not run on Windows).
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_PLANNOTATOR") == "1" {
		os.Exit(fakePlannotatorMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakePlannotatorMain checks the flags the real CLI requires together
// (--result-file needs --gate --json) and writes a decision.
func fakePlannotatorMain(args []string) int {
	has, out := map[string]bool{}, ""
	for i, a := range args {
		has[a] = true
		if a == "--result-file" && i+1 < len(args) {
			out = args[i+1]
		}
	}
	if !has["--gate"] || !has["--json"] || out == "" {
		os.Stderr.WriteString("--result-file requires --gate --json\n")
		return 2
	}
	if err := os.WriteFile(out, []byte(`{"decision":"approved","annotations":[]}`), 0o600); err != nil {
		return 1
	}
	return 0
}

func fakePlannotator(t *testing.T) {
	t.Helper()
	t.Setenv("FAKE_PLANNOTATOR", "1")
	orig := reviewCommand
	reviewCommand = os.Args[0]
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
