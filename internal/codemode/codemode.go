// Package codemode runs model-written JavaScript against typed connectors, in
// the style of Cloudflare's Code Mode: instead of one tool call (and one model
// round trip) per operation, the model writes an async arrow function that
// calls connectors, filters, loops, and returns only the final value. The
// intermediate results never re-enter the model's context.
//
// The sandbox is a fresh goja VM per execution. It has the ECMAScript builtins
// and nothing else: no fetch, require, process, timers or filesystem. The only
// way out is a connector global (e.g. events.recent), each backed by a Go
// function the harness controls. Every execution is bounded by a timeout, a
// connector-call cap, a call-stack cap and a result-size cap.
package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Defaults, matching Cloudflare's where they publish one.
const (
	DefaultTimeout   = 30 * time.Second
	DefaultMaxCalls  = 50
	DefaultMaxResult = 24000 // chars, about 6000 tokens at 4 chars/token
	maxLogLines      = 100
	maxStack         = 1024
)

// Param is one named argument of a connector.
type Param struct {
	Name     string
	Type     string // a TypeScript type, e.g. "string", "number", "string[]"
	Optional bool
	Doc      string
}

// Connector is one operation the sandbox can call as <Namespace>.<Name>(args).
type Connector struct {
	Namespace   string
	Name        string
	Description string
	Params      []Param
	Returns     string // TypeScript type of the resolved value
	Fn          func(ctx context.Context, args map[string]any) (any, error)
}

// Path is the connector's call path, e.g. "events.recent".
func (c Connector) Path() string { return c.Namespace + "." + c.Name }

// Decl is the connector's TypeScript declaration.
func (c Connector) Decl() string {
	var b strings.Builder
	fmt.Fprintf(&b, "/** %s */\n", c.Description)
	b.WriteString("declare namespace " + c.Namespace + " {\n  function " + c.Name + "(")
	if len(c.Params) > 0 {
		b.WriteString("args: {")
		for i, p := range c.Params {
			if i > 0 {
				b.WriteString(";")
			}
			opt := ""
			if p.Optional {
				opt = "?"
			}
			fmt.Fprintf(&b, " %s%s: %s", p.Name, opt, p.Type)
			if p.Doc != "" {
				fmt.Fprintf(&b, " /* %s */", p.Doc)
			}
		}
		b.WriteString(" }")
	}
	fmt.Fprintf(&b, "): Promise<%s>;\n}", c.Returns)
	return b.String()
}

// Runtime holds the connectors and limits. It keeps no per-execution state.
type Runtime struct {
	conns      []Connector
	byPath     map[string]Connector
	ScriptsDir string // saved snippets (the harness's "scripts" component)
	Timeout    time.Duration
	MaxCalls   int
	MaxResult  int
}

// New validates conns (unique paths, identifier-safe names) and builds a runtime.
func New(conns []Connector, scriptsDir string) (*Runtime, error) {
	rt := &Runtime{byPath: map[string]Connector{}, ScriptsDir: scriptsDir,
		Timeout: DefaultTimeout, MaxCalls: DefaultMaxCalls, MaxResult: DefaultMaxResult}
	for _, c := range conns {
		if !ident.MatchString(c.Namespace) || !ident.MatchString(c.Name) || c.Namespace == "codemode" || c.Fn == nil {
			return nil, fmt.Errorf("invalid connector %q", c.Path())
		}
		if _, dup := rt.byPath[c.Path()]; dup {
			return nil, fmt.Errorf("duplicate connector %q", c.Path())
		}
		rt.byPath[c.Path()] = c
		rt.conns = append(rt.conns, c)
	}
	return rt, nil
}

var (
	ident       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	snippetName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
)

// Namespaces lists the connector namespaces: all the model sees up front.
func (rt *Runtime) Namespaces() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range rt.conns {
		if !seen[c.Namespace] {
			seen[c.Namespace] = true
			out = append(out, c.Namespace)
		}
	}
	sort.Strings(out)
	return out
}

// Hit is one search result.
type Hit struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

