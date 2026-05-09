# Quickstart

Five commands to feel out the surface.

## 1. Sync a fast view of your dialogs

```bash
tg discover
```

Pulls every chat you have access to (DMs, groups, channels) into the
local SQLite cache. Just metadata — names, types, ids — not messages.
Fast.

## 2. Populate the entity cache

```bash
tg backfill-entities
```

This stores Telegram access hashes in `accounts/default/telegram.sqlite`
so chat-id-keyed writes work without resolving a username first.

## 3. Pull recent messages

```bash
tg backfill 1240314255 --max-messages 100
```

Pulls recent messages from your own chat into local SQLite. Use your
own `user_id` for Saved Messages, or replace it with another
`<your-chat-id>`.

For media:

```bash
tg backfill 1240314255 --max-messages 100 --download-media
```

Adds photos / voice notes / video / document files into
`accounts/default/media/<chat_id>/`.

## 4. Search what you've cached

```bash
tg search 1240314255 "shipping" --limit 20 --json
```

Substring search across cached message text. Returns a JSON envelope
with hits, chat ids, dates, and message ids.

## 5. Send a message

```bash
tg send 1240314255 "hello from tgctl-go" --allow-write
```

The `--allow-write` flag is required for any message that hits
Telegram. Without it the command exits with a clear error — this is
the [write gate](safety.md).

For multi-line text:

```bash
printf 'line one\nline two\n' | tg send 1240314255 - --allow-write
```

## What now?

- Browse the [full command reference](commands.md)
- Pipe JSON envelopes from [Library use](sdk.md)
- Read the [safety model](safety.md) before sending or deleting at scale
- Set up [multi-account](multi-account.md) if you have a personal + business split
