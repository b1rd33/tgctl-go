# Reliability and migration

These repairs follow v0.1.9. They change several incorrect or unsafe behaviors. They do not promise complete historical coverage, exactly-once delivery, or immunity from Telegram restrictions.

## Account location and ownership

`TGCTL_HOME` selects an absolute data directory. With no override, the directory is `tgctl` under Go's OS user-configuration directory (macOS: `~/Library/Application Support/tgctl`; Linux normally `~/.config/tgctl`; Windows normally `%AppData%/tgctl`). `.env` is loaded from that directory. For an existing checkout-based installation, set `TGCTL_HOME` to its existing absolute root before running the new binary. Changing the working directory no longer changes the selected account store.

Every network client, login and session import acquires exclusive session ownership. `--lock-wait` is cancellable and bounded to 3600 seconds. Local cache reads remain possible while another client listens; simultaneous network commands wait or fail. Read-only network use requires an existing ownership sidecar and matching account-identity cache; it cannot create them. Session replacement is atomic and private. Imports refuse an existing destination session. Account paths reject symlinks; Unix sessions reject hard-link aliases. Windows file protection relies on private directory ACLs.

Root-layout migration holds session ownership, stages moves and rolls back failures. It refuses an outstanding nonempty SQLite WAL: stop all users and checkpoint the legacy database before retrying. It does not silently move only the main database or ignore rename errors.

## Marked peer IDs and legacy caches

Users retain positive IDs; basic groups use `-raw_id`; channels/supergroups use `-1000000000000-raw_id`. Confirmation values, cached keys and emitted events use these marked IDs. Only raw IDs are sent inside the corresponding typed Telegram API peer.

Old caches can contain irreversibly colliding user/group/channel IDs. Migration preserves their chat/entity/message/history-state tables under `_legacy_v1`, then creates empty active tables. It never guesses message ownership. `doctor` reports preserved legacy history. Re-discover and backfill the active cache; `export <legacy-id> --legacy-cache` reads the preserved history locally. Legacy rows cannot supply live write targets. Account/session identity mismatches stop startup.

## Write outcomes and retries

Every supported Telegram mutation records its exact TL request, including random IDs and resolved peers, before invocation. Returned responses and accepted/rejected/unknown state are stored in the private account database. Audit logs omit message text, selectors, captions, paths, invite links and payload previews; they retain bounded operational metadata and are flushed.

Use `--idempotency-key` for a retryable command, including `send-by-username`. Keys bind the command and full request fingerprint. A completed identical request replays its recorded result; a different request is rejected. Pending/unknown operations never expire into an automatic resend. `operations-list` exposes safe outcome metadata without exposing serialized requests. A rejected operation can be retried after correcting the refusal; prepared/unknown records require reconciliation. There is deliberately no force-resend or automatic reservation-release command.

A send with missing or mismatched response IDs reports an accepted-but-unresolved outcome. Accepted Telegram operations followed by cache/audit/output failure remain committed failures, not permission to repeat the write. Unkeyed repeated commands are distinct operations and can duplicate work; exact request recording does not make a new invocation an automatic retry.

The 20-write/60-second guard and Telegram FLOOD_WAIT/SLOWMODE_WAIT cooldowns persist across writable clients. Server waits take precedence. This local ceiling is not a Telegram-approved safe rate. Read-only clients cannot persist newly observed cooldowns; callers must honor the returned wait.

## Moderation and deletion

Permission input is exact (`read-only`, `restrict`, or `send-messages`); unknown strings fail. Changes preserve unrelated restrictions, including encoded flag bits. `set-permissions` requires typed confirmation. Promotion defaults to the minimal `other` right and exposes explicit `--rights`.

Kick performs removal followed by unban and reports a partial commit if the second step fails. It refuses an already restricted participant instead of erasing existing restrictions. Ban remains a separate operation. Folder edits patch the original filter and preserve categories, exclusions, pinned peers and title entities. Shared folders are visible but cannot be edited through unsupported operations.

Deletion verifies every message's authoritative peer before sending a destructive RPC. Channel-local deletion is refused because the API affects everyone. The reported deleted count counts verified requested messages; `pts_count` is separate protocol metadata. Dry-run is a local preview: ownership is verified remotely only during execution.

## Updates, unread and history

Writable clients use gotd's recovery manager with durable pts/qts/date/seq and channel state. A persistence failure closes a checkpoint barrier; gotd cannot advance over failed application. Unsupported too-long gaps fail explicitly. A first connection establishes a protocol baseline, not a complete historical archive.

Normalized full/short/combined updates preserve peer kind, outgoing/reply/edit/media metadata and common versus channel deletion scope. Persistent outbox events have `event_id`. Acknowledgment follows successful consumption; a crash between output and acknowledgment can replay the event. Consumers should deduplicate event IDs. `--once` acknowledges its successful event before exiting. The outbox is durable and disk-backed, not an unbounded in-memory queue; disk failure stops recovery.

Deleted messages remain tombstoned across stale backfill. Newer edits win over older history, and changed media invalidates old file associations. Unsupported expiring messages/media are skipped or refused rather than archived; expiry-aware storage/export is deliberately unsupported.

`discover` returns actual dialogs, including archive pagination, not auxiliary message senders. Results expose read markers and source/coverage metadata. `unread` means cached incoming messages above known dialog read markers; missing history, stale markers and topic-specific read state mean this is not a complete server inbox or topic-unread view. Refresh with discover/sync. `chat-pinned-list` searches pinned messages in its selected chat and reports its 100-result bound. `chats-info` fetches current peer-dialog details.

`show`, `search`, and `list-msgs` return cache source/coverage and `next_cursor`; reuse it as `--cursor` with the same filters/order. Date plus message ID breaks timestamp ties. A full page may provide a cursor whose following page is empty. These reads are not a frozen snapshot across concurrent edits. CLI `--limit` is bounded at 10000. Backfill/sync report truncation and next history offset separately from protocol checkpoints; partial history work cannot advance the history checkpoint.

## Backup and restore

Stop network commands for the selected account. Create a cache snapshot with `db-backup <absolute-snapshot-path> --allow-write`. SQLite produces a consistent snapshot including committed WAL contents, validates integrity, and publishes it without replacing an existing file. Protect the snapshot as private data: it contains messages, access hashes and write/recovery records.

To restore, create a separate empty account with `accounts-add`, then run `--account <new-name> db-restore <snapshot> --allow-write`. Existing databases are never overwritten. The snapshot excludes session credentials and media files; retain those separately in private offline storage while the account is stopped. Do not run two copies of the same restored session concurrently. The next network startup verifies that the session identity matches the restored cache. Restoring an older snapshot also restores older recovery/idempotency state, so reconcile writes since the backup before sending again.

## Verification scope

Offline regressions exercise actual TL requests, two-process ownership, exact request recording, persistent waits, permission encoding, wrong-peer deletion, partial kick, 150-event bursts, a real gotd two-page 150-message recovery gap, checkpoint persistence failure, stale edits/tombstones, cursor ties and WAL snapshot restoration. Full unit/race/vet/build/docs/hygiene gates are required before release. Windows cross-compilation is not native Windows runtime validation. No real account or live Telegram behavior was tested during this repair work.

See [the prioritized feature roadmap](reliability-roadmap.md) for evidence-backed additions and exclusions.
