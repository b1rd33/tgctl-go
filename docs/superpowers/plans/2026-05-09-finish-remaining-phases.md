# Finish phases 10–15 against real Telegram

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish phases 10 (media), 11 (topics + folders), 12 (admin), 14 (local DB ops), and 15 (live + accounts already done; only `listen` remains) so the Go binary covers the full 62-command Python `tgctl` 1.0.1 surface.

**Architecture:** All infrastructure is already in place — `writes.Run` safety pipeline, `tg_entities` cache, `GotdClient` with `peerFromChatID`, FakeClient tests, dispatch envelope, audit log, idempotency. What remains is per-command gotd request marshaling and a few helper packages (`internal/media`, paged history iteration). Every new command runner follows the exact pattern in `internal/commands/messages_write.go` (Phase 9) and `internal/commands/destructive.go` (Phase 13).

**Tech Stack:** Go 1.23, gotd/td v0.144.0, Cobra, modernc.org/sqlite. No new top-level deps expected.

---

## Pattern reference (read these first, every new runner copies them)

- `internal/commands/messages_write.go` — `sendCommand` is the canonical write runner: cobra args → `runWrite(...)` → `writes.Run` pipeline → `client.Client` call. The gotd implementation lives in `internal/client/gotd.go` (e.g. `GotdClient.SendMessage`).
- `internal/commands/destructive.go` — `deleteMsgCommand` shows the typed-confirm gate pattern.
- `internal/client/gotd.go` — `peerFromChatID` is the chat_id → InputPeer adapter; every chat_id-keyed write goes through it.
- `internal/commands/backfill_entities.go` — shows how to wrap a one-shot gotd call (own `telegram.Client.Run`) without using the `Client` interface, and how to extend `tg_chats` + `tg_entities` from API responses.
- `tests/tgcli/*.py` (Python source at `/Users/christiannikolov/Projects/tg-cli/tests/tgcli/`) — behavioral spec. Every new Go test should port a Python test by name when one exists.

---

## Phase 10: Media uploads

**Files:**
- Create: `internal/media/media.go` (type detection + safe-path validation)
- Create: `internal/media/media_test.go`
- Create: `internal/commands/media.go` (4 cobra commands)
- Create: `internal/commands/media_test.go`
- Modify: `internal/client/client.go` (+ `UploadFileReq`, `UploadFileResp`, methods on `Client`)
- Modify: `internal/client/fake.go` (record uploads)
- Modify: `internal/client/gotd.go` (real `messages.SendMedia` via `uploader.Uploader`)
- Modify: `internal/store/messages.go` — extend so `MessageSummary` and the cache can record `media_path`/`media_type`/`has_media` after a successful upload
- Modify: `internal/commands/root.go` — replace the 4 stub registrations with real commands; remove media entries from `internal/commands/stubs.go`

**Commands (each goes through `writes.Run`):**
- `tg upload-photo <chat> <file> [--caption] [--silent] [--reply-to] [--idempotency-key] ...`
- `tg upload-voice <chat> <file>` (gotd: `tg.InputDocument`/`MessageMediaDocument` with `voice` flag, ogg only)
- `tg upload-video <chat> <file> [--caption] [--supports-streaming]`
- `tg upload-document <chat> <file> [--caption] [--filename]`

**Telethon parity (from `tgcli.commands.media`):**
- Reject paths with `?` and `#` via `_safe_user_path` (keep the same error wording)
- Detect media kind (`photo` | `voice` | `audio` | `video_note` | `video` | `sticker` | `image` | `document`) via the file extension/header — port `_media_type_of` 1:1
- Store downloaded media under `accounts/<name>/media/<chat_id>/<message_id><ext>`; uploads do not write the file but should record `tg_messages.media_path = <source_path>` so the local cache reflects what was sent
- Honor `--allow-write`, `--read-only`, `--fuzzy`, `--dry-run`, `--idempotency-key`, `--confirm` (when applicable)

**gotd hook:**
```go
import "github.com/gotd/td/telegram/uploader"
u := uploader.NewUploader(g.api)
file, err := u.FromPath(ctx, srcPath)
// then api.MessagesSendMedia(...) with InputMediaUploadedPhoto / Document
```

