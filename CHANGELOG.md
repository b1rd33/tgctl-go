# Changelog

## v0.1.3 — 2026-05-10

### Fixed

- `backfill` now paginates correctly. Previously it called
  `messages.GetHistory` exactly once with the user's `--max-messages` as
  the per-call limit, but Telegram caps that endpoint at 100 per call —
  so `--max-messages 1000` only returned 100 in practice.
  `BackfillMessages` now loops via `OffsetID` until the requested cap is
  reached or history runs out.
- FloodWait errors are now classified as exit code 5 (`FLOOD_WAIT`) with
  a `retry_after_seconds` field. Previously they fell through as exit 1
  (`GENERIC`) because the string-matcher looked for `FLOOD_WAIT_<n>`
  while gotd surfaces the typed error as `FLOOD_WAIT (n)`. Switched to
  `tgerr.AsFloodWait` for proper extraction. Same for
  `PREMIUM_ACCOUNT_REQUIRED` and `PHONE_*` / `AUTH_*` in `mapAuthErr`.

## v0.1.2 — 2026-05-09

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
