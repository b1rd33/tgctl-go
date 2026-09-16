# Phase progress ledger

This ledger records the implementation state for the v0.4–v0.6 reliability
roadmap in this worktree. “Complete” means the offline implementation,
contracts, and repository checks are complete; it does not claim live Telegram
acceptance.

| Phase | Status | Evidence | Commit | Remaining gate |
| --- | --- | --- | --- | --- |
| 1. Establish reliable coverage | complete (offline evidence) | Baseline v0.3.1 pinned; one-row current command matrix and repository checks established | `cefcfa3` | Live command matrix and fixture-dependent assertions remain unrun |
| 2. Make account and target selection explicit | implemented (offline evidence) | `self`/`me`, account-bound resolution and isolation, known/unknown Premium metadata, app-config limits, stable setup path, selection/setup/dry-run edge-case tests | `81beb22` | Disposable live account read and Premium comparison; server-side username reassignment and limit drift remain live-only |
| 3. Fetch messages directly from Telegram | implemented (offline evidence) | Telegram source for history/search/get, typed bounded cursors, filters, deleted placeholders, adapter propagation, empty/exact-full/short pagination, overlap termination, RPC/cancellation no-write, and malformed/mismatched cursor tests | `81beb22`, `ba9b0ed`, `564d275` | Redacted live reads and stable-data acceptance against Telegram remain unrun |
| 4. Retrieve conversation context correctly | implemented (offline core) | Replies, explicit forum topic history, topic/root-bound cursors, missing/deleted-topic rejection, linked discussion lookup without joining, distinct discussion peers, advisory permissions with slow-mode metadata | `81beb22`, `9a79663`, `cf1b183`, `ba9b0ed` | Live fixture only for real nested replies/topic pagination, linked-discussion membership denial, and owner/admin/member/restricted server-rights transitions |
| 5. Add everyday account management | implemented (offline second increment) | Archive/unarchive via typed `folders.editPeerFolders`; mute/unmute via per-peer `account.updateNotifySettings` with only `mute_until` present; explicit duration/deadline/forever parsing, account isolation, write/read-only/fuzzy/dry-run gates, durable idempotency, cancellation and committed-persistence failure handling | `e39fbd4`, pending mute commit | Live archive and notification acceptance remain unrun; contact add/remove, formatted text/captions, and logout remain separate increments |
| 6. Optional Premium and convenience features | not started (outside current handoff) | Original plan's separate optional proposals: voice transcription, translation, Saved Messages tags, custom emoji reactions, drafts/scheduled messages, and transfer progress | — | Each feature requires its own proposal, capability/privacy tests, and acceptance gate |
| Documentation and release hygiene | complete | Generated command docs, skill reference, coverage matrix, public-hygiene checks | `e10f990` | None for this implementation handoff |

No Telegram notification or archive mutation was run, and no live acceptance gate is claimed as
passed. The archive/unarchive and mute/unmute increments are offline-tested and do not include
the remaining phase 5 operations. Phase 6 remains separate future work. This worktree does not
install binaries, create tags, publish artifacts, or push branches.
