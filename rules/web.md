# Web rule pack

Extends [principles.md](../.claude/skills/go-tutor/principles.md).

### WEB-1  server state stays on the server
Rule:     Data fetched from the server is server state; don't copy it into a client store that can drift out of sync.
Prevents: two sources of truth for the same fact, so a cache or store shows stale data.
Detect:   A fetch response written into Redux/Zustand/Pinia state and read back as if authoritative.
Teach:    LearnHarness goal "server state vs client state, and why they need different tools".

### WEB-2  the URL is the state
Rule:     What a user can share or reload (filters, selected item, page) lives in the URL, not only in component state.
Prevents: a view you can't link, bookmark, or survive a refresh.
Detect:   A filter or tab selection that resets on reload; a deep link that opens the wrong thing.
Teach:    Practice problem "move a filter from useState into the query string".

### WEB-3  accessible from the first commit
Rule:     Interactive elements are real `<button>`/`<a>`/`<input>`, labels are associated, and keyboard focus is visible.
Prevents: shipping something a screen reader or keyboard user cannot use, and an expensive retrofix later.
Detect:   `axe`/lint in CI; a click handler on a `<div>`; an input with no label.
Teach:    LearnHarness goal "what a screen reader does with a div-with-onClick".

### WEB-4  Core Web Vitals budgets
Rule:     Set a budget (LCP, INP, CLS) and check it in CI; don't ship a change that blows past it.
Prevents: a slow-motion regression that drives users away before anyone notices.
Detect:   Lighthouse/Lighthouse CI in the gate; a PR that adds a 2 MB bundle.
Teach:    LearnHarness goal "which of LCP/INP/CLS is about which user complaint".

### WEB-5  no jank
Rule:     Long work runs off the main thread (Web Worker) or in chunks; avoid layout thrash in loops.
Prevents: a UI that stutters under real data, which users feel as "the app is slow".
Detect:   Profiling in the gate; a `while` loop that reads and writes layout properties.
Teach:    Practice problem "move a blocking computation to a worker and measure INP".

### WEB-6  idempotent, typed client calls
Rule:     Every network call from the client has an explicit type and an error path; retries are safe (idempotent) by design.
Prevents: an untyped, unretried call that fails once and leaves the UI in a half-state.
Teach:    LearnHarness goal "why a retried POST must be idempotent".
