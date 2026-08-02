# Safety model

`tgctl-go` runs against your real Telegram account. A misconfigured
agent that sends, forwards, or deletes the wrong thing has real
consequences — friends DM'd in error, messages permanently gone,
chats abandoned. The safety model is designed for that threat:
*the operator is a script that may be wrong*.

## Telegram-side write pipeline

Ordinary Telegram-side write commands use the shared `writes.Run` pipeline in
this order:

```
parse args
   ↓
write gate: --read-only rejection, then --allow-write requirement
   ↓
idempotency lookup: a committed replay returns before resolution
   ↓
selector gate: --fuzzy if needed, then resolve from the local cache
   ↓
dry-run short-circuit: print would-do envelope, exit 0
   ↓
local sliding-window limiter: 20 outbound writes / 60s default
   ↓
audit_pre: NDJSON entry with shared request_id
   ↓
Telegram call
   ↓
idempotency record
   ↓
final dispatch audit + success / fail envelope
```

Typed destructive commands add an earlier read-only preflight: enforce the
write and fuzzy gates, select the account, resolve the target once from the
cache, and validate `--confirm`. The pipeline then consumes that immutable
target rather than resolving again. Confirmation therefore precedes dry-run,
idempotency replay, audit writes, writable database or session access, and
Telegram client construction.

Local mutation commands such as `backfill` and `download-media` do **not** use
this full Telegram-write pipeline. They select an account, enforce
`--read-only` and `--allow-write`, and perform command-specific validation.
Operations that reach dispatch finalize through a durable outcome audit, so an
append failure is surfaced rather than ignored. These commands do not inherit
the pipeline's idempotency lookup, dry-run,
outbound-write limiter, or Telegram pre-call audit.

## Write gate (`--allow-write`)

Every Telegram-side write and every command that mutates the local cache
requires `--allow-write` per invocation. Read commands do not.

```bash
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "hi"
# → exit 6: WRITE_DISALLOWED
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "hi" --allow-write
# → sends
```

To globally lock down a session against any write:

```bash
export TG_READONLY=1
```

This rejects writes even with `--allow-write` flagged. It also prevents local
state mutation or creation: account directories, SQLite databases and
migrations, session state, audit logs, and startup migrations remain untouched.
Telegram reads use read-only session storage.

`--allow-write` is permission for the requested operation. For example,
`backfill` and `download-media` require it because they update local state.

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
tg --account "$TG_ACCOUNT_NAME" delete-msg "$TG_CHAT_ID" "$TG_MESSAGE_ID" \
  --allow-write
# → exit 7: NEEDS_CONFIRM
tg --account "$TG_ACCOUNT_NAME" delete-msg "$TG_CHAT_ID" "$TG_MESSAGE_ID" \
  --allow-write --confirm "$TG_CHAT_ID"
```

Omitting confirmation returns `NEEDS_CONFIRM` (exit 7). Supplying a value
that does not match the resolved target returns `BAD_ARGS` (exit 2). Both are
checked before Telegram or local-state writes.

This catches the failure mode where an agent intended one chat but a fuzzy
selector resolved to another.

## Fuzzy gate (`--fuzzy`)

Chat selectors resolve via three strategies in order:

1. **Integer chat_id** — exact match, always allowed
2. **`@username`** — exact match, always allowed where the command supports it
3. **Fuzzy substring on cached chat title** — allowed for reads,
   **rejected for writes unless `--fuzzy` is passed**

```bash
tg --account "$TG_ACCOUNT_NAME" show "$TG_TITLE_FRAGMENT" --limit 5
tg --account "$TG_ACCOUNT_NAME" send "$TG_TITLE_FRAGMENT" "..." --allow-write
# → exit 2: BAD_ARGS without --fuzzy
tg --account "$TG_ACCOUNT_NAME" send "$TG_TITLE_FRAGMENT" "..." \
  --allow-write --fuzzy
