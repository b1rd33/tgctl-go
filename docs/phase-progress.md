# Phase progress ledger

This ledger records the implementation state for the v0.4–v0.6 reliability
roadmap, with the remaining phase 5 increment described below. “Complete” means the offline implementation,
contracts, and repository checks are complete; it does not claim live Telegram
acceptance.

| Phase | Status | Evidence | Commit | Remaining gate |
| --- | --- | --- | --- | --- |
| 1. Establish reliable coverage | complete (offline evidence) | Baseline v0.3.1 pinned; one-row current command matrix and repository checks established | `cefcfa3` | Live command matrix and fixture-dependent assertions remain unrun |
| 2. Make account and target selection explicit | implemented (offline evidence) | `self`/`me`, account-bound resolution and isolation, known/unknown Premium metadata, app-config limits, stable setup path, selection/setup/dry-run edge-case tests | `81beb22` | Disposable live account read and Premium comparison; server-side username reassignment and limit drift remain live-only |
| 3. Fetch messages directly from Telegram | implemented (offline evidence) | Telegram source for history/search/get, typed bounded cursors, filters, deleted placeholders, adapter propagation, empty/exact-full/short pagination, overlap termination, RPC/cancellation no-write, and malformed/mismatched cursor tests | `81beb22`, `ba9b0ed`, `564d275` | Redacted live reads and stable-data acceptance against Telegram remain unrun |
| 4. Retrieve conversation context correctly | implemented (offline core) | Replies, explicit forum topic history, topic/root-bound cursors, missing/deleted-topic rejection, linked discussion lookup without joining, distinct discussion peers, advisory permissions with slow-mode metadata | `81beb22`, `9a79663`, `cf1b183`, `ba9b0ed` | Live fixture only for real nested replies/topic pagination, linked-discussion membership denial, and owner/admin/member/restricted server-rights transitions |
| 5. Add everyday account management | implemented (offline; selected live checks) | Archive/mute plus contact add/remove, current-session logout and explicit text/caption entities; durable ledger and media-identity regressions | `e39fbd4`, `b63deec`, `f441896` | Live formatted text/media passed; contact/logout, archive/mute and rights transitions remain fixture-dependent |
| 6. Optional Premium and convenience features | not started (outside current handoff) | Original plan's separate optional proposals: voice transcription, translation, Saved Messages tags, custom emoji reactions, drafts/scheduled messages, and transfer progress | — | Each feature requires its own proposal, capability/privacy tests, and acceptance gate |
| Documentation and release hygiene | complete | Generated command docs, skill reference, coverage matrix, public-hygiene checks | `e10f990` | None for this implementation handoff |

At the original v0.6 handoff, no Telegram notification or archive mutation was run, and no live acceptance gate is claimed as
passed. The archive/unarchive and mute/unmute increments are offline-tested and do not include
the remaining phase 5 operations. Phase 6 remains separate future work. That handoff did not install binaries, create tags, publish artifacts, or push branches.

## Media hash lookup (v0.7.0)

Implemented local `media-hash`, bounded `media-index` and `media-find`.
SHA-256 records live in an optional account-local index and preserve lookup
when a downloaded file is removed. Cached deleted/replaced media is excluded.
The command tests cover pagination, account separation, read-only/write gates,
missing files, truncation and lookup after file removal. Store tests cover
stale snapshots and media identities; hash tests cover known digests, size caps,
symlink/directory rejection and cancellation. No live Telegram calls were made.

Automatic indexing now covers confirmed uploads and fresh downloads, albums
and bounded media backfill. Upload hashes come from the private snapshot used
by the uploader. Representations distinguish original upload, downloaded media
and manually indexed cached files. Skipped files are not treated as verified
fresh downloads. Optional index failures preserve successful transfer outcomes.
Explicit visual indexing and candidate search use bounded JPEG/PNG decoding
and dhash64-v1, with Hamming-distance ranking and truthful scan limits.
Tests cover resize/recompression, invalid inputs, account separation, deleted
media, and lookup after file removal. Automatic transfers continue to use exact
SHA-256 only. No live transfer tests were run for this release.
No legacy conversions or cache version changes were added.

## Remaining phase 5 increment (2026-09-26, unreleased)

Implemented contact add/remove, current-session logout and explicit formatting
for messages, edits and captions. Tests cover write/read-only/confirmation and
replay gates, opt-in phone sharing, typed contact RPCs, logout ambiguity and
cleanup, late session writes, UTF-16 boundaries, plain edits clearing formatting,
cache/live entity metadata and formatting-dependent fingerprints.

Two reliability defects discovered during this work take priority over these
features: archive/mute were missing from the underlying durable RPC ledger;
verified downloads cleared the cached media identity and broke hash lookup.
Both now have regression tests. Verified downloads update identity and path
atomically without overwriting a newer replacement or deleted message.

See [live verification](verification-2026-09-26.md) for the bounded Saved Messages
checks and their limits. Contact mutations and logout remain offline-tested to
preserve the user's live address book and authorization. Optional phase 6 items
remain deferred, not required feature parity.

The roadmap is not fully accepted yet: forum/admin rights transitions and
linked-discussion tests still need disposable fixtures; channel sponsored-message
support remains an unresolved A release-scope gate. The separate customer-service
agent research must establish permitted AI processing scope as well as technical
feasibility. No unattended customer replies are authorized by these CLI tests.

## Durable consumer acknowledgement (2026-09-26, unreleased)

PR #15 was merged as `f441896` after CI and a full local race pass. The merged
binary passed the bounded Saved Messages text workflow and cleanup.

The next priority-A increment adds `listen --once --manual-ack`, `events-list`
and receipt-bound `events-ack`. It closes the gap between successful stdout
output and a consumer committing its own inbox, using the existing outbox.
Replaying a persisted event no longer reapplies old state over newer cached
updates. See [consumer contract and tests](update-consumers.md). This does not
provide multi-consumer subscriptions, a Telegram daemon or customer automation.
Fixture-dependent live gates and the sponsored-message scope question above
remain open; optional phase 6 features remain deferred.

## Expiring update delivery repair (2026-09-26, unreleased)

A regression reproduced old message/edit payloads remaining in the pending
outbox after an expiring update cleared the message cache. The repair purges
pending payloads for the complete peer/message identity in the expiry transaction,
keeps a content-free notice, and prevents stale recovery updates from requeuing
content rejected by a tombstone. No schema conversion or dependency change.

`internal/client/update_privacy_test.go` covers users, basic groups, channels,
media TTL, unchanged unrelated receipts, stale full/short update replay, consumer
restart, malformed unrelated rows and rollback/checkpoint failure barriers.
Expiry mutations are tested with synthetic offline fixtures, not private live
messages. Already delivered/exported copies remain the consumer's responsibility.
