# Quickstart

Five commands to feel out the surface.

Examples use synthetic environment placeholders. Set these from your own
account before running them:

```bash
export TG_ACCOUNT_NAME="test"
export TG_CHAT_ID="<chat-id-from-your-own-account>"
export TG_MESSAGE_ID="<message-id-containing-media>"
```

If app credentials are not configured yet, run `tg setup` from the project
directory. It writes only `TG_API_ID` and `TG_API_HASH` into a mode-0600 `.env`
on Unix and preserves unrelated entries. Login can use `tg login --qr` in an
interactive terminal; QR login still requires the same app credentials.

## 1. Sync a fast view of your dialogs

```bash
tg --account "$TG_ACCOUNT_NAME" discover --allow-write
```

Pulls every chat you have access to (DMs, groups, channels) into the
local SQLite cache. Just metadata — names, types, ids — not messages.
Fast.

## 2. Populate the entity cache

```bash
tg --account "$TG_ACCOUNT_NAME" backfill-entities --allow-write
```

This stores Telegram access hashes in
`accounts/<account-name>/telegram.sqlite`
so chat-id-keyed writes work without resolving a username first.

## 3. Pull recent messages

```bash
tg --account "$TG_ACCOUNT_NAME" backfill "$TG_CHAT_ID" \
  --max-messages 100 --allow-write
```

Pulls recent messages from your selected account into local SQLite. Set
`TG_CHAT_ID` to a chat ID from your own account. `backfill` changes the
local cache, so it requires `--allow-write`.

For media:

```bash
tg --account "$TG_ACCOUNT_NAME" backfill "$TG_CHAT_ID" \
  --max-messages 100 --download-media \
  --max-media-size-mb 100 --allow-write --json
```

Adds supported file media into
`accounts/<account-name>/media/<chat-id>/`. An item that exceeds the
per-file limit is counted as failed with a safe warning while the remaining
backfill continues. Use `--overwrite-media` only when replacing existing
files is intentional.

To download a single message's media instead:

```bash
tg --account "$TG_ACCOUNT_NAME" download-media \
  "$TG_CHAT_ID" "$TG_MESSAGE_ID" \
  --allow-write --max-size-mb 100 --json
```

`TG_ACCOUNT_NAME`, `TG_CHAT_ID`, and `TG_MESSAGE_ID` are deliberately
synthetic placeholders that you set from your own account. The default output
is account-scoped; `--output <directory>` selects another directory. Files are
written through a private part file and published atomically. Without
`--overwrite`, an anchored regular file already at the final name is returned
as `skipped: true`; this is not a content-hash comparison. With `--overwrite`,
the CLI uses a safe atomic replacement or fails without a non-atomic fallback.

## 4. Search what you've cached

```bash
tg --account "$TG_ACCOUNT_NAME" search "$TG_CHAT_ID" "shipping" --limit 20 --json
```

Substring search across cached message text. Returns a JSON envelope
with hits, chat ids, dates, and message ids.

## 5. Send a message

```bash
tg --account "$TG_ACCOUNT_NAME" send "$TG_CHAT_ID" \
  "hello from tgctl-go" --allow-write
```

The `--allow-write` flag is required for any message that hits
Telegram. Without it the command exits with a clear error — this is
the [write gate](safety.md).

For multi-line text:

```bash
printf 'line one\nline two\n' | tg --account "$TG_ACCOUNT_NAME" \
  send "$TG_CHAT_ID" - --allow-write
```

## What now?

- Browse the [full command reference](commands.md)
- Pipe JSON envelopes from [Library use](sdk.md)
- Read the [safety model](safety.md) before sending or deleting at scale
- Set up [multi-account](multi-account.md) if you have a personal + business split

## Agent-ready first run

Use an isolated account name while testing agents. `accounts-add` is a
command; `--account test` is the global flag that selects that account.

```bash
tg accounts-add test
tg --account test login
tg --account test me
tg --account test backfill-entities --allow-write
tg --account test discover --allow-write
tg --account test stats
tg --account test send "$TG_CHAT_ID" "hello from the sandbox account" --allow-write --json
```

If login fails with `TG_API_ID and TG_API_HASH must be set`, run the
command from the directory containing `.env`, or export the variables.
See [Install](install.md#troubleshooting).

For agents, keep `--account test` explicit instead of switching the
default account. That makes every subprocess call self-contained.

Account selection is deterministic: `--account` wins over `TG_ACCOUNT`,
which wins over the persisted `accounts/.current` selector, followed by the
`default` fallback.

`--read-only` (or `TG_READONLY=1`) is stronger than omitting
`--allow-write`: it blocks Telegram writes and local state writes or creation,
including account paths, databases, sessions, and audit logs.

If a bulk media backfill reports an error with `committed: true`, inspect its
bounded recovery metadata and the reported paths before retrying. Some files
may already be durable even though database or client finalization failed.
