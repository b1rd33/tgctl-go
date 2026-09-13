# Execution plan after v0.3.1

Status: proposed, not implementation approval. Baseline: v0.3.1, commit 8c42d8b. Version targets below are tentative and depend on acceptance gates. No legacy migrations, compatibility aliases, or feature-parity rewrite.

## 1. Establish reliable coverage before adding features

Priority A. Estimated complexity: medium. Deliverable: one row per current Cobra command, with separate columns for offline tests, live execution, behavioral assertions, tested binary/commit, cleanup verification, and remaining limitations. Include relevant flag combinations and failure paths; merely opening help or receiving exit 0 does not prove correctness. Every row must be passed, failed, unsupported in the chosen fixture, or not tested. Existing claims from the other task must be checked against actual evidence.

Use temporary databases and fake/TL invokers for destructive failures, account removal, session revocation, moderation, quota exhaustion and ambiguous writes. Keep raw private results outside the repository; publish redacted evidence only. Read-based smoke tests and controlled Saved Messages operations are distinct coverage categories. Do not infer new live-write authorization from this plan.

Required checks:
- Text: create/reply/edit/forward/reaction/delete, confirmed resulting state, stable idempotent replay, changed-request rejection, and cleanup of test-created messages only.
- Media: valid photo, document, video and voice fixtures; photo/video, audio and document albums; order/caption/grouping, downloaded bytes or appropriate media validation, cancellation and partial failure. No multi-gigabyte live transfers merely to test boundary arithmetic.
- History: pagination overlaps/timestamp ties, empty pages, deleted roots, edit freshness, absent cache coverage, account isolation and unread/read-marker behavior.
- Updates: controlled incoming event, persisted outbox acknowledgment, restart replay, simulated network interruption and gap recovery, failed persistence, SIGINT and released ownership. Inject dangerous faults offline. Do not weaken exclusive session ownership to generate events; a user action from an official client can provide a controlled event.
- Local operations: consistent backup with WAL, restore into empty isolated state, restore rejection for occupied destinations, archives/manifests and missing/changed/extra media.
- Safety: zero-network dry run, write/confirmation gates, secret-free output, invalid IDs, denied peers, server waits and committed/unknown outcomes.

Live moderation/topic tests require a disposable forum supergroup and suitable explicitly authorized roles; some denial cases require a second account. Provisioning or changing that group is a separate explicit action. Temporary folders can contain only test targets and must preserve unrelated folder state. Existing accounts/sessions are never sacrificed for a test.

Gate: no unresolved observed correctness failures; the complete matrix truthfully identifies remaining fixture-dependent gaps. Release a patch only if repairs result. Lack of a disposable group does not block independent tests, but prevents claiming live admin coverage.

## 2. Make account and target selection explicit

Proposed v0.4.0, after phase 1. Priorities A/B. Complexity: small to medium.

- Add an explicit self/Saved Messages selector and a resolve operation returning typed marked identity and source. Keep one target-resolution and idempotency pipeline. Never silently treat an unresolved title as a recipient or introduce a raw-RPC escape hatch.
- Surface Premium status in live `me`; cached status must carry freshness or be explicitly unknown. Read Telegram configuration for effective upload, caption and folder limits. Missing configuration must not silently imply Premium or an unlimited allowance. Preserve the operator's local transfer cap.
- Correct the misleading Premium label on `react --big`; the flag describes the animation and is not itself proof of subscription eligibility.
- Make setup consistently explain where credentials are written and loaded; test the default data directory and explicit TGCTL_HOME from unrelated working directories.

Tests: free/Premium/unknown/expired status, malformed or missing config, account switch, username reassignment, self peer typing, target/fingerprint consistency, offline dry-run, large-file part calculations, Unicode caption boundaries and secret-free diagnostics. Verify known installed gotd types before changing dependencies.

Gate: verified account identity and effective limits are observable; users can address Saved Messages without copying a numeric ID. No schema migration machinery added.

## 3. Fetch messages directly from Telegram

Proposed v0.5.0. Priority B. Complexity: medium.

- Add explicit remote history, search and single-message retrieval, with sender/date/media filters and bounded pagination. Final command naming should be selected in a short CLI design review; examples such as `search --source telegram` are proposals, not existing commands.
- Preserve the distinction between local cache and server results. A cache miss is never reported as proof a message does not exist remotely.
- Cursors must bind account, peer, query/filter/order and continuation. Return clear source, bounds and coverage; do not promise an immutable server snapshot.
- Remote reads must not implicitly mark messages read, join chats, or download media. Any cache writes must follow an explicit documented mode and the existing write gate.

Support: messages.getHistory, messages.search, messages.getMessages and channels.getMessages in pinned gotd.

Tests: full and empty pages, overlap/deduplication, server truncation, malformed/cross-account cursors, deleted messages, inaccessible peers, slow/flood waits, cancellation, media filters and no implicit read acknowledgment.

