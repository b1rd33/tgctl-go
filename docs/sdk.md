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
tg send 1240314255 "hello" --allow-write --json
```

Success:

```json
{
  "ok": true,
  "command": "send",
  "request_id": "req-abc12345",
  "data": {
    "message_id": 30350
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
  tg send 1240314255 "ack" --allow-write --json |
    jq -r '.data.message_id'
)

tg get-msg 1240314255 "$msg_id" --json |
  jq -e '.ok == true and .data.message.message_id == '"$msg_id"
```

## Subprocess integration

Any language can call `tg` and parse the envelope.

```python
import json
import subprocess

result = subprocess.run(
    ["tg", "show", "1240314255", "--limit", "5", "--json"],
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
tg --account test backfill-entities --json
tg --account test backfill 1240314255 --max-messages 100 --allow-write --json
tg --account test search 1240314255 "order" --limit 20 --json
tg --account test send 1240314255 "agent draft reply" \
  --allow-write \
  --idempotency-key "agent-reply-001" \
  --json
```

Reads do not need `--allow-write`. Local DB writes (`discover`,
`backfill`, `sync-contacts`, `listen`) and Telegram writes do. Treat
`request_id` as the correlation key for logs and audit entries.

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

Every envelope includes `request_id`. Write audit entries use the same
ID before and after the Telegram call:

```bash
tg send 1240314255 "trace me" --allow-write --json |
  jq -r '.request_id'
```

Then correlate in `accounts/default/audit.log`:

```bash
rg 'req-abc12345' accounts/default/audit.log
```

## Streaming

`tg listen --json` emits one envelope per update:

```bash
tg listen --json | jq -c 'select(.ok == true) | .data'
```

For tests, use one update and exit:

```bash
tg listen --once --json
```

## Versioning

The CLI contract is the API. New commands and fields are additive.
Existing exit codes, JSON envelope keys, and safety gates are stable
across the Go and Python ports.

## See also

- [Safety model](safety.md) — exit codes and audit log
- [Multi-account](multi-account.md) — isolating agents from your real DMs
- [Quickstart](quickstart.md) — agent-ready first run
