---
name: tgctl-go
description: Use when operating the tgctl-go Telegram CLI for account/auth, discovery/cache, messages, media, synchronization, exports, folders, moderation, forum topics, permissions, or releases
---

# tgctl-go Telegram CLI

Use the `tg` binary for account-scoped Telegram user operations. Prefer
`--json` for automation. Keep `.env`, sessions, SQLite databases, media,
audit logs, and raw live transcripts out of source control.

## Safety and account selection

- Always pass `--account <name>` in agent or automation commands. Use
  `accounts-add`, `login`, `accounts-use`, and `me` to manage and verify
  identities.
- Numeric chat IDs and explicit `@usernames` are safest. A title selector for
  a write requires `--fuzzy`; never infer a write target from a title.
- Commands that expose `--allow-write` require it for Telegram-side or local
  cache/media writes; `--read-only` overrides it. Auth/account setup/import
  commands use their own guards. Dry-runs still require the write gate but
  make no Telegram or network call.
- Never place Telegram IDs, usernames, phone numbers, invite links, API
  credentials, session paths, captions, source/media paths, message contents,
  database files, or audit lines in logs, prompts, commits, or public issues.

## Complete command coverage

The CLI has 74 commands. Do not infer that the album workflow is the whole
product. Use the bundled [complete command reference](references/commands.md)
for the exact usage, flags, examples, and output fields; it is generated from
the same Cobra help used to build `docs/commands.md`.

- **Identity and account state:** `account-sessions`, `accounts-add`,
  `accounts-list`, `accounts-remove`, `accounts-show`, `accounts-use`,
  `import-telethon-session`, `login`, `me`, `setup`, `terminate-session`.
- **Discovery, cache, and reads:** `backfill`, `backfill-entities`,
  `chats-info`, `chat-members`, `chat-pinned-list`, `contacts`, `discover`,
  `doctor`, `get-msg`, `list-msgs`, `search`, `show`, `stats`, `sync-contacts`,
  `topics-list`, `unread`.
- **Messages:** `delete-msg`, `edit-msg`, `forward`, `mark-read`, `pin-msg`,
  `react`, `send`, `send-by-username`, `unpin-msg`.
- **Media:** `download-album`, `download-media`, `upload-album`,
  `upload-document`, `upload-photo`, `upload-video`, `upload-voice`.
- **Synchronization and archives:** `export`, `listen`, `sync`.
- **Dialog folders:** `folder-add-chat`, `folder-create`, `folder-delete`,
  `folder-edit`, `folder-remove-chat`, `folder-show`, `folders-list`,
  `folders-reorder`.
- **Chat administration:** `ban-from-chat`, `block-user`, `chat-description`,
  `chat-invite-link`, `chat-photo`, `chat-title`, `demote`, `kick`,
  `leave-chat`, `promote`, `set-permissions`, `unban-from-chat`,
  `unblock-user`.
- **Forum topics:** `topic-create`, `topic-edit`, `topic-pin`, `topic-unpin`.
- **Shell utilities:** `completion`, `help`, `version`.

### Universal CLI contract

The general form is `tg [global flags] <command> [command flags]`. Every
command supports the applicable global flags `--account`, `--full`, `--json`,
`--human`, `--lock-wait`, `--read-only`, and `--version`. Non-interactive
stdout defaults to JSON; use `--human` only for a person at a terminal.

- Resolve the account before resolving a chat. An explicit `--account` is
  mandatory for automation, even when one account is currently selected.
- Numeric IDs and `@usernames` are deterministic selectors. A title-like
  selector on a write requires `--fuzzy`; never guess a write target.
- `--allow-write` gates Telegram writes and local state/media writes. Commands
  such as `discover`, `backfill`, `sync`, and `download-*` can need it even
  when their primary purpose sounds read-only. `--read-only` wins and
  prevents database, session, audit, and media-path creation too.
- Destructive or administrative operations require typed `--confirm` after
  the target is resolved. Treat `delete-msg`, `leave-chat`, bans/kicks,
  block/unblock, promote/demote, `terminate-session`, folder deletion, and
  permission changes as writes; inspect `tg <command> --help` for the exact
  confirmation value.