// Search ranks connectors by how many query words appear in their path or
// description. An empty query lists everything.
func (rt *Runtime) Search(query string) []Hit {
	words := strings.Fields(strings.ToLower(query))
	type scored struct {
		Hit
		score int
	}
	var all []scored
	for _, c := range rt.conns {
		hay := strings.ToLower(c.Path() + " " + c.Description)
		s := 0
		for _, w := range words {
			if strings.Contains(hay, w) {
				s++
			}
		}
		if s > 0 || len(words) == 0 {
			all = append(all, scored{Hit{c.Path(), c.Description}, s})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	out := make([]Hit, 0, len(all))
	for _, s := range all {
		out = append(out, s.Hit)
	}
	return out
}

// Describe returns the TypeScript declarations for a path ("events.recent") or
// a whole namespace ("events").
func (rt *Runtime) Describe(target string) (string, error) {
	if c, ok := rt.byPath[target]; ok {
		return c.Decl(), nil
	}
	var decls []string
	for _, c := range rt.conns {
		if c.Namespace == target {
			decls = append(decls, c.Decl())
		}
	}
	if len(decls) == 0 {
		return "", fmt.Errorf("no connector or namespace %q; call codemode.search first", target)
	}
	return strings.Join(decls, "\n\n"), nil
}

// Result is what the model gets back: the same completed/error shapes as
// Cloudflare's runtime. (A "paused" shape arrives with approval-gated
// connectors; none are exposed yet.)
type Result struct {
	Status    string   `json:"status"` // completed | error
	Result    any      `json:"result,omitempty"`
	Error     string   `json:"error,omitempty"`
	Logs      []string `json:"logs,omitempty"`
	Calls     int      `json:"calls"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Execute runs code, which must be an async arrow function expression such as
// `async () => { const e = await events.recent({n: 5}); return e.length }`.
func (rt *Runtime) Execute(ctx context.Context, code string) Result {
	return rt.run(ctx, "("+code+")()")
}

func (rt *Runtime) run(ctx context.Context, src string) Result {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxStack)
	var res Result

	ctx, cancel := context.WithTimeout(ctx, rt.Timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	// shortcut: no memory cap (goja has none); the timeout bounds runaway
	// allocation. Upgrade to a subprocess with RLIMIT_AS if that proves too weak.

	if err := rt.install(ctx, vm, &res); err != nil {
		return Result{Status: "error", Error: err.Error()}
	}
	v, err := vm.RunString(src)
	if err != nil {
		return rt.fail(res, err)
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStatePending:
			return rt.fail(res, errors.New("promise never settled (the sandbox has no timers or I/O to wait on)"))
		case goja.PromiseStateRejected:
			return rt.fail(res, fmt.Errorf("%v", p.Result()))
		}
		v = p.Result()
	}
	out, truncated, err := rt.shrink(v.Export())
	if err != nil {
		return rt.fail(res, err)
	}
	res.Status, res.Result, res.Truncated = "completed", out, truncated
	return res
}

func (rt *Runtime) fail(res Result, err error) Result {
	var intr *goja.InterruptedError
	if errors.As(err, &intr) {
		err = fmt.Errorf("execution stopped: %v (limit %s)", intr.Value(), rt.Timeout)
	}
	res.Status, res.Error = "error", err.Error()
	return res
}

// install exposes the connector namespaces, console and codemode.* to vm.
func (rt *Runtime) install(ctx context.Context, vm *goja.Runtime, res *Result) error {
	spaces := map[string]*goja.Object{}
	for _, c := range rt.conns {
		ns, ok := spaces[c.Namespace]
		if !ok {
			ns = vm.NewObject()
			spaces[c.Namespace] = ns
			if err := vm.Set(c.Namespace, ns); err != nil {
				return err
			}
		}
		c := c
		if err := ns.Set(c.Name, func(call goja.FunctionCall) goja.Value {
			res.Calls++
			if res.Calls > rt.MaxCalls {
				panic(vm.NewGoError(fmt.Errorf("connector call limit (%d) reached", rt.MaxCalls)))
			}
			args, err := argsOf(call.Argument(0))
			if err != nil {
				panic(vm.NewTypeError("%s: %v", c.Path(), err))
			}
			out, err := c.Fn(ctx, args)
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("%s: %w", c.Path(), err)))
			}
			return toJS(vm, out)
		}); err != nil {
			return err
		}
	}

	console := vm.NewObject()
	logf := func(call goja.FunctionCall) goja.Value {
		if len(res.Logs) < maxLogLines {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = a.String()
			}
			res.Logs = append(res.Logs, strings.Join(parts, " "))
		}
		return goja.Undefined()
	}
	for _, k := range []string{"log", "info", "warn", "error"} {
		if err := console.Set(k, logf); err != nil {
			return err
		}
	}
	if err := vm.Set("console", console); err != nil {
		return err
	}

	cm := vm.NewObject()
	_ = cm.Set("search", func(q string) goja.Value { return toJS(vm, rt.Search(q)) })
	_ = cm.Set("describe", func(target string) string {
		d, err := rt.Describe(target)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return d
	})
	_ = cm.Set("run", func(name string, input goja.Value) goja.Value {
		code, err := rt.readSnippet(name)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		fn, err := vm.RunString("(" + code + ")")
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("snippet %s: %w", name, err)))
		}
		call, ok := goja.AssertFunction(fn)
		if !ok {
			panic(vm.NewTypeError("snippet %s is not a function", name))
		}
		v, err := call(goja.Undefined(), input)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("snippet %s: %w", name, err)))
		}
		return v
	})
	return vm.Set("codemode", cm)
}

// argsOf accepts undefined (no args) or a plain object, round-tripped through
// JSON so connectors only ever see plain data.
func argsOf(v goja.Value) (map[string]any, error) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(v.Export())
	if err != nil {
		return nil, fmt.Errorf("arguments must be plain JSON data: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, errors.New("arguments must be a single object, e.g. {n: 5}")
	}
	return m, nil
}

// toJS hands Go data to the VM as plain JSON values, never as Go objects.
func toJS(vm *goja.Runtime, v any) goja.Value {
	b, err := json.Marshal(v)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("connector returned non-JSON data: %w", err)))
	}
	var plain any
	_ = json.Unmarshal(b, &plain)
	return vm.ToValue(plain)
}

// shrink returns v as plain JSON data, or as a truncated JSON string when it
// is over the result cap.
func (rt *Runtime) shrink(v any) (any, bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false, fmt.Errorf("result is not JSON-serialisable: %w", err)
	}
	if len(b) <= rt.MaxResult {
		var plain any
		_ = json.Unmarshal(b, &plain)
		return plain, false, nil
	}
	return string(b[:rt.MaxResult]) + "…", true, nil
}

// SaveSnippet stores code under name so later executions can call
// codemode.run(name, input). Code must be a function expression taking input.
func (rt *Runtime) SaveSnippet(name, code, description string) error {
	if !snippetName.MatchString(name) {
		return fmt.Errorf("invalid snippet name %q (use a-z, 0-9, _ . -)", name)
	}
	if rt.ScriptsDir == "" {
		return errors.New("no scripts directory configured")
	}
	if _, err := goja.Compile(name, "("+code+")", false); err != nil {
		return fmt.Errorf("snippet does not parse: %w", err)
	}
	if err := os.MkdirAll(rt.ScriptsDir, 0o755); err != nil {
		return err
	}
	header := "// " + strings.ReplaceAll(strings.TrimSpace(description), "\n", " ") + "\n"
	return os.WriteFile(filepath.Join(rt.ScriptsDir, name+".js"), []byte(header+strings.TrimSpace(code)+"\n"), 0o644)
}

func (rt *Runtime) readSnippet(name string) (string, error) {
	if !snippetName.MatchString(name) {
		return "", fmt.Errorf("invalid snippet name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(rt.ScriptsDir, name+".js"))
	if err != nil {
		return "", fmt.Errorf("snippet %s: %w", name, err)
	}
	// Drop the leading description comment so "(code)" is an expression.
	s := string(b)
	if strings.HasPrefix(s, "//") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s, nil
}
