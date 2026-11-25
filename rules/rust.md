# Rust rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### RS-1  Result over panic
Rule:     Functions return `Result`/`Option` for expected failure; `panic!`/`unwrap`/`expect` are for invariants and programmer bugs only.
Prevents: a panic in a library that a caller cannot catch, taking down the whole process.
Detect:   `clippy::unwrap_used` and `clippy::expect_used` in the gate.
Teach:    LearnHarness goal "why Result composes and panic does not".

### RS-2  ownership at the API edge
Rule:     Public functions take `&T` or `&mut T` by default; take `T` (or `impl Into<T>`) only when you consume it.
Prevents: forcing callers to clone at the boundary, and lifetimes that surprise downstream code.
Detect:   Review for a public fn that takes ownership but only reads.
Teach:    Practice problem "change a consuming parameter to a borrow and make it compile".

### RS-3  clippy -D warnings
Rule:     `cargo clippy -- -D warnings` is part of the language gate; no warning survives.
Prevents: an accumulation of cheap warnings that hide the one real bug.
Detect:   The harness gate records it: `harness gate --name rust-clippy --result pass`.
Teach:    LearnHarness goal "which clippy lints map to which bug class".

### RS-4  no unsafe without justification
Rule:     `unsafe` blocks are allowed only with a `// SAFETY:` comment citing the invariant that makes them sound.
Prevents: memory unsafety with no reviewer able to verify it.
Detect:   grep for `unsafe`; review every occurrence against the comment.
Teach:    Practice problem "remove an `unsafe` block by restructuring to safe code".

### RS-5  borrow before clone
Rule:     Reach for `&` and iterator adapters before `.clone()`; clone at the boundary, not in the hot loop.
Prevents: accidental deep copies that make a linear loop quadratic.
Detect:   Review for `.clone()` inside a loop; `clippy::clone_on_copy` and friends.
Teach:    LearnHarness goal "why a borrow is zero-cost and a clone is not".

### RS-6  errors implement std::error::Error
Rule:     Custom error types implement `std::error::Error` + `Display`, and wrap causes with `source()`.
Prevents: a library whose errors can't be matched or displayed by callers.
Detect:   `clippy` plus review for a custom error type missing `Display`.
Teach:    Practice problem "add source() to a custom error and print the chain".
