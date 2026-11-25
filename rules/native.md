# Native rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### NAT-1  offline-first data
Rule:     The app works with no network: data is persisted locally and reads are served from local storage, syncing in the background.
Prevents: an app that shows a blank screen the moment the user enters a tunnel or elevator.
Detect:   A screen whose first render awaits a network call with no local fallback.
Teach:    LearnHarness goal "why mobile assumes a flaky network and the web didn't".

### NAT-2  versioned APIs
Rule:     Every endpoint a released client calls is versioned (`/v1/...`); don't change or remove a field an old client reads.
Prevents: breaking users who can't or won't update the app.
Detect:   An endpoint change with no version bump; a field removed while old clients still ship.
Teach:    Practice problem "add /v2 without breaking /v1 clients".

### NAT-3  platform idioms over lowest common denominator
Rule:     Write native (SwiftUI/Compose) or platform-faithful (React Native/Flutter) UI; follow the platform's navigation, back, and lifecycle conventions.
Prevents: an app that feels alien on its own platform and fights the OS at every turn.
Detect:   A custom gesture or nav pattern that reimplements a platform affordance.
Teach:    LearnHarness goal "why back should work like every other app on the device".

### NAT-4  secure storage for secrets
Rule:     Tokens and keys go in the Keychain/Keystore, never in UserDefaults/SharedPreferences/plain files or logs.
Prevents: credentials extractable from a backup or a log, which is a silent account takeover.
Detect:   grep for a token written to a plain store or logged; review storage of auth state.
Teach:    LearnHarness goal "what the Keychain/Keystore actually protects against".

### NAT-5  background work is declared and batched
Rule:     Background work uses the platform scheduler (WorkManager/BGTaskScheduler), is batched, and is resumable.
Prevents: battery drain and data loss from work the OS kills mid-flight.
Detect:   A timer or infinite loop doing work; a sync that restarts from zero after interruption.
Teach:    Practice problem "make a sync resumable after the OS kills it".

### NAT-6  no secrets in the binary
Rule:     Build-time secrets live in CI/secret managers, never baked into the app binary or repo.
Prevents: a static string an attacker extracts in minutes, rotating into a full breach.
Detect:   grep the repo for API keys and signing material; scan the binary in the gate.
Teach:    LearnHarness goal "how long it takes to extract a string from a shipped app".
