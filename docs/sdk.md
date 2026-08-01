# Library use

`tgctl-go` is not a Go library you import. It is a binary you exec or
pipe.

The gotd/td-backed client interface lives under `internal/`, so Go's
visibility rules intentionally prevent external packages from importing
it. The stable integration surface is the CLI contract: JSON envelopes,
exit codes, and append-only audit logs.

## JSON envelope

Pass `--json` to force machine output:

```bash
tg me --json
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "hello" --allow-write --json
```

Success:

```json
{
  "ok": true,
  "command": "send",
  "request_id": "req-abc12345",
  "data": {
    "message_id": "<new-message-id>"
  },
  "warnings": []
}
```

Failure:

```json
{
  "ok": false,
  "command": "send",
  "request_id": "req-xyz09876",
  "error": {
    "code": "WRITE_DISALLOWED",
    "message": "write requires --allow-write"
  }
}
```

## Shell integration

Use `jq` for routing and assertions:

```bash
msg_id=$(
  tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "ack" --allow-write --json |
    jq -r '.data.message_id'
)

tg --account "$TG_ACCOUNT_NAME" get-msg "$TG_CHAT_ID" "$msg_id" --json |
  jq -e '.ok == true and .data.message.message_id == '"$msg_id"
```

## Subprocess integration

Any language can call `tg` and parse the envelope.

```python
import json
import os
import subprocess

result = subprocess.run(
    ["tg", "--account", os.environ["TG_ACCOUNT_NAME"], "show",
     os.environ["TG_CHAT_ID"], "--limit", "5", "--json"],
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
)

envelope = json.loads(result.stdout)
if result.returncode != 0 or not envelope["ok"]:
    raise RuntimeError(envelope.get("error", result.stderr))
```

## Agent subprocess pattern

For an agent, make every call explicit: account, JSON output, write
gate, and idempotency key.

```bash
tg --account test backfill-entities --allow-write --json
tg --account test backfill "$TG_CHAT_ID" --max-messages 100 --allow-write --json
tg --account test search "$TG_CHAT_ID" "order" --limit 20 --json
tg --account test send "$TG_CHAT_ID" "agent draft reply" \
  --allow-write \
  --idempotency-key "agent-reply-001" \
  --json
```

Reads do not need `--allow-write`. Local DB writes (`discover`,
`backfill`, `sync-contacts`, `listen`) and Telegram writes do. Treat
`request_id` as the correlation key for logs and audit entries.

Account selection is `--account`, then `TG_ACCOUNT`, then the persisted
`accounts/.current` selector, then `default`. Agents should pass `--account`
explicitly on every subprocess call. `--read-only` (or `TG_READONLY=1`) blocks
Telegram writes and all local-state writes or creation, even when
`--allow-write` is present.

## Media subprocesses

For one message:

The `TG_*` variables below are synthetic placeholders populated from your own
account and destination.

```bash
tg --account "$TG_ACCOUNT_NAME" download-media \
  "$TG_CHAT_ID" "$TG_MESSAGE_ID" \
  --output "$TG_MEDIA_OUTPUT_DIR" \
  --max-size-mb 100 --allow-write --json
```

For a capped bulk backfill into the selected account's default media tree:

```bash
tg --account "$TG_ACCOUNT_NAME" backfill "$TG_CHAT_ID" \
  --max-messages 250 --download-media --max-media-size-mb 100 \
  --allow-write --json
```

Supported current types are photo, video, video note, voice, audio, sticker,
animation, and generic document. Photos use Telegram's selected largest
downloadable image; document-class media uses the original file stream.
Single downloads return `media_path`, `bytes`, and `skipped`; bulk results
return `media_downloaded`, `media_skipped`, `media_failed`, and `warnings`.

Treat a nonzero envelope with `committed: true` as a partial commit. Its
bounded recovery metadata may include validated artifact paths and byte counts;
inspect those before deciding whether to retry. An anchored regular file at the
final name is a successful no-overwrite skip, not a content-hash match.
`--overwrite` or `--overwrite-media` explicitly opts into safe atomic
replacement; unsupported atomic replacement fails safely. Size limit `0` means
unlimited.

## Exit codes

The process exit code matches the envelope error code family:

| Code | Name |
|---|---|
| 0 | OK |
| 1 | GENERIC |
| 2 | BAD_ARGS |
| 3 | NOT_AUTHED |
| 4 | NOT_FOUND |
| 5 | FLOOD_WAIT |
| 6 | WRITE_DISALLOWED |
| 7 | NEEDS_CONFIRM |
| 8 | LOCAL_RATE_LIMIT |
| 9 | PREMIUM_REQUIRED |

Use both the exit code and `.error.code`. The numeric code is stable
for shells; the string is better for logs and metrics.

## Request IDs

Every envelope includes `request_id`. Telegram write-pipeline audit entries use
the same ID for pre-call and final records:

```bash
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" "trace me" --allow-write --json |
  jq -r '.request_id'
```

Then correlate in `accounts/<account-name>/audit.log`:

```bash
rg "$TG_REQUEST_ID" "accounts/$TG_ACCOUNT_NAME/audit.log"
```

## Streaming

`tg listen --json` emits one envelope per update:

```bash
tg --account "$TG_ACCOUNT_NAME" listen --allow-write --json |
  jq -c 'select(.ok == true) | .data'
```

For tests, use one update and exit:

```bash
tg --account "$TG_ACCOUNT_NAME" listen --once --allow-write --json
```

## Versioning

The CLI contract is the API. New commands and fields are additive.
Existing exit codes, JSON envelope keys, and safety gates are stable
across the Go and Python ports.

## See also

- [Safety model](safety.md) — exit codes and audit log
- [Multi-account](multi-account.md) — isolating agents from your real DMs
- [Quickstart](quickstart.md) — agent-ready first run
