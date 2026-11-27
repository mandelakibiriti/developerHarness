package harness

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestEvalsReport(t *testing.T) {
	home := t.TempDir()
	log := ""
	line := func(s string) { log += s + "\n" }
	line(`{"t":"2026-11-01T10:00:00Z","ev":"eval-start","data":{"run":"r1","task":"a","harness_version":"v1"}}`)
	line(`{"t":"2026-11-01T10:05:00Z","ev":"eval-end","data":{"run":"r1","status":"done","pass":false}}`)
	line(`{"t":"2026-11-02T10:00:00Z","ev":"eval-start","data":{"run":"r2","task":"a","harness_version":"v2"}}`)
	line(`{"t":"2026-11-02T10:05:00Z","ev":"eval-end","data":{"run":"r2","status":"done","pass":true}}`)
	line(`{"t":"2026-11-03T10:00:00Z","ev":"eval-start","data":{"run":"r3","task":"a","harness_version":"v2"}}`)
	line(`{"t":"2026-11-03T11:00:00Z","ev":"run","data":{"lang":"go","exit":1}}`)
	line(`{"t":"2026-11-03T11:01:00Z","ev":"run","data":{"lang":"go","exit":0}}`)
	line(`{"t":"2026-11-03T11:02:00Z","ev":"llm-error","data":{"purpose":"chat","cancelled":true}}`)
	line(`{"t":"2026-11-03T11:03:00Z","ev":"gate","data":{"name":"lint","result":"fail","run":"r3"}}`) // inside an eval: not a signal
	os.WriteFile(filepath.Join(home, "events.jsonl"), []byte(log), 0o644)
	h, _ := newTestRuntime(t, home)

	var rep evalReport
	rec := serveJSON(h, http.MethodGet, "/api/evals", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(rep.Runs) != 3 || rep.Runs[0].Outcome != "open" || rep.Runs[1].Outcome != "pass" || rep.Runs[2].Outcome != "fail" {
		t.Fatalf("runs = %+v", rep.Runs)
	}
	if rep.Runs[2].Started != "2026-11-01T10:00:00Z" {
		t.Fatalf("started = %q", rep.Runs[2].Started)
	}
	if len(rep.Signals) != 2 || rep.Counts["failed code run"] != 1 || rep.Counts["stopped by developer"] != 1 || rep.Signals[0].Kind != "stopped by developer" {
		t.Fatalf("signals = %+v counts = %v", rep.Signals, rep.Counts)
	}
}