- `--dry-run` exists only on selected commands. It still enforces write and
  confirmation gates, but must make zero Telegram/network calls. Never assume
  every command has a dry-run mode.
- `--idempotency-key` is per account and command. Reuse it only for an
  identical request; a definitive rejection can be reported, but an unknown
  transport result must not be retried blindly.
- JSON is a stable envelope with `ok`, `command`, and `request_id`; success
  puts command data in the result/payload, and failure puts a structured
  `error.code` and `error.message` in the envelope. Preserve the envelope in
  automation instead of scraping human text.

### What each command family actually changes

- **Account commands** create, select, inspect, import, authorize, or remove
  isolated account state. `login`/`import-telethon-session` affect sessions;
  `accounts-remove` deletes an account directory only after confirmation.
- **Discovery/cache commands** populate or query the local SQLite mirror.
  `backfill` fetches history and preserves album grouping; `discover` caches
  dialogs/entities; `sync-contacts` and `backfill-entities` fill lookup data;
  `doctor` diagnoses configuration; `stats` reports local counts.
- **Message commands** operate on text or cached message IDs. `send` needs
  an entity cache; `send-by-username` resolves an `@username` directly.
  `forward`, edit, reactions, read markers, pins, and deletes are Telegram
  writes and should return their JSON envelope for audit/retry decisions.
- **Media commands** upload one file or a 2–10 item group, or download media
  into the account-scoped media root. Use the album rules below and the
  complete reference for per-kind flags and size limits.
- **Folder and administration commands** mutate Telegram dialog folders,
  chat metadata, membership, permissions, moderation, and forum topics.
  They require explicit targets, write gates, and typed confirmations where
  the command exposes `--confirm`; never run them against a real group for a
  smoke test.
- **`listen` and `sync`** are long-running/event workflows. `sync` persists
  checkpoints and can use `--follow --once`; `listen` streams update envelopes
  and should be bounded with `--once` in deterministic tests.
- **`export`** is local-only: it reads the SQLite snapshot and media root,
  emits JSONL/CSV/HTML, and can create/verify a manifest without contacting
  Telegram. `completion`, `help`, and `version` are local shell utilities.

## Setup and login

`TG_API_ID` and `TG_API_HASH` are required for both phone and QR login. The
setup command preserves unrelated `.env` entries, writes mode `0600` on Unix,
and never prints the API hash:

```bash
tg setup --env-file .env
tg accounts-add work --json
tg --account work login --qr --human
tg --account work login --qr --qr-uri --human
tg --account work me --read-only --json
```

QR login is an authorization method, not an API-credential bypass.

## Upload albums

Albums contain 2–10 ordered items. Photo/video albums may mix those two kinds;
audio albums and document albums must each be same-type. `auto` is the default;
force `photo`, `video`, `audio`, or `document` with `--media-kind`:

```bash
tg --account work upload-album 123 ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --allow-write --dry-run --json
tg --account work upload-album 123 ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --idempotency-key album-001 --allow-write --json
tg --account work upload-album 123 ./01.mp3 ./02.mp3 \
  --media-kind audio --allow-write --json
```

The CLI uploads every file with `messages.uploadMedia`, then makes one
`messages.sendMultiMedia` call with a unique random ID per item. The caption is
placed on the first item by CLI policy. Success returns all message IDs and
the shared `grouped_id` when Telegram supplies it. Use a valid photo or normal
video fixture; Telegram may reject tiny, malformed, or silent synthetic MP4s
in an album with `MEDIA_EMPTY` even when a single-video upload succeeds.

Reuse an idempotency key only for the identical account, chat, ordered files,
media kind, caption, reply target, silent flag, and streaming flag. The size
limit is a safety cap, not part of the idempotency fingerprint. Definitive
Telegram RPC rejections are safe to report as rejected; cancellation or
unknown transport failure at final send is ambiguous. Do not retry an
ambiguous result blindly. Earlier uploads are not transactional and may
remain on Telegram if a later upload or the final send fails.

## Backfill, sync, and album downloads

