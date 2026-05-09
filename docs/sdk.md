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
