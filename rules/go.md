# Go rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### GO-1  context flows first
Rule:     Every function that does I/O takes `ctx context.Context` as its first argument and passes it down.
Prevents: requests that leak past cancellation and timeouts, and goroutines that outlive their caller.
Detect:   `go vet` plus a review scan: an I/O call (http, sql, io) whose chain has no ctx.
Teach:    LearnHarness goal "why a request should die when the client hangs up".

### GO-2  wrap errors with %w
Rule:     When you return an error from a lower layer, wrap it: `fmt.Errorf("load config: %w", err)`.
Prevents: losing the chain, so `errors.Is` and `errors.As` stop working and the root cause is invisible.
Detect:   Review scan for `return err` that could have context; `golangci-lint` errorlint.
Teach:    Practice problem "make `errors.Is(err, sql.ErrNoRows)` true across three layers".

### GO-3  interfaces at the consumer, not the producer
Rule:     Define the interface where it is used (small, 1–3 methods); implement it implicitly.
Prevents: fat interfaces that bind every implementation and can't be swapped for a fake.
Detect:   A package that exports an interface with 8 methods but only one caller.
Teach:    LearnHarness goal "why io.Reader is one method".

### GO-4  one goroutine owns its channel
Rule:     The goroutine that creates a channel is the one that closes it; signal completion with close, never a sent value.
Prevents: send-on-closed-channel panics and double-close races.
Detect:   `go test -race` under load; review for `close(ch)` outside the writer.
Teach:    Practice problem "make a worker pool that shuts down clean under -race".

### GO-5  -race in the gate
Rule:     `go vet ./... && go test -race ./...` is the language gate; it must pass before a card is green.
Prevents: shipping data races that pass in tests but corrupt production under concurrency.
Detect:   The harness gate command records it: `harness gate --name go-test --result pass`.
Teach:    LearnHarness goal "what the race detector actually detects".

### GO-6  no panic in a library
Rule:     Libraries return errors; `panic` is for programmer bugs in `main` or `init`, not for expected failure.
Prevents: a caller with no way to recover from a dependency's panic.
Detect:   grep for `panic(` outside `_test.go` and `main`.
Teach:    Practice problem "convert a panicking parser into `(T, error)`".

### GO-7  don't discard an error
Rule:     Every error is handled or deliberately, namedly ignored (`_ = f()` with a comment why).
Prevents: silent failures that make the next bug look unrelated.
Detect:   `errcheck`; review for bare `f()` where the result is ignored.
Teach:    LearnHarness goal "why the closest error is the cheapest to fix".

### GO-8  zero value should be usable
Rule:     Design types so their zero value is a valid starting state; hide state behind methods, not exported fields.
Prevents: types that need a constructor to be safe, and callers who forget the constructor.
Detect:   A struct whose zero value panics or misbehaves on first use.
Teach:    Practice problem "make a bytes.Buffer-like type usable without NewX".
