---
name: rule-gate
description: "Annotate a plan, spec, or diff against rules/*.md with rule ids. Use before approving anything in the harness cycle."
---

# rule-gate

## When to use
Before approving a plan or diff, and whenever a review asks "what rules does
this break?". Runs in the harness Review step (step 3 and step 5 of the cycle).

## What it does
1. Read the rule packs in `rules/*.md` for every language and framework the
   plan or diff touches.
2. Scan the plan/diff for violations.
3. Annotate each violation inline with its rule id and a one-line fix, e.g.
   `API-3: unversioned public route — add /v1/`.

## Output
A list of `ID — violation — suggested fix`, ordered by severity. Only cite ids
that exist in `rules/*.md`; never invent a rule. If no pack covers a language,
say so instead of guessing.

## Rules of the gate
- An annotation is advisory: no tool rejects a plan by itself. The human
  approves, or annotates to send it back.
- Blocking is reserved for what a machine can decide: `go vet && go test -race`,
  `tsc --noEmit && vitest`, and ARTEX High findings.
- Record the decision with `harness gate --name plan-approval --result pass|fail`.