Gate: find an uncached message without whole-chat backfill, retrieve it, and paginate without duplicates under stable data. Document concurrent-edit limitations.

## 4. Retrieve conversation context correctly

Proposed v0.6.0. Priority B. Complexity: medium; depends on phase 3 pagination.

- Add replies/thread retrieval, channel-discussion resolution and topic-scoped history.
- Preserve root, reply and topic identifiers, formatting metadata, and distinct broadcast/discussion peers. Never automatically join a linked discussion.
- Verify topic-plus-reply send routing and expose full effective permissions/slow-mode information to explain denied operations. Rights inspection is advisory: execution must still honor server rejection.

Support: messages.getReplies, messages.getDiscussionMessage, existing forum APIs, channels.getParticipant, channels.getFullChannel and messages.getFullChat.

Tests: empty/deleted roots, channel-local ID collisions, linked discussion membership denial, general/forum topics, nested replies, topic pagination, owner/admin/member/restricted rights and permissions changing between inspection and execution.

Gate: retrieve the context of a selected message and identify the correct reply destination without scanning an entire chat. Full live forum coverage remains conditional on an authorized disposable fixture.

## 5. Add everyday account management

Later bounded increments, not one large release. Priority B. Complexity: small to medium each.

| Addition | Use case and API support | Required tests and safety |
| --- | --- | --- |
| Logout current session | Retire this CLI authorization via auth.logOut | Confirmation, already revoked, unknown remote result, cleanup failure, unrelated sessions preserved; test revocation offline until a disposable session is authorized |
| Archive/unarchive | Triage chats via folders.editPeerFolders | Correct folder ID, no-op replay, pinned state and account isolation; preserve existing folders |
| Mute/unmute | Control one chat via account.updateNotifySettings | Inherited versus explicit settings, expiry/time zone, unrelated settings preserved; no implicit account-wide changes |
| Contact add/remove | Maintain a known contact via contacts.addContact/deleteContacts | Typed target, duplicate/non-contact, explicit phone-sharing choice, dry-run and remote-success/local-failure handling; no bulk harvesting |
| Formatted text/captions | Code blocks and links via message entities | Explicit formatting input, UTF-16 offsets, emoji/surrogate pairs, invalid overlaps, length limits and idempotency fingerprint; plain text default |

Gate each addition independently; a failure does not justify widening the change to unrelated command families.

## 6. Optional Premium and convenience features

Priorities B when a recurring use case is established, otherwise C. Each is a separate proposal; subscription does not add CLI commands automatically.

| Addition | Benefit/support | Complexity, tests and constraints |
| --- | --- | --- |
| Voice transcription | Read voice notes; messages.transcribeAudio and updateTranscribedAudio are in pinned gotd | Medium. Pending/final updates, quota/trial fields, unavailable transcription, waits, cancellation and transcript privacy. No automatic transcription of all chats |
| Translation | Selected multilingual messages; messages.translateText exists | Medium. Language errors, entities, batches, partial failures, gating and privacy. Distinguish selected-message translation from automatic chat translation |
| Saved Messages tags | Organize personal notes; saved reaction tags and search APIs | Medium. Preserve unrelated tags, pagination, account capabilities, deletion and replay; dependent on remote search |
| Custom emoji reactions | Use chosen custom reactions | Medium. Document IDs, chat allowed reactions, Premium/server rejection, multiple reactions and preservation semantics |
| Drafts and scheduled messages | Compose across devices or schedule one approved send | Medium. Draft conflicts, explicit schedule/time zone, cancellation, idempotency and scheduling versus delivery; no recurring mass automation |
| Transfer progress | Understand long operations | Medium. stderr-only progress, bounded concurrency, cancellation, low disk and aggregate resource limits; resume remains excluded until its correctness is demonstrated |

Defer cosmetic profile features, stories, calls, payments/Stars, broad business automation and general-purpose bot management. No automatic subscription purchases, automatic retries of unknown sends, relaxed flood controls or restoration of legacy compatibility.

## Release and completion rules

For each implemented phase: review the patch; run meaningful regression tests, full unit/race tests, vet, native build, Windows cross-build, formatting, generated documentation and hygiene checks; require native CI before tagging. Update bundled and installed skills with verified behavior only. Publish clear release notes and verify archive checksums and Homebrew metadata before installation. Re-run a bounded relevant installed-binary smoke test.

Complete phase 1 first. Then implement phases 2–4 in order, releasing only when their gates pass. Phase 5 items are selected independently; phase 6 remains optional. Estimates describe complexity, not calendar commitments.

## Evidence

