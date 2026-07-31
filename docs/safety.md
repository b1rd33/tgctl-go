# Safety model

`tgctl-go` runs against your real Telegram account. A misconfigured
agent that sends, forwards, or deletes the wrong thing has real
consequences — friends DM'd in error, messages permanently gone,
chats abandoned. The safety model is designed for that threat:
*the operator is a script that may be wrong*.

## The pipeline

Every write command passes through these gates in order:

```
parse args
   ↓
write gate: --allow-write or TG_READONLY rejection
   ↓
selector gate: --fuzzy required for title selectors; resolve from the cache read-only
   ↓
destructive gate (if applicable): typed --confirm <resolved-id>
   ↓
dry-run short-circuit: print would-do envelope, exit 0
   ↓
local rate limiter (token bucket: 20 outbound / 60s default)
   ↓
audit_pre: NDJSON entry with shared request_id
   ↓
Telegram call
   ↓
audit_post: NDJSON entry with same request_id + result
   ↓
emit success / fail envelope
```

## Write gate (`--allow-write`)

Any command that hits Telegram requires `--allow-write` per
invocation.

```bash
tg send 1240314255 "hi"                  # → exit 6: WRITE_DISALLOWED
tg send 1240314255 "hi" --allow-write    # → sends
```

To globally lock down a session against any write:

```bash
export TG_READONLY=1
```

This rejects writes even with `--allow-write` flagged. Use this in
scripts that are supposed to be pure-read.

## Destructive gate (typed `--confirm <id>`)

Destructive commands require `--confirm <id>` matching their typed target,
not just a flag. Confirmation is checked before dry-run, idempotency replay,
audit writes, writable database/session access, or creation of a Telegram
client.

| Commands | Confirmation target |
|---|---|
| `delete-msg`, `leave-chat`, `promote`, `demote` | resolved `chat_id` |
| `block-user`, `unblock-user`, `ban-from-chat`, `kick` | resolved `user_id` |
| `folder-delete` | `folder_id` |
| `terminate-session` | `session_hash` |

```bash
tg delete-msg 1240314255 99 --allow-write                    # → exit 7: NEEDS_CONFIRM
tg delete-msg 1240314255 99 --allow-write --confirm 1240314255
```

This catches the "agent meant to delete in Hamid's chat but resolver
matched Hamburg supergroup" failure mode that bare-flag confirms
allow.

## Fuzzy gate (`--fuzzy`)

Chat selectors resolve via three strategies in order:

1. **Integer chat_id** — exact match, always allowed
2. **`@username`** — exact match, always allowed where the command supports it
3. **Fuzzy substring on cached chat title** — allowed for reads,
   **rejected for writes unless `--fuzzy` is passed**

```bash
tg show Hambu --limit 5                    # ← reads OK with fuzzy match
tg send Hambu "..." --allow-write          # ← exit 2 BAD_ARGS without --fuzzy
tg send Hambu "..." --allow-write --fuzzy  # ← OK
```

The point is to make agents commit to fuzzy resolution at call site
rather than silently accepting whatever the title-match returned.
For reads it doesn't matter; for writes you can't recover.

## Idempotency keys

Every write command accepts `--idempotency-key <name>`. If the same
key + same command was previously committed, the cached result
envelope is returned **without** re-calling Telegram.

```bash
tg send 1240314255 "ack" --allow-write --idempotency-key reply-99-2026-05-09
```

Use case: an LLM-drafted reply pipeline that retries after
`FLOOD_WAIT` (`exit 5`). The first attempt sends; the retry sees
the cached envelope and returns the prior `message_id` — no double-send.

The cache is per-account, in `accounts/<name>/telegram.sqlite`'s
`tg_idempotency` table. Same key reused for a *different* command
raises `BAD_ARGS`.

## Audit log

Every write generates **two NDJSON entries** in `audit.log`:

```json
{"ts":"2026-05-09T11:18:21Z","phase":"before","request_id":"req-abc","cmd":"send","actor":"agent","resolved_chat_id":1240314255,"args":{},"dry_run":false}
{"ts":"2026-05-09T11:18:22Z","phase":"after","request_id":"req-abc","cmd":"send","result":"ok","message_id":99}
```

The pre-call entry is written *before* the Telegram call, so even if
the call hangs / crashes / times out, you know what was attempted.
The post-call entry shares the same `request_id` so retries are
linkable.

`audit.log` lives at `accounts/<name>/audit.log` and is append-only.
File permissions are 0600 (owner-only read/write).

## Local rate limiter

A token bucket caps outbound Telegram writes at 20 per 60 seconds
by default. Hitting it raises `LOCAL_RATE_LIMIT` with a
`retry_after_seconds` field. This is your guard against an agent
loop that runs away.

The Telegram-side rate limit (`FLOOD_WAIT`) is separate and stricter
when triggered. The local limit is more conservative — it gives you
time to notice and stop.

## Session lock

Only one process at a time can hold the gotd session. The lock is
`accounts/<name>/tg.session.lock`. Pass `--lock-wait <secs>` to wait
up to N seconds for the lock instead of failing immediately.

## File permissions

Sensitive files in `accounts/<name>/` are chmod'd to owner-only:

| File | Mode |
|---|---|
| `tg.session` | 0600 |
| `telegram.sqlite` | 0600 |
| `audit.log` | 0600 |
| `tg.session.lock` | 0600 |
| Account directories | 0700 |

This is best-effort and never blocks the operation if it fails.

## Exit codes

| Code | Name | Meaning |
|---|---|---|
| 0 | OK | Command succeeded |
| 1 | GENERIC | Unclassified error |
| 2 | BAD_ARGS | Invalid args (or fuzzy-write without `--fuzzy`) |
| 3 | NOT_AUTHED | `TG_API_ID/HASH` not set or session expired |
| 4 | NOT_FOUND | Chat / message / folder not in DB or server |
| 5 | FLOOD_WAIT | Telegram rate-limited; check `retry_after_seconds` in envelope |
| 6 | WRITE_DISALLOWED | Write attempted without `--allow-write` (or `--read-only` mode) |
| 7 | NEEDS_CONFIRM | Destructive op without `--confirm <id>` |
| 8 | LOCAL_RATE_LIMIT | In-process rate limiter tripped |
| 9 | PREMIUM_REQUIRED | Telegram requires Premium for this action |

## Handling FloodWait

Telegram limits how fast a user account can send messages. When you
exceed the server-side budget, the API returns `FLOOD_WAIT_<seconds>`,
which tgctl-go classifies as exit code 5 with a `retry_after_seconds`
field in the error envelope:

```json
{"ok": false, "command": "send", "request_id": "req-abc",
 "error": {"code": "FLOOD_WAIT", "message": "Telegram FloodWait: wait 30s",
           "retry_after_seconds": 30}}
```

The local sliding-window rate limiter (20 writes per 60 seconds per
process) is meant to keep you well below the server budget, but burst
patterns, account age, and chat type all affect what Telegram
considers acceptable.

A safe agent loop:

```bash
out={"ok":false,"command":"send","request_id":"req-45fec014","error":{"code":"NOT_FOUND","message":"chat_id 1240314255 not in DB"}}
if [ "" = "FLOOD_WAIT" ]; then
  sleep ""
  # retry with the same --idempotency-key
fi
```

Pair this with `--idempotency-key` so the retry is safe even if the
original request actually landed before the FloodWait fired.

## See also

- [Multi-account](multi-account.md) — audit log paths per account
- [Library use](sdk.md) — agent subprocess pattern with idempotency
- [Quickstart](quickstart.md) — first safe send
