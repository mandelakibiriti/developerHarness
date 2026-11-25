# TypeScript rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### TS-1  strict mode on
Rule:     `"strict": true` in tsconfig, from day one; never relax it for a file.
Prevents: undefined-null bugs and type drift that a loose config silently permits.
Detect:   `tsc --noEmit` as the language gate; a `// @ts-ignore` or `strict:false` anywhere.
Teach:    LearnHarness goal "what `strict` turns on, one flag at a time".

### TS-2  no `any` at a boundary
Rule:     `any` may not appear in the signature of an exported function, an API call, or a shared type.
Prevents: types that lie, so a change in one place breaks a distant caller at runtime instead of compile time.
Detect:   `eslint` no-explicit-any on boundaries; review for `any` in exports.
Teach:    Practice problem "remove `any` from one module's exports".

### TS-3  validate at the I/O edge
Rule:     Parse untrusted input (fetch responses, form bodies, localStorage, env) with a schema (zod or similar) and use the inferred type inward.
Prevents: trusting the wire or storage, so malformed data crashes deep inside the app.
Detect:   `as` casts or `: unknown`-without-parse at the edge; a response type that was hand-written instead of derived.
Teach:    Practice problem "replace a hand-written response type with a zod schema".

### TS-4  discriminated unions over optional fields
Rule:     Model states as `{ kind: "loading" } | { kind: "ok"; data: T } | { kind: "error"; e }`, not `{ data?: T; error?: E }`.
Prevents: impossible states (data and error both set) and missing exhaustive checks.
Detect:   A type with two optional fields that are mutually exclusive.
Teach:    LearnHarness goal "why a union is cheaper to check than a nullable pair".

### TS-5  explicit return types on public functions
Rule:     Export a function with its return type written, not inferred.
Prevents: an accidental signature change rippling out and the API contract drifting silently.
Detect:   `explicit-function-return-type` lint on exported functions.
Teach:    LearnHarness goal "when inference helps and when it hides".

### TS-6  no ts-ignore without a reason
Rule:     `@ts-ignore` and `@ts-expect-error` need a comment explaining why, and a TODO to remove them.
Prevents: a stack of suppressions that quietly hide the next real error.
Detect:   grep for `ts-ignore` / `ts-expect-error`; count them in review.
Teach:    Practice problem "delete every suppression in a module and fix the real errors".
