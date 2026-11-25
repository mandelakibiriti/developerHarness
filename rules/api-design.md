# API design rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### API-1  resources and status codes (or schema-first GraphQL)
Rule:     REST models resources with nouns and the standard verbs/status codes (201 on create, 404 on miss, 409 on conflict); GraphQL is schema-first with one schema as the contract.
Prevents: an ad-hoc surface where every client guesses the shape and every error is a 500.
Detect:   A route whose name is a verb, or a 200-with-error-body; review against the schema.
Teach:    LearnHarness goal "why 201 vs 200 and 404 vs 400 matter to a client".

### API-2  paginate every collection
Rule:     List endpoints page by default (cursor or offset) and never return an unbounded collection.
Prevents: a response that grows with the data, timing out the client and crushing the DB.
Detect:   A list endpoint with no limit and no cursor; a SELECT without LIMIT.
Teach:    Practice problem "add cursor pagination to an endpoint and keep it stable".

### API-3  public routes are versioned
Rule:     Every route a client outside this repo calls is versioned (`/v1/...`); versions change only with a breaking change and old versions stay until clients migrate.
Prevents: breaking mobile clients you can't force-update.
Detect:   Floorplan endpoint list; rule-gate on plans; route lint in CI.
Teach:    LearnHarness goal "why native clients pin API versions".

### API-4  idempotency keys on writes
Rule:     Mutating endpoints accept an idempotency key so a retried request applies once, not twice.
Prevents: double charges and duplicate records from a client that retried a timed-out request.
Detect:   A POST with a side effect and no key; a retry that would create two resources.
Teach:    Practice problem "make a charge endpoint idempotent under retry".

### API-5  one error envelope
Rule:     Errors return one stable shape (`{ error: { code, message, ... } }`) with a machine-readable code, across every endpoint.
Prevents: every client hand-parsing a different error body, and a buggy parser per format.
Detect:   Two endpoints returning different error shapes; a code that is a free-text string only.
Teach:    LearnHarness goal "why a stable error code beats a stable error message".

### API-6  don't leak internals
Rule:     Responses return a client contract, not the ORM entity or DB row; internal fields are excluded by default.
Prevents: a client depending on a field you meant to keep private, freezing a schema by accident.
Detect:   A handler that marshals the DB model straight to JSON; a DB id leaked as a foreign key.
Teach:    LearnHarness goal "what happens when a client depends on an accidental field".
