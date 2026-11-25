# Practice problems

A practice problem is a small JSON file whose `process` step is a real command
in the repo. Pass = exit 0. Run it with `harness practice <problem.json>`, which
records a `practice` event (pass/fail) in the timeline.

## Schema

```json
{
  "id":      "unique id",
  "skill":   "the rule pack it exercises (e.g. api-design)",
  "goal":    "what the learner must make true",
  "hint":    "one nudging hint, never the answer",
  "cwd":     "optional working directory (default: process cwd)",
  "setup":   ["optional", "one-time", "setup", "command"],
  "process": ["the", "real", "test", "command"],
  "timeout": "optional duration (default 2m)"
}
```

## How it teaches

The `process` step runs the repo's own tests. A problem **fails on a wrong
solution** (the test the learner must make pass is still red) and **passes on a
right one** (the test goes green). The same `process` command is the language
gate from the harness cycle, so practice and gating use one source of truth.

## Examples

- `go-api-versioning.json` — runs `go test ./...` (see `rules/api-design.md`).
- `ts-schema-validation.json` — runs `npm test` (see `rules/typescript.md`).

These examples target this repo's own suite; copy one and point `cwd`/`process`
at the codebase you are learning.
