package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devex/harness/internal/events"
)

// --- /api/review: Plannotator review of docs/ ---
//
// POST starts `plannotator annotate docs/ --gate --json --result-file …`
// (--result-file requires --gate --json). The process runs until the reviewer
// approves or sends feedback; GET reports running/done and the decision.

var reviewCommand = "plannotator" // overridden in tests

type reviewState struct {
	Status     string         `json:"status"` // none | running | done | failed
	Started    string         `json:"started,omitempty"`
	Finished   string         `json:"finished,omitempty"`
	Decision   string         `json:"decision,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	ResultFile string         `json:"result_file,omitempty"`
}

func (rt *Runtime) handleReview(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rt.reviewMu.Lock()
		st := rt.review
		rt.reviewMu.Unlock()
		if st.Status == "" {
			st.Status = "none"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	case http.MethodPost:
		rt.startReview(w)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (rt *Runtime) startReview(w http.ResponseWriter) {
	if _, err := os.Stat(rt.docsDir()); err != nil {
		http.Error(w, "no docs/ to review yet", http.StatusBadRequest)
		return
	}
	rt.reviewMu.Lock()
	if rt.review.Status == "running" {
		rt.reviewMu.Unlock()
		http.Error(w, "a review is already open in Plannotator", http.StatusConflict)
		return
	}
	resultFile := filepath.Join(rt.home, "review-result.json")
	_ = os.Remove(resultFile) // never report a previous review's decision
	cmd := exec.Command(reviewCommand, "annotate", rt.docsDir()+"/",
		"--gate", "--json", "--result-file", resultFile, "--title", "Architecture spec review")
	stderr := &capWriter{n: 16 << 10}
	cmd.Stdout = os.Stderr // annotate prints its session URL; keep it in the harness log
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	if err := cmd.Start(); err != nil {
		rt.reviewMu.Unlock()
		http.Error(w, "failed to start plannotator: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rt.review = reviewState{Status: "running", Started: time.Now().UTC().Format(time.RFC3339), ResultFile: resultFile}
	st := rt.review
	rt.reviewMu.Unlock()
	rt.log.Append(events.Event{Ev: "review-started", Tool: "plannotator", Data: map[string]any{"result_file": resultFile, "target": rt.docsDir()}})

	go rt.finishReview(cmd, resultFile, stderr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

// finishReview waits for Plannotator (also reaping the process) and records the decision.
func (rt *Runtime) finishReview(cmd *exec.Cmd, resultFile string, stderr *capWriter) {
	waitErr := cmd.Wait()
	st := reviewState{Finished: time.Now().UTC().Format(time.RFC3339), ResultFile: resultFile}
	var result map[string]any
	b, readErr := os.ReadFile(resultFile)
	if readErr == nil {
		readErr = json.Unmarshal(b, &result)
	}
	switch {
	case readErr == nil:
		st.Status, st.Result, st.Decision = "done", result, reviewDecision(result)
	case waitErr != nil:
		st.Status = "failed"
		st.Error = "Plannotator closed without a decision (" + waitErr.Error() + ")"
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 { // usage/startup error: its output says why
			st.Error = strings.TrimSpace(fmt.Sprintf("plannotator failed to start: %s", lastLine(string(stderr.buf))))
		}
	default:
		st.Status, st.Error = "failed", "plannotator exited without a decision"
	}
	rt.reviewMu.Lock()
	st.Started = rt.review.Started
	rt.review = st
	rt.reviewMu.Unlock()
	data := map[string]any{"status": st.Status, "decision": st.Decision}
	if st.Error != "" {
		data["error"] = st.Error
	}
	rt.log.Append(events.Event{Ev: "review-finished", Tool: "plannotator", Data: data})
}

// reviewDecision pulls a human-readable verdict out of Plannotator's JSON.
func reviewDecision(res map[string]any) string {
	for _, k := range []string{"decision", "status", "action"} {
		if s, ok := res[k].(string); ok && s != "" {
			return s
		}
	}
	if b, ok := res["approved"].(bool); ok {
		if b {
			return "approved"
		}
		return "changes requested"
	}
	return "submitted"
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