**Tests:**
- Port `tests/tgcli/test_phase12_media.py` 1:1 with FakeClient.
- Add a `safe-path` rejection test for `?` and `#`.
- Verify dry-run does not call `Uploader`.

---

## Phase 11: Topics + Folders

**Files:**
- Create: `internal/commands/topics.go`, `internal/commands/topics_test.go`
- Create: `internal/commands/folders.go`, `internal/commands/folders_test.go`
- Modify: `internal/client/client.go` — add `CreateTopicReq`, `EditTopicReq`, `PinTopicReq`, `FolderReq` types + methods
- Modify: `internal/client/fake.go` + `internal/client/gotd.go`
- Remove from `stubs.go`: 5 topic + 9 folder entries

**Topics (channels with forum=true):**
- `tg topic-create <chat> <title> [--icon-color] [--icon-emoji-id]` → `channels.CreateForumTopic`
- `tg topic-edit <chat> <topic-id> [--title] [--icon-emoji-id]` → `channels.EditForumTopic`
- `tg topic-pin <chat> <topic-id>` → `channels.UpdatePinnedForumTopic` with pinned=true
- `tg topic-unpin <chat> <topic-id>` → same with pinned=false
- `tg topics-list <chat> [--limit]` → `channels.GetForumTopics` (read-only, no write gate)

**Folders (dialog filters):**
- `tg folders-list` → `messages.GetDialogFilters`
- `tg folder-show <id>` → filter by id from the list above
- `tg folder-create <name> [--include-chats] [--exclude-chats] [--emoji]` → `messages.UpdateDialogFilter` with a new id
- `tg folder-edit <id> [--name] [--add] [--remove] [--emoji]` → same op with merged include/exclude
- `tg folder-delete <id>` → `messages.UpdateDialogFilter` with `Filter` empty (Python parity: id 0 is reserved → `BAD_ARGS`)
- `tg folder-add-chat <id> <chat>` / `tg folder-remove-chat <id> <chat>` → mutate include set
- `tg folders-reorder <id-csv>` → `messages.UpdateDialogFiltersOrder`
- `tg chat-pinned-list <chat>` → `messages.GetPinnedDialogs`-equivalent

**Python parity invariants (port from `tests/tgcli/test_phase61_topics.py`, `test_phase62_folders.py`):**
- Folder id 0 reserved for "All Chats" → reject with `BAD_ARGS`
- Topic writes require `--fuzzy` for non-int / non-@username chat selectors (existing fuzzy gate handles this)
- All write commands honor idempotency keys

---

## Phase 12: Admin

**Files:**
- Create: `internal/commands/admin.go`, `internal/commands/admin_test.go`
- Modify: `internal/client/client.go` (req types per command)
- Modify: `internal/client/fake.go` + `internal/client/gotd.go`
- Remove from `stubs.go`: 13 admin entries

**Commands and gotd request mapping:**

| Command                | gotd request                                              |
| ---------------------- | --------------------------------------------------------- |
| `chat-title <chat> <t>`| `channels.EditTitle` / `messages.EditChatTitle`           |
| `chat-photo <chat> <f>`| `channels.EditPhoto` / `messages.EditChatPhoto` (uploader)|
| `chat-description ..`  | `channels.EditAbout` / `messages.EditChatAbout`           |
| `set-permissions ..`   | `messages.EditChatDefaultBannedRights`                    |
| `chat-invite-link ..`  | `messages.ExportChatInvite`                               |
| `promote <chat> <u>`   | `channels.EditAdmin` with rights mask                     |
| `demote <chat> <u>`    | `channels.EditAdmin` with empty rights                    |
| `ban-from-chat ..`     | `channels.EditBanned`                                     |
| `kick <chat> <u>`      | `channels.EditBanned` with `viewMessages: false` then unset|
| `unban-from-chat ..`   | `channels.EditBanned` with empty rights                   |
| `chat-members <chat>`  | `channels.GetParticipants` (read, paged)                  |
| `chats-info <chat-csv>`| `channels.GetFullChannel` / `messages.GetFullChat` per id |
| `account-sessions`     | `account.GetAuthorizations` (already wired in `ListSessions`) — surface as a read command that prints the slice |

