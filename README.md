# tgctl-go

## What it is

Go port of Python `tgctl`: a single static `tg` binary with the same CLI contract, JSON envelope, safety gates, local SQLite cache, and MIT license.

## Install

```bash
go install github.com/b1rd33/tgctl-go/cmd/tg@latest
```

```bash
brew install b1rd33/tap/tgctl-go # coming soon
```

Release binaries are published on GitHub Releases.

## Setup

```bash
cp .env.example .env
```

Fill `TG_API_ID` and `TG_API_HASH` from https://my.telegram.org/apps, then authorize the account:

```bash
tg login
```

Migrating from Python:

```bash
tg import-telethon-session <path>
```

## Quick start

```bash
tg backfill-entities
tg send <your-chat-id> "hello" --allow-write
tg show <your-chat-id> --limit 5
```

## Safety contract

- `0=OK`: command completed successfully.
- `1=GENERIC`: unexpected local or Telegram failure.
- `2=BAD_ARGS`: invalid arguments or unsafe input.
- `3=NOT_AUTHED`: missing or invalid Telegram session.
- `4=NOT_FOUND`: requested chat, message, user, folder, or session was not found.
- `5=FLOOD_WAIT`: Telegram asked the client to wait before retrying.
- `6=WRITE_DISALLOWED`: a write was blocked by the safety gate.
- `7=NEEDS_CONFIRM`: a destructive command is missing a matching typed confirmation.
- `8=LOCAL_RATE_LIMIT`: local sliding-window rate limit blocked the write.
- `9=PREMIUM_REQUIRED`: Telegram rejected a Premium-only operation.
- `--allow-write`: required for every Telegram-side write unless `TG_ALLOW_WRITE=1`.
- `--read-only`: rejects Telegram writes and local DB writers; `TG_READONLY=1` does the same.
- `--fuzzy`: allows title-like selectors for write commands.
- `--confirm`: must match the resolved id for destructive commands.
- Idempotency keys: pass `--idempotency-key <key>` to replay the prior successful write envelope instead of writing again.
- Audit log path: `accounts/<name>/audit.log`.

## Migrating from Python

Existing Python `tgctl` users can keep the same Telegram authorization by adopting the Telethon session into the Go account store:

```bash
tg import-telethon-session <path>
```

## Status

| Phase | Status |
| --- | --- |
| 1 | ✅ |
| 2 | ✅ |
| 3 | ✅ |
| 4 | ✅ |
| 5 | ✅ |
| 6 | ✅ |
| 7 | ✅ |
| 8 | ✅ |
| 9 | ✅ |
| 10 | ✅ |
| 11 | ✅ |
| 12 | ✅ |
| 13 | ✅ |
| 14 | ✅ |
| 15 | ✅ |
| 16 | ✅ |

## Contributing

See the plan files in [docs/superpowers/plans/](docs/superpowers/plans/).