This plan builds on the source-backed [reliability roadmap](reliability-roadmap.md) and Premium research checked on 12 September 2026. Recheck API/dependency details when implementation starts; these references are not proof of current CLI support:
- https://core.telegram.org/api/premium
- https://core.telegram.org/api/config
- https://core.telegram.org/method/messages.search
- https://core.telegram.org/method/messages.getReplies
- https://core.telegram.org/method/messages.getDiscussionMessage
- https://core.telegram.org/api/transcribe
- https://core.telegram.org/api/translation
- https://core.telegram.org/api/saved-messages

The existing roadmap also records a channel sponsored-message support/release-scope question. Resolve it against current official requirements before expanding channel functionality; it was not closed by the v0.3.1 cancellation patch.

## Implementation handoff

The user authorized execution of this plan using GPT-5.6 Luna with high reasoning on 13 September 2026. The original “proposed” status above describes its drafting stage; execution is now authorized, subject to the scope below. Start with phase 1 and continue through phases 2–4 when their gates pass. Phase 5 remains separate bounded increments after the core work; phase 6 remains optional and is not part of this handoff. Suggested version numbers are planning labels, not authorization to tag or publish.

### Concrete design decisions

- Do not build a new framework or replace gotd. Follow existing Cobra commands, narrow client interfaces, marked peer IDs, SQLite stores, dispatch envelopes and fake/TL-invoker tests.
- First create `docs/command-test-coverage.md`: enumerate commands from the built binary and explicitly distinguish execution from assertion-backed tests. Cite repository test names and redacted live evidence. Track absent evidence as unverified. Prior broad claims are not acceptance evidence.
- For phase 2, use `self` as the reserved selector for the authenticated account, consistently across relevant read/write commands. Resolve without network when an account-bound cache supplies identity; an unresolved dry-run must not log in or make RPCs. Add a dedicated `resolve <selector>` command with explicit local/server source selection. Ensure username reassignment and account identity cannot redirect a previously confirmed operation.
- Expose Premium status and its source/freshness through `me`; do not treat a missing field as false. Avoid adding a schema column just for this: reuse current cached self metadata where appropriate. Add a bounded account-limits read command backed by help.getAppConfig and authenticated account capabilities. Report unknown values explicitly. The effective file cap is the minimum of known server and explicit operator limits, with conservative refusal or an actionable error when limits cannot be safely established.
- Setup's default credential destination must match the runtime's selected stable root, independent of cwd. Preserve explicit --env-file overrides and existing read-only/secret-handling gates. Keep help output deterministic so generated docs do not embed personal paths.
- For phase 3, extend the existing history/search/get command families with an explicit `--source cache|telegram`, defaulting to cache. Remote mode does not silently persist retrieved history or acknowledge reads. Prefer opaque typed JSON cursors encoded with standard encoding/base64; validate version, account identity, peer, filters, order and server offset before use. These are continuation tokens, not authentication tokens or frozen snapshots. Refuse combinations the API cannot faithfully support rather than emulating them with unbounded scanning.
- For phase 4, add `replies <chat> <message-id>` and a discussion-resolution command, reusing the phase 3 cursor and normalization code only where semantics actually match. Topic history must explicitly identify its topic. Keep original channel and linked discussion peers distinct. Add full/effective rights inspection without claiming it guarantees a subsequent mutation will succeed.
- Add interface methods/types only as each command requires; update FakeClient and invoker tests in the same change. Do not add unused speculative capability layers. Respect current-schema-only policy and preserve existing data without destructive resets.

### Work sequence and evidence

1. Establish baseline commit/version and inspect local changes. Run baseline checks once; create the coverage matrix and verify existing tests actually assert expected behavior.
2. Repair validated issues with a minimal reproducer first. Reuse the installed v0.3.1 cancellation result and committed/unknown precedence; do not redo or overwrite the already-merged fix.
3. Implement phase 2 in small reviewable commits, then phase 3, then phase 4. Check current official API docs and the pinned dependency's generated request types before each adapter change. Complete local validation at each phase and keep unresolved live coverage visible.
4. Update bundled skill, generated references, documentation and the phase status ledger. Do not overwrite the installed skill until the matching CLI changes are installed. Do not change the user's installed binary, push to main, merge, tag or publish in this execution task.
5. Finish with completed phases, remaining items, exact commits/files, validation evidence, compatibility changes and a proposed release scope for review. If a prerequisite is missing, continue independent work and identify the specific blocked test.

### Live-test boundaries

This coding handoff authorizes code changes, offline fixtures, and bounded live account reads/local cache operations already used in the parent task. It does not enlarge earlier permission to mutate Telegram. The parent task encountered an explicit automatic-review block on live writes; do not infer authorization to bypass it from earlier task summaries. Prepare tests that create/edit/delete Saved Messages probes, temporary folders or disposable groups, but run their Telegram mutations only after explicit approval in this task. Never touch unrelated messages, folders, contacts, permissions or sessions. No credential or private content belongs in a commit or report.
