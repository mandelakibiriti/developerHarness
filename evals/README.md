# Eval set

`tasks/*.json` are the tasks the self-improvement loop scores harness versions on.
Each one names a `practice-problems/<problem>.json`. **That problem's real test decides
pass or fail.** The harness runs the test itself at `harness improve end`, so an agent
can't claim a pass.

```json
{ "id": "…", "prompt": "what the agent is asked", "problem": "practice id",
  "mode": "build", "level": "optional", "domain": "lessons domain" }
```

## A run

```sh
RUN=$(harness improve begin --task go-api-versioning)    # logs eval-start with harness_version
HARNESS_RUN=$RUN claude -p "…prompt…"                    # hook, gate and cost events carry data.run
harness improve end --run $RUN                           # runs the task's test, logs eval-end
harness improve judge --split train                      # writes runs/<version>/judged.json
harness improve report --base <v> --cand <v> --split test
```

## How a run is scored (100 points)

| Points | Awarded when |
|---|---|
| 40 | the task's test passed |
| 20 | there is no High finding |
| 20 | cost is within `budgets.per_feature_usd` |
| 10 | there are few Plannotator annotations (rework); this reaches 0 at 5 |
| 10 | there are few tool errors; this reaches 0 at 5 |

If setup failed or there is no `eval-end`, the run is **unscored**, not given 0. A broken
eval tells you nothing about the harness.

`split.json` is fixed by `harness improve split --seed N`. Commit it. If you re-split, the
earlier scores can no longer be compared.

## Learning from developer interactions

Everyday use is not evidence on its own. `improve propose` only receives each eval
run's task, score, pass and cost. So a problem seen in daily work has to become a
test, a rule or a lesson before the loop can learn from it.

**After a session or a week**

1. Open the Evals tab, "Signals from everyday use" (failed gates and code runs, stopped
   or failed model calls, reviews that asked for changes), and the Trail tab for detail.
2. For each repeated problem pick one:
   - the agent should have known something → a lesson in `context/memory/lessons/`
   - the agent broke a convention → a rule in `rules/` (give it a `Detect:` line)
   - the harness should be able to prove it → a practice problem plus an eval task
3. A practice problem gets its own folder, `practice-problems/<id>/` (its `cwd`), and must
   **fail before the work is done and pass after**; `go test ./internal/improve` checks the
   first half. Before accepting a run, check the agent did not edit the test.
4. Open a pull request. Tests and the rules they score go in **separate** PRs, so a
   change can't grade itself.

**Sharing across developers.** `.harness/events.jsonl` is local and ignored by git, so
one developer's signals never reach another. Share the distilled result (lesson, rule,
problem, task) through pull requests. Don't commit raw logs or session transcripts: they
hold prompts, paths and possibly secrets.

**Per run, what may change**

| Artifact | Changed by |
|---|---|
| `rules/`, `tracks/`, `context/` (including `context/scripts/`), `.claude/skills/` | proposer, then your approval |
| `context/memory/lessons/` | developers (denied to the proposer) |
| `practice-problems/`, `evals/` | developers only |

Add new tasks to `split.json` by hand. `improve split --force` reshuffles it and makes
earlier scores incomparable.

**Known gaps to close before trusting scores**

- A failed run still scores 60 of 100: security, cost, rework and errors default to full
  marks because nothing records `finding` events, tool failures (no `PostToolUseFailure`
  hook) or the cost of a `claude -p` run. Compare pass rates until those are wired in.
- There are only two tasks, one held out. Aim for at least ten before acting on a score.
- A lesson added for one `domain` changes that task's version hash, which splits versions
  across tasks. Check "Not scored" before comparing.
