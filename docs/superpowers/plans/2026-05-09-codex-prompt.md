/goal Finish phases 10–15 of the tgctl-go rewrite (media uploads, topics+folders, admin, local DB ops, listen) so the Go binary covers the full Python tgctl 1.0.1 command surface. Stop only when every command listed in `internal/commands/stubs.go` has been replaced with a real implementation, `stubs.go` itself is deleted, `go test ./...` is green on every package, `go vet ./...` is clean, and `./tg --help` lists at least 62 commands with no "[stub]" prefixes.

## Context

Working directory: `/Users/christiannikolov/Projects/tgctl-go/`. This is a Go rewrite of the shipped Python `tgctl` 1.0.1 (PyPI) whose source lives READ-ONLY at `/Users/christiannikolov/Projects/tg-cli/`. Don't modify the Python repo — it's the behavioral spec.

Phases 1–9 + 13 are already done and verified end-to-end against real Telegram (account: Kris @la71bi33d, user_id 1240314255). The full plan for the work you're about to do is at `docs/superpowers/plans/2026-05-09-finish-remaining-phases.md` — read it first; it lists every command, gotd request, file path, and Python test to port.

## Required reading before you write code

- `docs/superpowers/plans/2026-05-09-finish-remaining-phases.md` (the plan).
- `internal/commands/messages_write.go` — pattern reference for write runners (Phase 9 canonical).
- `internal/commands/destructive.go` — pattern reference for typed-confirm gates.
- `internal/client/gotd.go` — every chat_id-keyed write goes through `peerFromChatID`; copy the call style for `messages.EditMessage`, `MessagesForwardMessages`, etc., when adding new methods.
- `internal/commands/backfill_entities.go` — pattern for one-shot gotd calls that own their own `telegram.Client.Run` (use this for `discover`, `sync-contacts`, `backfill`).
- `internal/writes/pipeline.go` — the safety pipeline order is fixed; do not reorder.
- `/Users/christiannikolov/Projects/tg-cli/tgcli/commands/messages.py`, `media.py`, `chats.py`, `admin.py`, `contacts.py`, `events.py` — Python reference. Match the JSON `data` shape and warning strings byte-for-byte where reasonable.
- `/Users/christiannikolov/Projects/tg-cli/tests/tgcli/test_phase12_media.py`, `test_phase61_topics.py`, `test_phase62_folders.py`, `test_phase13_admin.py`, `test_phase8_backfill_caps.py`, `test_phase8_readonly.py` — port each by name to the matching Go file.

## Hard rules

1. **Never break a green test.** After every commit, `go test ./... -count=1` and `go vet ./...` must stay clean. If a refactor breaks tests, fix them in the same commit.
2. **One phase per branch isn't required, but one commit per logical step is.** Conventional Commits messages: `feat(phase-10):`, `feat(phase-11):`, etc.
3. **Every new write command flows through `writes.Run`.** Don't bypass the safety pipeline. The order is: write gate → idempotency lookup → fuzzy gate → resolve → dry-run → rate limit → audit_pre → Telegram call → idempotency record. Reorder = behavior change = test failure.
4. **Every chat_id-keyed Telegram call goes through `GotdClient.peerFromChatID`.** When you encounter a peer the cache doesn't know, surface `*safety.BadArgs` with the message `"no cached access_hash for chat_id N; run \`tg backfill-entities\` once or use \`tg send-by-username @name\`"`. Don't silently call `ContactsResolveUsername` — that path is reserved for `send-by-username`.
5. **Every new gotd method on `Client` gets a FakeClient counterpart in `internal/client/fake.go`** so command tests stay offline. The FakeClient should record every call into a slice the test asserts on.
6. **Every new command runner gets a test that asserts on the JSON envelope, exit code, and audit log entry.** Use the existing `runRoot(t, cfg, args...)` helper from `internal/commands/messages_write_test.go`.
7. **Don't break the existing audit invariants.** `phase: "before"` entry + post-call entry must share the same `request_id`. Already verified for Phase 9; new commands inherit this through `writes.Run` automatically — just don't bypass it.
8. **Idempotency replay is mandatory for every new write command.** Test it: same key → second call returns `idempotent_replay: true` without hitting the FakeClient. The pipeline already does this; just verify with a test.
9. **Stub-removal is part of "done."** When you finish a phase, delete the corresponding entries from `internal/commands/stubs.go`. When all five phases are done, delete `stubs.go` itself.
10. **Don't expand scope.** No new top-level dependencies (gotd/td v0.144.0, modernc.org/sqlite, cobra, x/term are enough). No new Cobra subcommand groupings — keep the flat command surface that matches Python.

## Work order

1. **Phase 10 (media)** — adds `internal/media/` (file detection + safe-path validation, port `_media_type_of` and `_safe_user_path` from Python) and 4 commands. Use `gotd/telegram/uploader.NewUploader(g.api)`. Test with FakeClient first, then run `./tg upload-photo 1240314255 <some.jpg> --caption "test" --allow-write --json` against the real account.
2. **Phase 14 (local DB)** — `discover` reuses `backfill-entities` shape. `sync-contacts` is `contacts.GetContacts` + UPSERT. `backfill` is the heaviest: paged `messages.GetHistory` with the cap warnings the Python tests assert. Port `test_phase8_backfill_caps.py` faithfully.
3. **Phase 11 (topics+folders)** — straightforward request mapping. `channels.CreateForumTopic`, `messages.UpdateDialogFilter`. Folder id 0 is reserved → `BAD_ARGS`. Port the `_phase62_folders.py` reserved-id test.
4. **Phase 12 (admin)** — 13 commands; mostly thin gotd wrappers. Destructive variants (`ban-from-chat`, `kick`) need typed `--confirm <resolved-id>`. `chats-info` accepts a comma-separated id list and pages through.
5. **Phase 15 (listen)** — subscribe to `tg.NewUpdateDispatcher`; emit one envelope per update with `command: "listen.event"`. `--once` exits after first update so tests are deterministic.

## Acceptance commands (run after every phase, must all succeed)

```bash
go vet ./...
go test ./... -count=1 -race
go build -o ./tg ./cmd/tg

# Phase-10 smoke:
echo "test" > /tmp/up.txt
./tg upload-document 1240314255 /tmp/up.txt --caption "phase 10 ok" --allow-write --json | jq .

# Phase-11 smoke:
./tg folders-list --json | jq '.data.folders | length'

# Phase-12 smoke:
./tg chats-info 1240314255 --json | jq .

# Phase-14 smoke:
./tg backfill 1240314255 --max-messages 50 --allow-write --json | jq '.data.messages_inserted'
./tg sync-contacts --allow-write --json | jq '.data.synced'

# Phase-15 smoke (run, send a message from another device, verify event prints):
timeout 10 ./tg listen --once --json || true

# Final acceptance:
test ! -f internal/commands/stubs.go || (echo "stubs.go still exists" && exit 1)
./tg --help | grep -c "^  [a-z]" | awk '$1 < 62 {exit 1}'
```

## When you're done

- All tests green, `stubs.go` deleted, 62+ commands in help.
- Append a final commit `chore: complete phases 10-15, delete stubs.go` and stop.
- Print a short summary: which commands were added per phase, total commits, total LOC delta vs the start of this run.

Operate autonomously. The user has a real Telegram session at `accounts/default/tg.session` and credentials in `.env` so live verification works. If a smoke test would hit Telegram and you have a more precise unit test that already covers the same behavior, prefer the unit test to avoid rate-limit risk on the user's account. If you must hit Telegram for verification, send only to chat_id 1240314255 (Saved Messages equivalent — the user's own chat with themselves).

Begin.
