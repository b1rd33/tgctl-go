# tgctl-go

**Docs:** https://b1rd33.github.io/tgctl-go/

A single static `tg` binary that drives your real Telegram account from the command line. Send messages, edit them, organize folders, run forum topics, manage admin actions, react, mark-read, backfill history into a local SQLite cache, listen for live updates — all scriptable, all auditable, all behind a JSON envelope.

Go port of the Python [`tgctl`](https://github.com/b1rd33/tg-cli) with the same CLI contract, the same exit codes, the same safety gates. One binary, no runtime, no Python required.

## What it's for

Anything you'd otherwise click through Telegram Desktop to do, but at scale or on a schedule. Concrete uses people are running it for today:

- **Notifications and ops** — send build failures, deploy completions, or on-call pings to a chat from CI
- **Customer-support triage** — backfill a support chat, search, sort by intent, auto-reply with `--idempotency-key` so retries are safe
- **Personal automation** — cron-driven reminders and account-scoped media archives
- **Migrations and audits** — adopt your existing Telethon session via `tg import-telethon-session`, then export message history into the local SQLite cache for offline analysis
- **Building bots without Bot API** — full MTProto user-account access via `gotd/td`, not the limited Bot API

It is *not* meant to spam, scrape contacts, or evade rate limits — there's a sliding-window rate limiter and an audit log specifically to keep you on the safe side of Telegram's terms.

## Install

```bash
# Homebrew (Mac / Linux):
brew install b1rd33/tap/tgctl-go

# Anyone with Go:
go install github.com/b1rd33/tgctl-go/cmd/tg@latest

# Pre-built binaries (Linux / macOS / Windows × amd64 / arm64):
# https://github.com/b1rd33/tgctl-go/releases/latest
```

## Setup (one time)

1. Register an app at https://my.telegram.org/apps to get an `api_id` and `api_hash`.
2. Drop them in `.env`:

   ```bash
   cp .env.example .env
   # edit TG_API_ID and TG_API_HASH
   ```

3. Authorize the account:

   ```bash
   tg login
   ```

   You'll get an SMS code in your Telegram app; paste it back. 2FA password is supported.

4. Populate the local entity cache so chat-id-keyed commands work:

   ```bash
   tg backfill-entities --allow-write
   ```

Coming from the Python `tgctl`? Skip steps 3–4 and reuse your existing session:

```bash
tg import-telethon-session "$TELETHON_SESSION_PATH"
```

## Agent quick setup

Use an isolated account while testing agent flows:

```bash
tg accounts-add test
tg --account test login
export TG_CHAT_ID="$(tg --account test me --json | jq -r '.data.user_id')"
tg --account test backfill-entities --allow-write
tg --account test discover --allow-write
tg --account test send "$TG_CHAT_ID" "hello from the sandbox account" --allow-write --json
```

Run login from the directory containing `.env`, or export
`TG_API_ID` / `TG_API_HASH`. See the
[Quickstart](https://b1rd33.github.io/tgctl-go/quickstart/) and
[Library use](https://b1rd33.github.io/tgctl-go/sdk/) docs for the
agent subprocess pattern.

## Examples

### Send a message to yourself (Saved Messages)

```bash
export TG_CHAT_ID="$(tg --account test me --json | jq -r '.data.user_id')"
tg --account test send "$TG_CHAT_ID" "hello from tgctl-go" --allow-write
```

### Send by username (no cache needed)

```bash
tg --account test send-by-username "$TG_RECIPIENT_USERNAME" "hello" --allow-write
```

### Edit, react, pin, delete

```bash
MESSAGE_ID=$(tg --account test send "$TG_CHAT_ID" "draft" --allow-write --json | jq -r '.data.message_id')
tg --account test edit-msg "$TG_CHAT_ID" "$MESSAGE_ID" "final wording" --allow-write
tg --account test react "$TG_CHAT_ID" "$MESSAGE_ID" "👍" --allow-write
tg --account test pin-msg "$TG_CHAT_ID" "$MESSAGE_ID" --allow-write
tg --account test delete-msg "$TG_CHAT_ID" "$MESSAGE_ID" --confirm "$TG_CHAT_ID" --allow-write
```

### Idempotent retries from cron / CI

```bash
tg --account test send "$TG_CHAT_ID" "deploy $(git rev-parse --short HEAD) ok" \
  --allow-write \
  --idempotency-key "deploy-$(git rev-parse HEAD)"
```

A second run with the same key returns `idempotent_replay: true` instead of double-sending.

### Read locally, no network

```bash
tg --account test show "$TG_CHAT_ID" --limit 20
tg --account test search "$TG_CHAT_ID" "invoice" --limit 50
tg --account test list-msgs "$TG_CHAT_ID" --since 2026-04-01 --until 2026-05-09
tg --account test get-msg "$TG_CHAT_ID" "$TG_MESSAGE_ID"
```

### Folders and topics

```bash
tg folders-list
tg --account test folder-create "support" --include-chats "$TG_CHAT_IDS" --emoji 🛟 --allow-write
tg --account test topic-create "$TG_FORUM_CHAT_ID" "Q3 Releases" --allow-write
```

### Admin

```bash
tg --account test chat-title "$TG_GROUP_CHAT_ID" "Renamed Group" --allow-write
tg --account test promote "$TG_GROUP_CHAT_ID" "$TG_USER_ID" --allow-write --confirm "$TG_GROUP_CHAT_ID"
tg --account test ban-from-chat "$TG_GROUP_CHAT_ID" "$TG_USER_ID" --allow-write --confirm "$TG_USER_ID"
tg --account test chat-members "$TG_GROUP_CHAT_ID" --limit 100 --json
```

### Live event stream

```bash
tg --account test listen --allow-write --json
# → emits {"command":"listen.event","data":{"update_kind":"new_message",...}}
#   per incoming Telegram update. --once for one-shot tests.
```

### Multi-account

```bash
tg accounts-add work
tg accounts-use work
tg login                              # uses the persisted current account
tg --account personal me              # explicit one-off override
```

Selection precedence is `--account`, then `TG_ACCOUNT`, then
`accounts/.current`, then `default`. Agents should pass `--account` on every
call so subprocesses do not depend on ambient state.

### Download one media item

The placeholders below are environment variables populated from your own
account; they are not example Telegram identifiers.

```bash
export TG_ACCOUNT_NAME="test"
export TG_CHAT_ID="<chat-id-from-your-own-account>"
export TG_MESSAGE_ID="<message-id-containing-media>"

tg --account "$TG_ACCOUNT_NAME" download-media \
  "$TG_CHAT_ID" "$TG_MESSAGE_ID" \
  --allow-write --max-size-mb 100 --json
```

The default destination is the selected account's
`accounts/<name>/media/<chat-id>/`. Use `--output <directory>` to choose an
explicit destination. Downloads use a private part file and atomic
publication. With the default no-overwrite policy, an existing anchored regular
file at the final name is inspected and returned with `skipped: true`; this is
not a content-hash comparison. `--overwrite` uses the platform's safe atomic
replacement path or fails without falling back to a non-atomic overwrite. The
command updates the local message cache, so it requires `--allow-write` even
though it does not send a Telegram message.

### Backfill messages and media

```bash
tg --account "$TG_ACCOUNT_NAME" backfill "$TG_CHAT_ID" \
  --max-messages 250 \
  --download-media --max-media-size-mb 100 \
  --allow-write --json
```

Backfill stores rows in the selected account's SQLite database and names media
with chat, message, and Telegram media identity to avoid cross-chat and edited
message collisions. Per-item problems are reported through counters and safe
warnings while other rows continue. If files were committed before a later
failure, the error envelope has `committed: true` and bounded recovery metadata;
do not retry blindly.

## Safety contract

Ordinary Telegram-side writes use this fixed pipeline:

```
write gate → idempotency lookup → fuzzy gate → resolve → dry-run →
local rate limit → audit_pre → Telegram call → idempotency record → audit final
```

Typed destructive commands first run a read-only preflight: enforce the write
and fuzzy gates, select the account, resolve the target once from the read-only
cache, and validate the exact typed confirmation. Only then does the later
idempotency/dry-run send pipeline consume that immutable target.

Local media and cache mutations such as `download-media` and `backfill` use
command-specific account, write, and read-only gates plus durable final audit
handling; they do not use the ordinary pipeline above. See the
[Safety model](docs/safety.md) for the exact flows.

| Flag / env | Effect |
|---|---|
| `--allow-write` or `TG_ALLOW_WRITE=1` | Required for any Telegram-side write |
| `--read-only` or `TG_READONLY=1` | Rejects Telegram and local-state writes, including path/database/session/audit creation, even with `--allow-write` |
| `--fuzzy` | Allows title-like selectors (e.g. `"Bjørn"`) on write commands |
| `--confirm <resolved-id>` | Required for destructive ops (delete-msg, leave-chat, ban, promote, terminate-session) |
| `--idempotency-key <key>` | Replays the prior envelope instead of re-sending |
| `--dry-run` | On commands that expose it, returns the resolved payload without contacting Telegram; it still requires the write and confirmation gates |
| Audit log | NDJSON at `accounts/<name>/audit.log`; Telegram write-pipeline entries share a `request_id` across pre-call and final records, while local media/cache commands record final dispatch outcomes |

Stable exit codes (0–9): `OK`, `GENERIC`, `BAD_ARGS`, `NOT_AUTHED`, `NOT_FOUND`, `FLOOD_WAIT`, `WRITE_DISALLOWED`, `NEEDS_CONFIRM`, `LOCAL_RATE_LIMIT`, `PREMIUM_REQUIRED`.

For typed destructive operations, an omitted `--confirm` is
`NEEDS_CONFIRM` (exit 7); a supplied value that does not match the resolved
target is `BAD_ARGS` (exit 2).

## Media scope and non-goals

Current downloads support Telegram photos and file-backed documents, classified
as photo, video, video note, voice, audio, sticker, animation, or generic
document. Photos use Telegram's selected largest downloadable image; documents
retain the original file stream and safe filename. The CLI does not bypass
Telegram content protection, download non-file media such as locations or
polls, or provide an unrestricted scraping facility.

## Planned, not implemented: album v1

There is no runnable `upload-album` or `download-album` command yet, and cached
messages do not yet expose `grouped_id`. The planned first version is:

- upload 2–10 ordered photo/video items, including mixed photo/video albums;
- put the CLI's single album caption on the first item;
- prepare items in CLI order, sequentially uploading each file, converting it
  with `messages.uploadMedia`, and assigning its own unique random ID; only
  after all items are ready, make one final `messages.sendMultiMedia` call;
- return every resulting message ID, preserve Telegram's shared `grouped_id`
  during history/backfill, and offer album-aware grouping/downloads;
- make the planned album dry-run perform zero Telegram or other network calls;
  test ordering, captions, failures, idempotency, and live carousel rendering.

The upload phase is not transactional: if a later temporary upload fails,
earlier temporary uploads may already exist on Telegram even though no album
message was sent. Hashes, resumable downloads, disk-space preflight, manifests,
thumbnails, concurrency controls, all-or-nothing orchestration, and audio or
document albums are later hardening rather than album-v1 requirements.
Recommended later dry-run hardening would additionally guarantee zero database,
audit, or session writes and no local file or directory creation; those local
side-effect guarantees are not core album-v1 acceptance criteria. See the
[Telegram method contract](https://core.telegram.org/method/messages.sendMultiMedia),
[file documentation](https://core.telegram.org/api/files), and
[message schema](https://core.telegram.org/constructor/message).

## JSON envelope

Every command emits one of:

```json
{"ok": true,  "command": "send", "request_id": "req-abc12345",
 "data": {"message_id": 30350, ...}, "warnings": []}

{"ok": false, "command": "send", "request_id": "req-xyz09876",
 "error": {"code": "FLOOD_WAIT", "message": "...", "retry_after_seconds": 30}}
```

Pipe through `jq` and script with confidence — the shape is locked.

## License

MIT. See [`LICENSE`](LICENSE).

## Contributing

See `CHANGELOG.md` and the conventional-commits git history for the implementation arc.
