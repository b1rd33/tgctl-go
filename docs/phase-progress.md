# Phase progress ledger

This ledger records the implementation state for the v0.4–v0.6 reliability
roadmap in this worktree. “Complete” means the offline implementation,
contracts, and repository checks are complete; it does not claim live Telegram
acceptance.

| Phase | Status | Evidence | Commit | Remaining gate |
| --- | --- | --- | --- | --- |
| 1. Establish reliable coverage | complete (offline evidence) | Baseline v0.3.1 pinned; one-row current command matrix and repository checks established | `cefcfa3` | Live command matrix and fixture-dependent assertions remain unrun |
| 2. Make account and target selection explicit | implemented (offline evidence) | `self`/`me`, account-bound resolution, known/unknown Premium metadata, app-config limits, stable setup path | `81beb22` | Disposable live account read, Premium comparison, and full edge-case matrix |
| 3. Fetch messages directly from Telegram | implemented (offline evidence) | Telegram source for history/search/get, typed bounded cursors, filters, deleted placeholders, adapter propagation tests | `81beb22` | Redacted live reads, full/empty/truncated pagination, and failure-path matrix |
| 4. Retrieve conversation context correctly | implemented (offline core) | Replies, explicit forum topic history, topic/root-bound cursors, missing/deleted-root rejection, linked discussion lookup without joining, distinct discussion peers, advisory permissions | `81beb22`, `9a79663` | Live forum/admin fixture and nested-topic/reply verification; slow-mode inspection remains unavailable in pinned gotd types |
| Documentation and release hygiene | complete | Generated command docs, skill reference, coverage matrix, public-hygiene checks | `e10f990` | None for this implementation handoff |

No Telegram mutation was run, and no live acceptance gate is claimed as
passed. Phases 5–6 (broader cache semantics and release hardening) remain
follow-up work after live fixtures are available. This worktree does not
install binaries, create tags, publish artifacts, or push branches.
