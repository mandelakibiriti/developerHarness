# Principles (ponytail)

Every rule pack and gate in this harness extends these principles. "Ponytail" =
the least code that works cleanly, everywhere.

1. **Least code that works.** Build the smallest thing that satisfies the real
   requirement. Delete before you add. Every abstraction must pay rent.
2. **One concept per step.** Teach and build one idea at a time. A step that
   needs two ideas is two steps.
3. **Explain why.** A rule without its *Prevents* is cargo-cult. State what
   breaks when you skip it, or drop the rule.
4. **Boundaries before bodies.** Get the edges right first — types, I/O, errors,
   contracts — before filling in the middle.
5. **Prefer boring.** Stdlib and small deps over clever frameworks. Boring code
   is easy to review, upgrade, and delete.
6. **Correct first, fast later.** Don't optimize what you haven't measured.

Rule packs live in `rules/*.md` and share one shape:

```
### ID  Short title
Rule:     what to do
Prevents: what breaks if you skip it
Detect:   how a machine or reviewer catches a violation
Teach:    a LearnHarness goal or practice problem for it
```
