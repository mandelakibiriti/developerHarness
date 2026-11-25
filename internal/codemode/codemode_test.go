package codemode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/events"
)

func testRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt, err := New([]Connector{
		{Namespace: "math", Name: "double", Description: "Double a number.",
			Params: []Param{{Name: "x", Type: "number"}}, Returns: "number",
			Fn: func(_ context.Context, a map[string]any) (any, error) { return a["x"].(float64) * 2, nil }},
		{Namespace: "math", Name: "boom", Description: "Always fails.", Returns: "never",
			Fn: func(context.Context, map[string]any) (any, error) { return nil, errors.New("kaboom") }},
		{Namespace: "data", Name: "rows", Description: "Many rows of data.", Returns: "number[]",
			Fn: func(context.Context, map[string]any) (any, error) {
				out := make([]int, 10000)
				for i := range out {
					out[i] = i
				}
				return out, nil
			}},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestExecuteReturnsOnlyFinalValue(t *testing.T) {
	rt := testRuntime(t)
	res := rt.Execute(context.Background(), `async () => {
		const rows = await data.rows();              // 10k rows stay in the sandbox
		const d = await math.double({x: rows.length});
		console.log("rows", rows.length);
		return { total: d, evens: rows.filter(n => n % 2 === 0).length };
	}`)
	if res.Status != "completed" {
		t.Fatalf("status %s: %s", res.Status, res.Error)
	}
	m := res.Result.(map[string]any)
	if m["total"] != float64(20000) || m["evens"] != float64(5000) || res.Calls != 2 {
		t.Fatalf("result %+v calls %d", m, res.Calls)
	}
	if len(res.Logs) != 1 || res.Logs[0] != "rows 10000" {
		t.Fatalf("logs %v", res.Logs)
	}
}

func TestSandboxHasNoHostAccess(t *testing.T) {
	rt := testRuntime(t)
	res := rt.Execute(context.Background(), `async () => [typeof fetch, typeof require, typeof process, typeof setTimeout, typeof XMLHttpRequest, typeof Deno].join(",")`)
	if res.Result != "undefined,undefined,undefined,undefined,undefined,undefined" {
		t.Fatalf("host globals leaked: %v (%s)", res.Result, res.Error)
	}
}

func TestLimits(t *testing.T) {
	rt := testRuntime(t)
	rt.Timeout = 200 * time.Millisecond
	start := time.Now()
	if res := rt.Execute(context.Background(), `async () => { while (true) {} }`); res.Status != "error" || !strings.Contains(res.Error, "execution stopped") {
		t.Fatalf("infinite loop: %+v", res)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not interrupt promptly")
	}

	rt.MaxCalls = 3
	res := rt.Execute(context.Background(), `async () => { for (let i = 0; i < 10; i++) await math.double({x: i}); }`)
	if res.Status != "error" || !strings.Contains(res.Error, "call limit") {
		t.Fatalf("call cap: %+v", res)
	}

	if res := rt.Execute(context.Background(), `async () => { const f = () => f(); f(); }`); res.Status != "error" {
		t.Fatalf("unbounded recursion must error, got %+v", res)
	}

	rt.MaxCalls, rt.MaxResult = DefaultMaxCalls, 100
	res = rt.Execute(context.Background(), `async () => await data.rows()`)
	if res.Status != "completed" || !res.Truncated || len(res.Result.(string)) > 104 {
		t.Fatalf("result cap: truncated=%v len=%d", res.Truncated, len(res.Result.(string)))
	}
}

func TestErrorShapes(t *testing.T) {
	rt := testRuntime(t)
	cases := map[string]string{
		`async () => { await math.boom() }`:        "kaboom",
		`async () => { throw new Error("mine") }`:  "mine",
		`async () => await new Promise(() => {})`:  "never settled",
		`async () => await math.double(5)`:         "single object",
		`async () => { return syntax error here }`: "SyntaxError",
	}
	for code, want := range cases {
		res := rt.Execute(context.Background(), code)
		if res.Status != "error" || !strings.Contains(res.Error, want) {
			t.Errorf("%s: got %+v, want error containing %q", code, res, want)
		}
	}
	// A connector error is a normal JS exception the code can handle.
	res := rt.Execute(context.Background(), `async () => { try { await math.boom() } catch (e) { return "caught" } }`)
	if res.Result != "caught" {
		t.Fatalf("connector error not catchable: %+v", res)
	}
}

func TestSearchDescribe(t *testing.T) {
	rt := testRuntime(t)
	if hits := rt.Search("double number"); len(hits) == 0 || hits[0].Path != "math.double" {
		t.Fatalf("search: %+v", hits)
	}
	d, err := rt.Describe("math.double")
	if err != nil || !strings.Contains(d, "function double(args: { x: number }): Promise<number>;") {
		t.Fatalf("describe: %q %v", d, err)
	}
	if d, _ := rt.Describe("math"); !strings.Contains(d, "boom") {
		t.Fatalf("namespace describe: %q", d)
	}
	if _, err := rt.Describe("nope"); err == nil {
		t.Fatal("unknown target must error")
	}
	if got := strings.Join(rt.Namespaces(), ","); got != "data,math" {
		t.Fatalf("namespaces %q", got)
	}
	res := rt.Execute(context.Background(), `async () => codemode.search("double")[0].path`)
	if res.Result != "math.double" {
		t.Fatalf("in-sandbox search: %+v", res)
	}
}

func TestSnippets(t *testing.T) {
	rt := testRuntime(t)
	if err := rt.SaveSnippet("quad", `async (input) => await math.double({x: await math.double({x: input.x})})`, "x*4"); err != nil {
		t.Fatal(err)
	}
	res := rt.Execute(context.Background(), `async () => await codemode.run("quad", {x: 3})`)
	if res.Status != "completed" || res.Result != float64(12) {
		t.Fatalf("snippet run: %+v", res)
	}
	for _, bad := range []string{"../escape", "Upper", ""} {
		if err := rt.SaveSnippet(bad, "async () => 1", ""); err == nil {
			t.Errorf("snippet name %q must be rejected", bad)
		}
	}
	if err := rt.SaveSnippet("broken", "async () => {", ""); err == nil {
		t.Error("unparseable snippet must be rejected")
	}
}

func TestNewValidates(t *testing.T) {
	fn := func(context.Context, map[string]any) (any, error) { return nil, nil }
	for _, c := range [][]Connector{
		{{Namespace: "a", Name: "b", Fn: fn}, {Namespace: "a", Name: "b", Fn: fn}},
		{{Namespace: "codemode", Name: "x", Fn: fn}},
		{{Namespace: "bad-name", Name: "x", Fn: fn}},
		{{Namespace: "a", Name: "x"}},
	} {
		if _, err := New(c, ""); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestHarnessConnectors(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".harness", "events.jsonl")
	log, err := events.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []events.Event{
		{Ev: "gate", Data: map[string]any{"result": "fail"}},
		{Ev: "cost", Data: map[string]any{"amount": 1.5}},
		{Ev: "gate", Data: map[string]any{"result": "pass"}},
	} {
		if err := log.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()
	write(t, root, "practice-problems/ok.json", `{"id":"ok","skill":"go","goal":"passes","process":["true"]}`)
	cfg := &config.Config{Level: "beginner", Budgets: config.Budgets{PerFeatureUSD: 2, AlertAt: []float64{0.8, 1}}}

	rt, err := New(HarnessConnectors(Env{Root: root, Cfg: cfg}), "")
	if err != nil {
		t.Fatal(err)
	}
	res := rt.Execute(context.Background(), `async () => {
		const gates = await events.recent({ev: "gate"});
		const b = await budget.status();
		const p = await practice.run({id: (await practice.list())[0].id});
		const c = await context.compile({mode: "build"});
		return { last: gates[gates.length - 1].data.result, n: gates.length, spent: b.spent,
		         pass: p.pass, tutor: c.system.includes("hint ladder") };
	}`)
	if res.Status != "completed" {
		t.Fatalf("%s", res.Error)
	}
	m := res.Result.(map[string]any)
	if m["last"] != "pass" || m["n"] != float64(2) || m["spent"] != 1.5 || m["pass"] != true || m["tutor"] != true {
		t.Fatalf("got %+v", m)
	}
	if res := rt.Execute(context.Background(), `async () => await practice.run({id: "../../etc/passwd"})`); res.Status != "error" {
		t.Fatalf("path-like problem id must be rejected: %+v", res)
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
