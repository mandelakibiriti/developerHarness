---
name: harness-cycle
description: "Drive the end-to-end build/understand cycle (goal -> learn -> map -> review -> build -> gate -> cost -> secure -> practice). Use when the user gives a build goal or wants to understand an existing repo."
---

# harness-cycle

## When to use
When the user gives a goal ("build X" or "understand this repo") and wants the
full harness cycle. Each step appends an event to `.harness/events.jsonl`, which
the shell's timeline shows in order.

## The cycle

0. **Goal** — `harness up --goal "…"` records the goal event.
1. **Learn** — probe what the user knows; LearnHarness brackets the level and
   plans the minimal path.
2. **Map** — write a PRD, then a Floorplan blueprint (`floorplan/plan.json`).
   For an existing repo, a from-code map comes first.
3. **Review** — plan mode; Plannotator opens the plan; `rule-gate` pre-annotates
   violations with rule ids.
4. **Build** — one card at a time. Beginners write the code (tutor loop);
   intermediate and above, the agent writes and the user reviews every plan/diff.
5. **Gate** — `plannotator review --gate` plus the language gate
   (`go vet && go test -race`, `tsc --noEmit && vitest`, …). Record it:
   `harness gate --name go-test --result pass`.
6. **Cost** — check per-feature cost against budget. Record it:
   `harness cost --amount 0.42 --feature login`.
7. **Secure** — an ARTEX audit scoped to loopback with approval gates on.
   Findings become Plannotator Inbox items, then fix tasks.
8. **Practice** — LearnHarness poses problems from what was taught plus each
   rule broken in steps 3–7. Record it: `harness practice <problem.json>`.

## Done criteria
A feature counts as done when its card is green on the map, its last `gate`
event is PASS, it is within budget, and it has no open High findings.

## Record every step
Use `harness gate`, `harness cost`, and `harness practice` to record evidence.
Never skip recording a gate — promotion is evidence-based, not time-based.
