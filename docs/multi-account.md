# Multi-account

`tgctl-go` supports multiple Telegram accounts on the same machine
with isolated stores per account.

## Why isolate

Each account gets its own:

- gotd `tg.session` (auth token)
- `telegram.sqlite` (cached messages)
- `audit.log` (write trail)
- `media/` (downloaded file-backed Telegram media)
- `tg.session.lock` (concurrent-process guard)

This means a write you make as `work` cannot leak into `personal`'s
audit log, and a backfill on one account doesn't pollute the other's
SQLite. It also means losing or rotating one account's session
doesn't affect the other.

## Layout on disk

```
~/Projects/your-app/
├── accounts/
│   ├── default/
│   │   ├── tg.session
│   │   ├── telegram.sqlite
│   │   ├── audit.log
│   │   └── media/
│   ├── work/
│   │   ├── tg.session
│   │   ├── telegram.sqlite
│   │   ├── audit.log
│   │   └── media/
│   └── .current        ← which one is "default" right now
└── ...
```

If you don't use `accounts-add`, everything lives at
`accounts/default/`. The account name `default` is reserved and is
auto-created by the first permitted writable operation. Read-only mode never
creates it.

## Commands

### `tg accounts-add <name>`

Creates a new isolated account directory.

```bash
tg accounts-add work
```

Then log in as that account:

```bash
tg --account work login
```

`accounts-add` is a command, not a flag. This is valid:

```bash
tg accounts-add test
```

This is not:

```bash
tg --accounts-add test
```

### `tg accounts-list`

Lists all configured accounts, showing which is current.

```bash
tg accounts-list
```

### `tg accounts-show`

Show paths and current account.

```bash
tg accounts-show
# {"current": "default", "accounts": ["default", "work"], ...}
```

### `tg accounts-use <name>`

Set the default account selector. Every subsequent command without
an explicit `--account` uses this one.

```bash
tg accounts-use work
tg me
tg --account default me
```

### `tg accounts-remove <name>`

Delete an account directory. Permanent — back up the files first
if you want them.

```bash
tg accounts-remove old-test-account --confirm old-test-account
```

### `tg import-telethon-session <path>`

Adopt a Python `tgctl` / Telethon session as the current Go account's
session.

```bash
tg import-telethon-session "$TELETHON_SESSION_PATH"
```

Run `tg me` after import to verify the adopted account.

## Selecting an account at command time

Account selection has one exact precedence order, highest first:

1. `--account <name>` flag on the command
2. `TG_ACCOUNT=<name>` environment variable
3. The current selector at `accounts/.current`
4. Falls back to `default`

```bash
tg --account work send "$TG_CHAT_ID" "..." --allow-write
TG_ACCOUNT=work tg me
tg accounts-use work && tg me
```

## Test account walkthrough

For agent development, create a separate account directory and keep the
account selector explicit:

```bash
cd "$TGCTL_GO_CHECKOUT"
tg accounts-add test
tg --account test login
tg --account test me
tg --account test backfill-entities --allow-write
tg --account test discover --allow-write
tg --account test stats
```

The files are isolated under:

```text
accounts/test/tg.session
accounts/test/telegram.sqlite
accounts/test/audit.log
accounts/test/media/
```

Then test self-chat read/write:

```bash
tg --account test send "$TG_CHAT_ID" "hello from the sandbox account" --allow-write --json
tg --account test show "$TG_CHAT_ID" --limit 5 --json
```

If you rely on `.env`, run these from the directory containing `.env`.
If you run from `~/accounts/test` or another directory, export
`TG_API_ID` and `TG_API_HASH` first.

Agents should usually use `--account test` on every call. Use
`accounts-use test` only for interactive shell work where changing the
default account is intentional.

The default destination for `download-media` and media-enabled `backfill` is
also selected-account scoped:

```text
accounts/<account-name>/media/<chat-id>/
```

`download-media --output <directory>` overrides that destination for one
call. Account selection still controls which session, cache, and audit log are
used.

`--read-only` and `TG_READONLY=1` prevent both Telegram writes and local-state
writes or creation. They therefore do not create a missing account directory,
database, session, audit log, or media directory, even if `--allow-write` is
also present.

## Recommended use cases

- **Personal + business split** — keep work DMs in `accounts/work/` and your
  personal account untouched
- **Test accounts for development** — develop reply flows against
  `accounts/test/` with a throwaway phone number, then promote to
  `accounts/default/` once stable
- **Temporary backup account** — register a second SIM as `accounts/backup/`
  in case your main account hits a SpamBot restriction. Won't help recover the
  main, but lets you keep operating while you appeal.

## See also

- [Install](install.md) — initial `.env` and login
- [Safety model](safety.md) — per-account audit logs
- [Library use](sdk.md) — `--account` from agent subprocesses