```bash
tg --account work backfill 123 --allow-write --download-media --json
tg --account work sync 123 --follow --once --allow-write --json
tg --account work download-album 123 --grouped-id 9001 \
  --output ./album-9001 --allow-write --json
tg --account work download-media 123 456 --output ./media \
  --max-size-mb 100 --allow-write --json
```

Backfill preserves `grouped_id`, de-duplicates overlapping history pages, and
reports `albums_seen`. `sync` stores per-account/chat checkpoints, persists
before advancing them, and reconnects with bounded backoff. `--once` requires
`--follow`. Album downloads select a cached group by `--grouped-id` or anchor
message ID; they can be partial, and `--overwrite` forces fresh files. Existing
artifacts are skipped only after local regular-file/size inspection; the CLI
does not compare a remote content hash or media identity during that skip.
An ungrouped anchor returns the stable `not-an-album` result. Album output
intentionally omits captions and local artifact paths.

Dry-run reads local metadata only. The supported retry for interrupted media
is a new atomic full download: gotd v0.144.0 exposes no safe CDN-offset resume
primitive, so there is intentionally no `--resume` flag.

## Local archives and manifests

```bash
tg --account work export 123 --format jsonl --output ./chat.jsonl \
  --include-media --manifest ./chat.manifest.json --manifest-hash --json
tg --account work export --verify ./chat.manifest.json --json
```

Export never contacts Telegram. Normal export reads cached SQLite and refuses
to overwrite output; `--manifest` and `--verify` also scan the selected
account's media root. Manifest verification is offline and reports stable
`ARCHIVE_MISSING`, `ARCHIVE_CHANGED`, or `ARCHIVE_EXTRA` results.
`ARCHIVE_EXTRA` scans the entire media root, so unrelated chat artifacts also
count as extras.

## Permission and live verification

Use Saved Messages for neutral album/download probes. For permissions, use
only a disposable group/channel and two already-authorized accounts:

```bash
TGCTL_LIVE_PERMISSION_CHAT=<disposable-id> \
TGCTL_LIVE_ALLOWED_ACCOUNT=default \
TGCTL_LIVE_DENIED_ACCOUNT=restricted \
scripts/live_permissions.sh
```

The operator must provision and clean the disposable chat; the script requires
a numeric chat ID and distinct already-authorized account names. It sends one
allowed probe and one denied probe, expects exit 10 and `PERMISSION_DENIED`
for the restricted member, keeps raw output ephemeral, and does not ban,
promote, delete, mass-message, or deliberately create a real `FLOOD_WAIT`.
The broader `scripts/live_verify.sh` exercises many write commands and is not
an album test; run it only against an isolated target. Separately test album
upload/download with Saved Messages or another disposable chat using valid
photo, video, audio, and document fixtures. The album matrix should cover a
valid JPEG plus normal H.264/AAC video carousel, audio-only and document-only
groups, order, first-item caption, returned IDs, shared `grouped_id`, backfill,
album download output, idempotent replay, and zero-network dry-run. Keep
fixtures and transcripts ephemeral. The permission script does not create or
delete accounts or chats.

## Verification and release

Run the project with Go 1.25 or newer, matching the module and CI workflows.

Before changing or releasing the public repository:

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
GOOS=windows GOARCH=amd64 go build ./...
test -z "$(gofmt -l .)"
make docs-commands-check
make public-hygiene
```

Push a version tag (`v*`) only from a clean, verified `main`. GoReleaser then
publishes Linux/macOS/Windows archives, `checksums.txt`, and (when
`HOMEBREW_TAP_TOKEN` is configured) the Homebrew tap formula. Verify the
release workflow and GitHub assets before announcing it; do not blindly run a
package-manager upgrade. Never publish sessions, databases, audit logs, or
private live-test output.

## Stable results and later scope

Important exit codes are `FLOOD_WAIT` (5), `PERMISSION_DENIED` (10),
`ARCHIVE_MISSING` (11), `ARCHIVE_CHANGED` (12), and `ARCHIVE_EXTRA` (13).
Thumbnails, disk-space preflight, transfer concurrency, all-or-nothing album
orchestration, and safe resumable transfers remain future hardening—not hidden
requirements of the current CLI.
