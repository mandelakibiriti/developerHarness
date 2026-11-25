# Architecture rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### ARC-1  modular monolith first
Rule:     Start as one deployable with clear internal modules; split into a service only at a proven seam.
Prevents: a service mesh that costs more in ops than it saves, before you know the real boundaries.
Detect:   A plan that names 5 services for a feature that fits in one process; review the "why" for every service.
Teach:    LearnHarness goal "what a modular monolith gets you that a monolith doesn't".

### ARC-2  split only at a proven seam
Rule:     A seam is proven by independent scaling, an independent team, or an independent deploy cadence — not by a hunch.
Prevents: a distributed system split along imagined boundaries that you now have to re-merge or re-split.
Detect:   A service split that shares a database or a synchronous call chain with its sibling.
Teach:    Practice problem "argue, from evidence, whether a given boundary is a real seam".

### ARC-3  dependencies point inward
Rule:     Dependencies flow toward the domain, never outward from it; the core imports nothing from the edges (HTTP, DB, UI).
Prevents: a domain that can't be tested or swapped because it drags in a framework.
Detect:   An import of a handler/DB driver inside the domain package; a cycle in the module graph.
Teach:    LearnHarness goal "why the core should not know HTTP exists".

### ARC-4  contracts are versioned
Rule:     Every contract between modules, services, or clients is versioned and has an explicit owner; changes go through the contract, not around it.
Prevents: a silent schema change that breaks a consumer you didn't know about.
Detect:   A changed struct/field with no version bump; two services talking through an undocumented shared type.
Teach:    LearnHarness goal "why a contract is a promise, not a suggestion".

### ARC-5  one writer per store
Rule:     Each piece of data has exactly one owning service that writes it; everyone else reads through that owner's API.
Prevents: two services writing the same table and corrupting each other's invariants.
Detect:   Two services with write access to the same table or topic; a shared DB between owners.
Teach:    Practice problem "route a write through its owner and delete the direct access".

### ARC-6  async at the boundary, not everywhere
Rule:     Queues and events live between boundaries (where latency or load is proven); within a module, synchronous calls are the default.
Prevents: an event bus inside one process that makes control flow unreadable for no benefit.
Detect:   A message queue used to call a function in the same service; async "for scale" with no load evidence.
Teach:    LearnHarness goal "when async buys you decoupling and when it only buys you confusion".
