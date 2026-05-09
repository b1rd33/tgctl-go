# tgctl-go

**Agent-friendly Telegram CLI** built on [gotd/td](https://github.com/gotd/td).

Read, write, archive, and listen to your own Telegram account from a
single static `tg` binary — with JSON envelope output, idempotency
keys, audit logging, multi-account stores, and explicit safety gates
designed for autonomous agent use.

```bash
go install github.com/b1rd33/tgctl-go/cmd/tg@latest
tg login
tg --help    # 70 commands
```

## What is `tgctl-go`?

`tgctl-go` is a Go port of Python [`tgctl`](https://github.com/b1rd33/tg-cli).
It drives your **personal Telegram account** through MTProto, exposes
the same CLI contract and stable exit codes, stores a local SQLite
mirror for offline queries, and ships as one binary with no Python
runtime.

It's **not a bot framework**. Bots run on Telegram's separate Bot API
and never see private chats. `tgctl-go` is for *your own account* —
read your DMs, search archives, draft and send replies, manage
groups you administer, listen for incoming events.

## Why this exists

Telegram already has Pyrogram, Telethon, gotd/td, and Telegram
Desktop. This project sits in a specific gap:

| Need | Existing tool | Why `tgctl-go` |
|---|---|---|
| Read/send from a script | gotd/td | Lower-level — you write the CLI yourself |
| Python-native CLI | tg-cli | Same contract, but requires Python |
| Interactive terminal client | nchat | TUI, not scriptable |
| MCP for Claude | chigwell/telegram-mcp | Online-only, no offline cache, no safety gates |
| Bulk export | iyear/tdl | Different category — no safety pipeline for account writes |

`tgctl-go` is the standalone static binary with the **safety
architecture**: typed `--confirm <chat-id>` on destructive ops,
idempotency keys for safe retries, pre/post audit logging with shared
request IDs, and opt-in `--fuzzy` so an agent can't fat-finger which
chat to address.

## Quick links

- [Install](install.md) — `go install`, release binaries, `.env` setup
- [Quickstart](quickstart.md) — first 5 commands
- [Commands](commands.md) — full 70-command reference
- [Library use](sdk.md) — JSON envelope as the cross-language SDK surface
- [Safety model](safety.md) — write gate, typed confirm, idempotency, audit log
- [Multi-account](multi-account.md) — isolated stores per account
- [Contributing](contributing.md) — for code contributors

## Status

- **v0.1.0** on GitHub Releases
- 70 CLI commands across read / write / destructive / media / admin
- CI runs tests, vet, and race detector on Ubuntu + macOS
- MIT licensed
