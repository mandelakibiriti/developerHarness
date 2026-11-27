# How the evals work (a plain-language explainer)

This file explains the self-improvement loop for someone who has never seen it
before. The terse operator version lives in [`README.md`](README.md) (this
folder) and the porting ledger from the two research projects lives in
[`../devEximport.md`](../devEximport.md). Read this one first.

---

## The big question

You have an AI agent (the "student") that does coding work. You also have a
**harness** — the bundle of rules, skills, context files, and guardrails that
surrounds the agent and shapes how it behaves (in this repo: `context/`,
`rules/`, `tracks/`, `.claude/skills/`).

Sooner or later you will want to change the harness: add a rule, rewrite a
skill, tune a track. And then the awkward question arrives:

> **Did that change actually make the agent better — or just *feel* better?**

Vibes are not evidence. An eval is a controlled experiment that answers the
question with a number. Two research projects figured out how to do this well:

- **Harness²** (Google Research) — how to *score* a harness version fairly and
  cheaply, and when a new version is allowed to replace an old one.
- **Hermit** (infini-ai-lab) — how to *generate* improvement suggestions
  automatically, without letting the improver cheat.

This repo ported the best parts of both. The rest of this file explains how it
all works, using a school analogy and three schematics.

---

## The cast of characters

| Real thing in the repo | School analogy |
|---|---|
| The harness (`context/`, `rules/`, `tracks/`, `.claude/skills/`) | The student's **study system**: notes, cheat sheets, habits |
| The agent (Claude Code) doing a task | The **student** sitting the exam |
| `evals/tasks/*.json` | The **exam papers**: "make every API route versioned under /v1/" |
| `practice-problems/<problem>.json` | The **teacher's grading machine**: the real test that actually gets run, no matter what the student claims |
| `evals/split.json` | Which exams are **practice** (train) and which are **locked in the drawer** (test) |
| The version hash (12 hex characters) | The **edition number** of the study system, stamped on every exam |
| `.harness/events.jsonl` | The **exam room camera log**: everything that happened, recorded as it happened |
| `harness improve judge` | The **grader**: an unbribable machine that reads only the camera log and the grading machine's verdict |
| `harness improve report` | The **rules for replacing the study system** with a new edition |
| `Proposer` (Hermit's "Evolver") | The **tutor**: reads graded exams and suggests edits to your notes |
| `Learn` block + `ValidateCandidate` (Hermit's "template") | The **list of what the tutor may touch**: your notes, yes. The answer key, **no**. |

Keep this table handy — every section below uses it.

---

## The whole picture at a glance

Three schematics. Read them in order: **A** is one exam, **B** is how an exam
result turns into a change, **C** is the wall that keeps the change honest.

### A. One exam, end to end (the eval process)

```text
  (1)  harness improve begin
        │  writes eval-start: edition hash + run id
        ▼
  (2)  AGENT sits the exam
        │  claude -p "<the task prompt>"
        │
        │  every action ──────────────►  ┌───────────────────────────┐
        │                                │  .harness/events.jsonl    │
        │                                │  the camera log:          │
        │                                │  cost · tool errors ·     │
        │                                │  security · review marks  │
        ▼                                └─────────────┬─────────────┘
  (3)  harness improve end                             │
        │  the harness runs the REAL TEST              │
        │  (go test ./...) and writes eval-end         │
        └──────────────────────────────────────────────┤
                                                       ▼
                                                       (4)  harness improve judge
                                                            a pure function of the log
                                                            → score out of 100
                                                       │
                                                       ▼
                                                       runs/<edition>/judged.<split>.json
```

The agent never reports its own grade. Steps (3) and (4) are done by the
harness, reading only the log and the test command's exit status.

### B. From a score to a new edition (the improvement loop)

```text
        ┌──────────────────────────────────────────────────────┐
        │                 THE IMPROVEMENT LOOP                 │
        └──────────────────────────────────────────────────────┘

  (1) SCORE             (2) SUGGEST           (3) BOUNCE
  ┌───────────┐         ┌───────────┐         ┌───────────┐
  │   JUDGE   │────────►│ PROPOSER  │────────►│  BOUNCER  │
  │ scores    │evidence │  (tutor)  │candidate│ learnable │
  │ every run │  trail  │           │ new text│ file?     │
  └─────▲─────┘         └───────────┘         └─────┬─────┘
        │                                           │ yes
        │                                           ▼
        │                                           (4)  NEW EDITION
        │                                                (candidate harness)
        │                                           │
        │                                           │ sits the SAME exams
        │                                           ▼
        │                                           (5)  ADOPTION RULE
        │                                                Δ ≥ 1.0 · no regressions
        │                                                cost ≤ +10% · same coverage
        │                                           │ "proposable"
        │                                           ▼
        │                                           (6)  HUMAN APPROVES
        │                                                (Plannotator diff review)
        │                                           │ merged
        └───────────────────────────────────────────┘
          the approved edition becomes the new base
```

Step (2) is the *only* step that creates ideas. Everything after it is
filtering: the bouncer rejects illegal edits, the exams reject changes that
don't actually work, the adoption rule rejects changes that aren't clearly
better, and a human rejects anything they don't want merged. The arrow along
the bottom is the whole point: the approved edition becomes the new base, so
the next round of scores is measured against a harness that is already better
than the one before it.

### C. The trust boundary (why the loop can't cheat)

```text
   ┌───────────────────────────────┐   ┌───────────────────────────────┐
   │  MUTABLE — the scaffolding    │   │  FROZEN — the instruments     │
   │  the loop MAY edit            │   │  the loop may NEVER edit      │
   ├───────────────────────────────┤   ├───────────────────────────────┤
   │  context/**                   │   │  harness.json   (guardrails)  │
   │  rules/**                     │   │  .harness/**    (camera log)  │
   │  tracks/**                    │   │  evals/**       (exams)       │
   │  .claude/skills/**            │   │  practice-problems/** (key)   │
   │                               │   │  runs/**        (gradebook)   │
   │   ▲ improved by the loop      │   │  .git/**        (rollback)    │
   └───┼───────────────────────────┘   └───────────────┬───────────────┘
       │                                               │
       │                  measured by                  │
       └──────────────────  THE LOOP  ─────────────────┘
```

The loop rewrites the notes. It can never rewrite the exam, the answer key, or
the gradebook.

**That is the whole trick of "self-improving."** The left box changes; the
right box is what decides whether the change was good. Because the two boxes
are separate — and the right one is frozen in Go code — a candidate cannot
win by weakening its own test.

---

## What an exam looks like

An eval task is a small JSON file in `evals/tasks/`:

```json
{
  "id": "go-api-versioning",
  "prompt": "Make every route that a client outside this repo calls versioned under /v1/, without breaking existing tests. Explain each route you move.",
  "problem": "go-api-versioning",
  "mode": "build",
  "domain": "go"
}
```

- `prompt` — what the agent (student) is asked to do.
- `problem` — the name of the **practice problem** that decides pass/fail.
- `mode`, `level`, `domain` — which flavor of the study system the student
  gets for this exam.

The practice problem is the important half. It lives in
`practice-problems/`:

```json
{
  "id": "go-api-versioning",
  "skill": "api-design",
  "goal": "Every route a client outside this repo calls is versioned under /v1/.",
  "hint": "Check the router registrations. A route shared with mobile clients must start with /v1/ ...",
  "cwd": ".",
  "process": ["go", "test", "./..."]
}
```

That `process` line is the whole trick: **the real test is a command the
harness runs itself.** The agent never gets to say "trust me, I think I got it
right." The grading machine decides.

---

## One run, step by step

A "run" is one student sitting one exam with one edition of the study system.

### 1. Open the exam — `harness improve begin`

```sh
RUN=$(harness improve begin --task go-api-versioning)
```

This compiles the current harness bundle and stamps the exam with its
**edition number** — a 12-character hash of every context, rule, track, and
skill file. It writes an `eval-start` event to the camera log and prints a run
ID. That ID is how every later event gets attributed to this exam.

### 2. The student sits the exam

```sh
HARNESS_RUN=$RUN claude -p "…the task prompt…"
```

While the agent works, the harness records everything into the camera log
(`.harness/events.jsonl`), each event tagged with the run ID: how much money
was spent, which tools errored, what security findings turned up, how many
review annotations (red-pen marks) the work needed.

### 3. Grade the real test — `harness improve end`

```sh
harness improve end --run $RUN
```

The **harness itself** runs the practice problem's `process` — here,
`go test ./...`. Whatever that command prints, pass or fail, is the only pass
the grader will ever trust. The result is written as an `eval-end` event.

### 4. Score it — `harness improve judge`

```sh
harness improve judge --split train
```

The grader reads the camera log and hands out points (next section). Results
are archived under `runs/<edition-number>/judged.<split>.json`.

### 5. Decide — `harness improve report`

```sh
harness improve report --base <old-version> --cand <new-version> --split test
```

Applies the adoption rule (below) and says whether the new edition of the
study system may replace the old one.

---

## How an exam is scored (100 points)

| Points | Awarded when | Why it matters |
|---|---|---|
| **40** | The task's real test passed | Did the work actually work? |
| **20** | No High-severity security findings | Right answer, no cheating |
| **20** | Cost stayed within `budgets.per_feature_usd` (currently $2) | Right answer, without burning money |
| **10** | Few Plannotator review annotations (rework) | Needed few red-pen corrections — 0 points at 5 annotations |
| **10** | Few tool errors | Didn't fumble the equipment — 0 points at 5 errors |

Notice that "did the right thing" is less than half the grade. A harness that
passes tests but goes bankrupt doing it, or leaves security holes, is not a
better harness. **The score measures the study system, not just the student** —
because the same student runs every exam, the only variable is the harness.

---

## Rule 1: a broken exam is *voided*, not failed

Suppose the grading machine was out of order, or the exam never officially
ended (the process crashed, the room caught fire). The grader does **not** give
that run a zero. It marks it **unscored, with a reason**:

- no `eval-end` event → "run crashed or still running"
- status is `"error"` → "eval error: <what broke>"
- the start event was missing the edition number → unscored too

This is a direct lesson from Harness²: *a broken eval tells you nothing about
the harness.* Counting a crashed exam as a zero would punish a good harness
for bad infrastructure luck.

One more guardrail: the grader is not an AI. It is a **pure function** — same
camera log in, same scores out, every time. Harness² ships an AI judge-model;
this repo deliberately declined it (see `devEximport.md`, point 3). You can't
bribe a rubric as easily as you can sweet-talk a model.

---

## Rule 2: you cannot grade your own homework

This is Hermit's core insight, and the most important paragraph in this file.

The improve loop is allowed to **edit the study system** — that's the whole
point. But if it could also edit the exams, the answer key, or the gradebook,
it could give itself a perfect score and the whole experiment becomes theater.

So there is a manifest (`internal/config/learn.go`) with two lists:

**Learnable by default** (what the tutor may edit):

- `context/**` — system prompts, tool guidance, lessons
- `rules/**` — rule packs
- `tracks/**` — level definitions
- `.claude/skills/**` — skills

**Protected forever** — never learnable, no matter what any config file says:

- `harness.json` — the guardrail file itself
- `.harness/**` — the camera log the grader reads
- `evals/**` — the task set and the split
- `practice-problems/**` — the real tests
- `runs/**` — the archived scores
- `.git/**` — the history that rollback depends on

The protected list lives in Go code, not in `harness.json`, so it cannot be
widened by editing the config. A candidate that tries to touch any of these is
rejected by `ValidateCandidate` — the bouncer — before it can even be tried.

**That is what "scaffolding improvement" means here:** the system improves its
own scaffolding (notes, rules, skills), but the things it is measured by sit
permanently outside its reach.

---

## When does a new edition win?

`harness improve report` compares old vs. new and applies strict rules. The
new edition must:

1. **Beat the old one by at least 1.0 point** on the mean score.
2. **Never turn a passed exam into a failed one.** One regression vetoes it.
3. **Cost no more than +10% per task.** A smarter harness that costs way more
   is not an upgrade.
4. **Sit every exam the old edition sat.** If the old edition was graded on a
   task, the new one must be too — a candidate cannot win by skipping the
   exams it would lose.

Even when all four rules pass, the verdict is only *"proposable."* A **human
still approves the actual diff** in Plannotator. The loop proposes; people
decide.

---

## Why there are two piles of exams (train / test)

`harness improve split --seed 42` shuffles the task IDs using a fixed formula
(sort by `sha256(seed:id)`) and holds about 30% out as **test**, locked in the
drawer:

```json
{ "seed": 42,
  "train": ["go-api-versioning"],
  "test":  ["ts-schema-validation"] }
```

- **Train** exams are the ones you iterate against while improving the harness.
- **Test** exams are the ones you open *once*, to confirm a candidate really
  improved and didn't just memorize the practice pile.

Because the shuffle is computed from the seed rather than stored by hand, the
same seed always produces the same split. Add a new task and the old tasks keep
their piles. **Commit `split.json` and don't re-split casually** — every score
already recorded is only comparable within the split it was scored on.

---

## The cheap pre-check: don't pay for a broken exam

Before spending money on a probe run, `harness improve check` proves the new
edition is even structurally sound — **without calling any AI model and
without writing anything**:

1. The harness bundle **compiles, twice, to the same edition number** (if two
   compiles disagree, something in the context files is nondeterministic).
2. Every eval task parses and loads.
3. The split file loads.
4. **Every task's practice problem actually exists** — so a probe can never
   come back unscored just because the test file was missing.
5. `go vet` and `go test -race ./...` pass (skippable with `--skip-tests`).

A candidate that would fail dies here, for free, instead of after an expensive
exam. This is Harness²'s "spec-check before eval," and it also absorbs their
`--dry-run` idea: the check *is* the dry run.

---

## The tutor: where suggestions come from

Everything above is the **grading half**. The **suggesting half** is the
`Proposer` interface (`internal/improve/proposer.go`) — Hermit calls this role
the "Evolver":

```go
type Proposer interface {
    Name() string
    Propose(ctx, ProposeRequest) ([]Candidate, error)
}
```

A Proposer receives the scored runs (each with its grade **and** a pointer to
the exam's trajectory, so it can point at the exact failure) and returns
**candidates** — inert suggestions like:

> "Edit `rules/api-design.md` because runs p-1 and p-3 lost 40 points
> on the gate for unversioned routes."

Three things keep the tutor honest:

1. **Candidates are just paper.** Proposing writes nothing to disk.
2. **The bouncer checks every candidate** against the learnable manifest
   before it can be applied or probed. Attempting to edit a protected file is
   rejected, full stop.
3. **Even a validated candidate only gets *probed*** — it becomes a new
   edition, sits the exams, and must win under the adoption rules above.

Status check (from `devEximport.md`): the interface and the bouncer are built
and tested, but no concrete Proposer is wired up yet — today the suggestions
come from humans, and the grading machinery validates them. That's the missing
bridge to a fully closed loop.

---

## Glossary

| Term | Plain meaning |
|---|---|
| **Task** | One exam: a prompt plus a pointer to the real test |
| **Practice problem** | The real test (a command) that decides pass/fail |
| **Split** | The fixed train/test division of tasks |
| **Run** | One agent attempt at one task, bracketed by `eval-start` / `eval-end` |
| **Version / edition** | The 12-hex hash of the compiled harness bundle |
| **Scored run** | A run with a complete exam; got points out of 100 |
| **Unscored run** | A voided exam (broke or never ended); carries a reason, never a zero |
| **Gate** | The 40-point component: did the real test pass |
| **Adoption rule** | Δ ≥ 1 point, no regressions, cost +10% cap, same exam coverage |
| **Proposer** | The tutor: turns graded evidence into suggested harness edits |
| **Candidate** | One suggested edit (file + full new content + why); inert until validated |
| **Learnable set** | The whitelist of files the loop may edit |
| **Protected set** | Files the loop may never edit: guardrails, log, evals, tests, scores, git |

---

## Cheat sheet

```sh
harness improve split  --seed 42          # shuffle once, commit split.json
harness improve check                     # free structural pre-check
RUN=$(harness improve begin --task X)     # open exam, get run id
HARNESS_RUN=$RUN claude -p "…"            # student sits the exam
harness improve end --run $RUN            # run the real test, log eval-end
harness improve judge --split train       # score every edition in the log
harness improve report --base A --cand B --split test   # apply adoption rule
```

Further reading: [`README.md`](README.md) for the operator view,
[`../devEximport.md`](../devEximport.md) for exactly which ideas were ported
from Harness² and Hermit (and which were deliberately declined), and the source
in `internal/improve/` (`tasks.go`, `judge.go`, `proposer.go`, `check.go`).
