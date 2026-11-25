# Python rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### PY-1  type hints at the boundary
Rule:     Public functions and every module boundary carry type hints; mypy runs in the gate.
Prevents: a function whose contract exists only in the author's head, so callers pass the wrong shape.
Detect:   `mypy` as the language gate; review for an exported function with no hints.
Teach:    LearnHarness goal "why hints at the edge matter more than in the middle".

### PY-2  no mutable default arguments
Rule:     Defaults are `None` (or an immutable); build the mutable value inside the body.
Prevents: one list/dict shared across every call, accumulating state between callers.
Detect:   `flake8-bugbear` B006; review for `def f(x=[])`.
Teach:    Practice problem "find the shared-default bug and fix it".

### PY-3  ruff in the gate
Rule:     `ruff check .` and `ruff format --check .` run before merge.
Prevents: style and lint drift that turns every review into a formatting argument.
Detect:   The harness gate records it: `harness gate --name python-lint --result pass`.
Teach:    LearnHarness goal "which ruff rules catch real bugs, not just style".

### PY-4  raise specific exceptions
Rule:     Raise a specific exception with a message; never a bare `raise` or `except Exception: pass`.
Prevents: a swallowed error that makes the next failure untraceable, and callers who can't catch meaningfully.
Detect:   grep for `except:` and `except Exception: pass`; review for bare `raise`.
Teach:    Practice problem "turn a swallowed exception into a typed one with context".

### PY-5  dataclasses for records
Rule:     Use `@dataclass` (or `NamedTuple`) for plain records, not ad-hoc dicts or index-tuples.
Prevents: dicts whose keys are typoable strings, so a rename breaks silently at runtime.
Detect:   A dict passed through 3 functions as a "record"; a tuple indexed by position across files.
Teach:    LearnHarness goal "when a dict is data and when it is a mapping".

### PY-6  no mutable class attribute defaults
Rule:     Class-level attributes must be immutable; mutable state belongs in `__init__`.
Prevents: one shared list/dict across every instance of the class.
Detect:   Review for a list/dict assigned directly in a class body.
Teach:    Practice problem "spot the shared state across instances".
