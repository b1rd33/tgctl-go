# tgctl-go

**Docs:** https://b1rd33.github.io/tgctl-go/

A single static `tg` binary that drives your real Telegram account from the command line. Send messages, edit them, organize folders, run forum topics, manage admin actions, react, mark-read, backfill history into a local SQLite cache, listen for live updates — all scriptable, all auditable, all behind a JSON envelope.

Go port of the Python [`tgctl`](https://github.com/b1rd33/tg-cli) with the same CLI contract, the same exit codes, the same safety gates. One binary, no runtime, no Python required.

## What it's for

Anything you'd otherwise click through Telegram Desktop to do, but at scale or on a schedule. Concrete uses people are running it for today:

- **Notifications and ops** — send build failures, deploy completions, oncall pings to a chat from CI: `tg send -100123 "deploy ok" --allow-write`
- **Customer-support triage** — backfill a support chat, search, sort by intent, auto-reply with `--idempotency-key` so retries are safe
- **Personal automation** — cron-driven reminders, scrape link-bot output, archive media to disk
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
   tg backfill-entities
   ```

Coming from the Python `tgctl`? Skip steps 3–4 and reuse your existing session:

```bash
tg import-telethon-session ~/path/to/python/tgctl/accounts/default/tg.session
```

## Examples

### Send a message to yourself (Saved Messages)

```bash
tg me --json | jq -r '.data.user_id'                # -> 1240314255
tg send 1240314255 "hello from tgctl-go" --allow-write
```

### Send by username (no cache needed)

```bash
tg send-by-username @durov "👋" --allow-write
```

### Edit, react, pin, delete

```bash
ID=$(tg send 1240314255 "draft" --allow-write --json | jq -r '.data.message_id')
tg edit-msg 1240314255 $ID "final wording" --allow-write
tg react    1240314255 $ID "👍"             --allow-write
tg pin-msg  1240314255 $ID                  --allow-write
tg delete-msg 1240314255 $ID --confirm 1240314255 --allow-write
```

### Idempotent retries from cron / CI

```bash
tg send -1001234567890 "deploy $(git rev-parse --short HEAD) ok" \
  --allow-write \
  --idempotency-key "deploy-$(git rev-parse HEAD)"
```

A second run with the same key returns `idempotent_replay: true` instead of double-sending.

### Read locally, no network

```bash
tg show 1240314255 --limit 20
tg search 1240314255 "invoice" --limit 50
tg list-msgs 1240314255 --since 2026-04-01 --until 2026-05-09
tg get-msg 1240314255 30350
```

### Folders and topics

```bash
tg folders-list
tg folder-create "support" --include-chats 1001,1002,1003 --emoji 🛟 --allow-write
tg topic-create -1001234567890 "Q3 Releases" --allow-write
```

### Admin

```bash
tg chat-title    -1001234567890 "Renamed Group" --allow-write
tg promote       -1001234567890 1240314255 --allow-write --confirm 1240314255
tg ban-from-chat -1001234567890 5555555555 --allow-write --confirm 5555555555
tg chat-members  -1001234567890 --limit 100 --json
```

### Live event stream

```bash
tg listen --json
# → emits {"command":"listen.event","data":{"update_kind":"new_message",...}}
#   per incoming Telegram update. --once for one-shot tests.
```

### Multi-account

```bash
tg accounts-add work
tg accounts-use work
tg login                              # logs in as the work account
tg --account personal me              # one-off override
```

## Safety contract

Every Telegram-side write goes through a fixed pipeline you can trust:

```
write gate → idempotency lookup → fuzzy gate → resolve → dry-run →
local rate limit → audit_pre → Telegram call → idempotency record
```

| Flag / env | Effect |
|---|---|
| `--allow-write` or `TG_ALLOW_WRITE=1` | Required for any Telegram-side write |
| `--read-only` or `TG_READONLY=1` | Rejects all writes, even with `--allow-write` |
| `--fuzzy` | Allows title-like selectors (e.g. `"Bjørn"`) on write commands |
| `--confirm <resolved-id>` | Required for destructive ops (delete-msg, leave-chat, ban, promote, terminate-session) |
| `--idempotency-key <key>` | Replays the prior envelope instead of re-sending |
| `--dry-run` | Returns the resolved payload without contacting Telegram |
| Audit log | NDJSON at `accounts/<name>/audit.log`; one entry pre-call, one post-call, sharing a `request_id` |

Stable exit codes (0–9): `OK`, `GENERIC`, `BAD_ARGS`, `NOT_AUTHED`, `NOT_FOUND`, `FLOOD_WAIT`, `WRITE_DISALLOWED`, `NEEDS_CONFIRM`, `LOCAL_RATE_LIMIT`, `PREMIUM_REQUIRED`.

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
