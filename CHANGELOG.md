# Changelog

## v0.1.1 — 2026-05-09

### Fixed

- `topic-create` now returns the created topic's id instead of the channel
  id. Without this, `tg send <chat> --topic <id>` could not target the topic
  produced by `topic-create`.
- `folder-create` / `folder-edit` now persist `--include-chats` and
  `--exclude-chats` into the Telegram-side `DialogFilter`. Folders were
  being created empty.

Both fixes verified live via `scripts/import_export_simulation.sh`.

## v0.1.0 — 2026-05-09

Initial Go port of Python tgctl 1.0.1.

### Added
- All 62 commands from Python tgctl 1.0.1, plus `send-by-username`,
  `import-telethon-session`, and `backfill-entities`.
- gotd/td live MTProto integration with on-disk session storage
  compatible with Telethon (via `import-telethon-session`).
- Multi-account isolation under `accounts/<name>/`.
- SQLite cache with `tg_chats`, `tg_messages`, `tg_contacts`,
  `tg_me`, `tg_idempotency`, `tg_entities`.
- Safety pipeline: write gate, idempotency replay, fuzzy gate,
  resolver, dry-run, sliding-window rate limiter, audit_pre.
- Cross-platform binaries via GoReleaser
  (Linux x86/arm64, macOS x86/arm64, Windows x86).

### Compatibility
- JSON envelope shape is byte-compatible with Python tgctl 1.0.1
  for every command both ports implement.
- Exit codes are stable across both ports (0=OK ... 9=PREMIUM_REQUIRED).

### Known limitations
- `tg login` creates a fresh session; existing Telethon sessions can
  be adopted via `tg import-telethon-session <path>`.
- Reactions that require Premium return PREMIUM_REQUIRED (exit 9).
- Forum topic reads require a forum-enabled supergroup.
- Chat member reads require a channel or supergroup.
- See the conventional-commits git log for the full implementation history.