**Python parity:**
- Destructive admin variants (`ban-from-chat`, `kick`) require typed `--confirm <resolved-id>` like `delete-msg`.
- Non-destructive admin writes still need `--allow-write` and `--fuzzy` for title selectors.
- All audit logs include `telethon_method` matching the gotd request name (`channels.EditAdmin`, etc.).

---

## Phase 14: Local DB ops (full)

**Files:**
- Create: `internal/commands/localdb.go`, `internal/commands/localdb_test.go`
- Replace stubs: `backfill`, `discover`, `sync-contacts`

**`tg backfill <chat>`** (port `tgcli.commands.messages.run_backfill` precisely):
- Reject when `--read-only` or `TG_READONLY=1` even though it's local-write only
- Iterate `messages.GetHistory` paged (100 per page) until `--max-messages` is reached or the chat is exhausted
- Cap warnings at 80 % thresholds for `--max-messages` and `--max-db-size-mb`
- Reject when `current_msg_count >= --max-messages` BEFORE making any RPC call
- Optional `--download-media` writes files under `accounts/<name>/media/<chat_id>/<message_id><ext>` and records the path in `tg_messages.media_path`
- Throttle between chats (`--throttle-seconds`)
- JSON data fields: `chats_processed`, `messages_inserted`, `media_downloaded`, `skipped`, `per_chat`, `cap_warnings`
- Tests must include the cap rejection messages, warning strings, and `TG_READONLY=1` rejection (port `test_phase8_backfill_caps.py`)

**`tg discover [--limit]`**:
- Calls `messages.GetDialogs` (we already do this in `backfill-entities`); the difference is `discover` writes the chat metadata into `tg_chats` *and* returns a JSON summary
- Reuse the helpers from `backfill_entities.go`

**`tg sync-contacts`**:
- `contacts.GetContacts`
- Upsert into `tg_contacts(user_id, phone, first_name, last_name, username, is_mutual, synced_at)`
- Returns `synced` count

---

## Phase 15: Live (`listen`)

**Files:**
- Create: `internal/commands/live.go`, `internal/commands/live_test.go`
- Replace stub: `listen`

`tg listen` is the only Phase 15 piece left (multi-account is done):

- Subscribes to gotd updates via `telegram.Options.UpdateHandler` (or `tg.NewUpdateDispatcher`).
- For every `UpdateNewMessage` / `UpdateNewChannelMessage`, upsert chat + message into the cache, call `_media_type_of` if the message has media, optionally download to `accounts/<name>/media/...`.
- JSON event stream: emit one envelope per update with `command:"listen.event"` and `data: {update_kind, chat_id, message_id, ...}`.
- `--once` flag for tests / CI — exits after the first update.
- Honor `--read-only` (rejects, since listen mutates the local DB).

---

## Phase 16: completion polish

- Real `tg version` build-time injection: pass `-ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(git describe --tags --dirty)"` in `.goreleaser.yaml` and `go build` invocations.
- Stub-removal cleanup: when phases 10/11/12/14/15 finish, `internal/commands/stubs.go` should be deleted entirely.
- `go test -race ./...` on Linux + macOS in CI.
- Tag `v0.1.0` once everything is green.

---

## Verification (run at the end)

```bash
# 1. Build clean.
go vet ./... && go test ./... -count=1 && go build ./cmd/tg

# 2. Sanity smoke test against the existing logged-in account.
./tg backfill-entities --json | jq '.data.entities_cached'

# 3. Send + edit + react + delete (already verified for Phase 9 — should still pass).
./tg send 1240314255 "phase 10-15 acceptance run" --allow-write --json

# 4. New phases:
./tg upload-photo 1240314255 ./pic.jpg --caption "from upload-photo" --allow-write --json
./tg topics-list <forum-channel-id> --json
./tg folders-list --json
./tg chat-members <channel-id> --limit 50 --json
./tg backfill 1240314255 --max-messages 200 --allow-write --json
./tg sync-contacts --allow-write --json
./tg listen --once --json
```

End state: `tg --help` lists 62+ commands, `internal/commands/stubs.go` is gone, all tests pass.