```

The point is to make agents commit to fuzzy resolution at call site
rather than silently accepting whatever the title-match returned.
For reads it doesn't matter; for writes you can't recover.

## Idempotency keys

Telegram write commands that expose `--idempotency-key <name>` cache committed
results. If the same key and command are retried, the cached result envelope is
returned **without** re-calling Telegram.

```bash
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "ack" \
  --allow-write --idempotency-key "$TG_IDEMPOTENCY_KEY"
```

Use case: an LLM-drafted reply pipeline that retries after
`FLOOD_WAIT` (`exit 5`). The first attempt sends; the retry sees
the cached envelope and returns the prior `message_id` — no double-send.

The cache is per-account, in `accounts/<name>/telegram.sqlite`'s
`tg_idempotency` table. Same key reused for a *different* command
raises `BAD_ARGS`. For typed-confirmation commands, the key is also bound to
the stable operation identity: resolved peer plus confirmation slot/value.
Reusing it for another destination or confirmed target raises `BAD_ARGS`
instead of replaying the earlier operation.

## Audit log

Telegram writes using the shared write pipeline generate a pre-call entry and a
final dispatch entry in `audit.log`:

```json
{"ts":"<timestamp>","phase":"before","request_id":"req-example","cmd":"send","resolved_chat_id":"<your-chat-id>","payload_preview":{},"dry_run":false}
{"ts":"<timestamp>","request_id":"req-example","cmd":"send","args":{"chat":"<your-chat-id>","dry_run":false},"result":"ok"}
```

The pre-call entry is written *before* the Telegram call, so even if
the call hangs / crashes / times out, you know what was attempted.
The final entry shares the same `request_id` so retries are linkable. Local
cache and media commands do not use the Telegram pre-call pipeline; they record
their final dispatch outcome. `download-media` and `backfill`
treat failure to finalize that audit record after a committed artifact as a
partial committed error instead of silently reporting success.

`audit.log` lives at `accounts/<name>/audit.log` and is append-only.
On Unix, sensitive files are created with mode 0600 (owner-only
read/write). Windows does not expose POSIX mode bits through Go's
`os.FileMode`; there, protection comes from the inherited ACL on the
private account root and the session lock remains exclusive.

## Local rate limiter

A sliding window caps outbound Telegram writes at 20 per 60 seconds
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

On Unix, sensitive files in `accounts/<name>/` are chmod'd to owner-only:

| File | Mode |
|---|---|
| `tg.session` | 0600 |
| `telegram.sqlite` | 0600 |
| `audit.log` | 0600 |
| `tg.session.lock` | 0600 |
| Account directories | 0700 |

This is best-effort and never blocks the operation if it fails.
On Windows, Go reports implementation-defined POSIX mode bits; the account
root's inherited ACL is the protection boundary instead.

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

## Dry-run

Commands that expose `--dry-run` still enforce `--allow-write`,
`--read-only`, fuzzy-selector, and typed-confirmation checks. After those gates,
dry-run returns the resolved payload without contacting Telegram. Commands such
as `download-media` and `backfill` do not currently expose `--dry-run`; check
the command's `--help` rather than assuming the flag is universal.

A safe agent loop:

```bash
out=$(tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "status" \
  --allow-write --idempotency-key "$TG_IDEMPOTENCY_KEY" --json) || true
if [ "$(jq -r '.error.code // empty' <<<"$out")" = "FLOOD_WAIT" ]; then
  sleep "$(jq -r '.error.retry_after_seconds' <<<"$out")"
  # retry with the same --idempotency-key
fi
```

Pair this with `--idempotency-key` so the retry is safe even if the
original request actually landed before the FloodWait fired.

## See also

- [Multi-account](multi-account.md) — audit log paths per account
- [Library use](sdk.md) — agent subprocess pattern with idempotency
- [Quickstart](quickstart.md) — first safe send
