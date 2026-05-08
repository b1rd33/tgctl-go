/goal Harden tgctl-go and ship v0.1.0. Phases 1–15 are complete and green offline; what's missing is real-Telegram verification of the new commands, version injection, a minimal README, and a v0.1.0 tag. Stop only when (a) every smoke test below passes against the live account (Kris @la71bi33d, user_id 1240314255), (b) `go test -race ./...` is green on every package, (c) `./tg --version` prints a non-"dev" string when built with the release ldflags, (d) `README.md` documents install + login + a quick send example, (e) a `v0.1.0` annotated git tag exists, and (f) the final commit message is `chore(release): v0.1.0`.

## Working directory

`/Users/christiannikolov/Projects/tgctl-go/` — repo on branch main, the `.env` already has `TG_API_ID`/`TG_API_HASH`, the gotd session is at `accounts/default/tg.session`, and `tg me` (live) returns the user above.

## Required reading before changing code

- `docs/superpowers/plans/2026-05-09-finish-remaining-phases.md` — the just-finished plan; covers what every phase produced.
- `cmd/tg/main.go` — entry point + factory wiring.
- `internal/commands/doctor.go` — already exposes `Version`. Confirm it's the same global the ldflags target.
- `.goreleaser.yaml` — release config (already has `-X github.com/b1rd33/tgctl-go/internal/commands.Version=...`). The `go build` invocation in tests/dev should match.
- `.github/workflows/ci.yml` — make sure it runs `go test -race` on Linux + macOS.
- The Python `README.md` at `/Users/christiannikolov/Projects/tg-cli/README.md` — match the tone and section ordering for the Go README so users coming from PyPI see a familiar surface.

## Hard rules

1. **Never break a green test.** Before every commit, run `go test ./... -count=1` and `go vet ./...`. Both must stay clean.
2. **Live smoke tests only against `chat_id 1240314255`** (the user's own chat). Do not message other users. Treat anything else as scope expansion.
3. **No new top-level dependencies.** No new packages, no new modules. README is plain Markdown.
4. **Conventional Commits.** `chore:`, `docs:`, `fix:`, `test:`, `chore(release):` for the tag.
5. **Don't churn the audit log.** If a smoke test creates noise, document it as expected; do not redact rows.
6. **The `v0.1.0` tag must be annotated** (`git tag -a v0.1.0 -m ...`) and point at the final commit.

## Work order

### 1. Exhaustive live verification of every command against real Telegram

Goal of this step: exercise EVERY command surface against the user's real account so any bug not covered by the FakeClient unit tests gets surfaced now, before the v0.1.0 tag. Every Telegram-side write must target `chat_id 1240314255` (Kris talking to Kris) so no other user is messaged. Read commands and local-DB commands have no destination concerns.

Save the script as `scripts/live_verify.sh`, make it executable, run it, and commit the resulting transcript at `scripts/live_verify.transcript.txt` (created by piping the script's stdout/stderr to that file). The script must:

- Use `set -euo pipefail` so the first failure halts.
- Define `CHAT=1240314255`, `SELF_USERNAME="@la71bi33d"`, and `OUT=scripts/live_verify.transcript.txt` at the top, then `> "$OUT"`.
- Use `run() { echo "+ $*" | tee -a "$OUT"; "$@" | tee -a "$OUT" || (echo "FAILED: $*" | tee -a "$OUT"; exit 1); echo | tee -a "$OUT"; }` so every invocation is logged with its exit status.
- For each command below, assert the JSON envelope has `"ok":true` (or, where the contract requires failure, the expected error code) by piping through `jq -e '.ok == true'` or the matching predicate. A failed assertion = abort.

Command matrix (every `tg --help` entry must appear in this script at least once unless explicitly skipped under "deliberate skips" below):

```bash
go build -o ./tg ./cmd/tg

# ---- Foundation ----
run ./tg version --json
run ./tg doctor --json
run ./tg me --json
run ./tg me --offline --json

# ---- Accounts (already-tested flow, just regress) ----
run ./tg accounts-list --json
run ./tg accounts-show --json

# ---- Phase 8: reads ----
run ./tg backfill-entities --json    # pre-populate cache
run ./tg show $CHAT --limit 5 --json
run ./tg show $CHAT --limit 3 --reverse --json
run ./tg search $CHAT "tgctl-go" --limit 10 --json
run ./tg list-msgs $CHAT --limit 5 --json
run ./tg list-msgs $CHAT --since 2026-05-01 --until 2026-05-09 --limit 50 --json
LAST_KNOWN=$(./tg show $CHAT --limit 1 --json | jq -r '.data.messages[0].message_id')
run ./tg get-msg $CHAT $LAST_KNOWN --json

# ---- Phase 9: text writes ----
run ./tg send $CHAT "live-verify: phase 9 send" --allow-write --json
SENT_ID=$(./tg send $CHAT "live-verify: edit-me" --allow-write --json | jq -r '.data.message_id')
run ./tg edit-msg $CHAT $SENT_ID "live-verify: edited body" --allow-write --json
run ./tg pin-msg $CHAT $SENT_ID --allow-write --json
run ./tg unpin-msg $CHAT $SENT_ID --allow-write --json
run ./tg mark-read $CHAT --up-to $SENT_ID --allow-write --json
FWD_SRC=$(./tg send $CHAT "live-verify: forward-source" --allow-write --json | jq -r '.data.message_id')
run ./tg forward $CHAT $CHAT $FWD_SRC --allow-write --json
# react: graceful Premium fallback. If the account is Premium, expect ok=true;
# if not, expect ok=false AND .error.code == "PREMIUM_REQUIRED" (exit 9).
./tg react $CHAT $SENT_ID "👍" --allow-write --json | tee -a "$OUT" | \
    jq -e '.ok == true or .error.code == "PREMIUM_REQUIRED"' >/dev/null
# Idempotency: same key twice -> idempotent_replay:true on the second hit.
KEY="live-verify-$(date +%s)"
run ./tg send $CHAT "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json
./tg send $CHAT "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json | tee -a "$OUT" | \
    jq -e '.data.idempotent_replay == true' >/dev/null
# send-by-username route:
run ./tg send-by-username "$SELF_USERNAME" "live-verify: send-by-username path" --allow-write --json

# ---- Phase 10: media (each kind, all to self) ----
mkdir -p /tmp/tgctl-live
echo "doc payload" > /tmp/tgctl-live/doc.txt
# Tiny 1x1 png so we can exercise upload-photo without needing user assets:
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xfc\xff\xff?\x03\x00\x05\xfe\x02\xfe\xa3\x86\x9b\xa3\x00\x00\x00\x00IEND\xaeB`\x82' > /tmp/tgctl-live/pixel.png
run ./tg upload-document $CHAT /tmp/tgctl-live/doc.txt --caption "live-verify: doc" --allow-write --json
run ./tg upload-photo $CHAT /tmp/tgctl-live/pixel.png --caption "live-verify: photo" --allow-write --json
# upload-voice and upload-video need real .ogg / .mp4 input. If the script
# cannot synthesize one without ffmpeg, log it as a deliberate skip and DO
# NOT abort (still must touch the dry-run path so the runner code is exercised):
run ./tg upload-voice $CHAT /tmp/tgctl-live/doc.txt --allow-write --dry-run --json
run ./tg upload-video $CHAT /tmp/tgctl-live/doc.txt --allow-write --dry-run --json
# Safe-path rejection (must FAIL with BAD_ARGS=2):
./tg upload-document $CHAT "/tmp/has?question.txt" --allow-write --json | tee -a "$OUT" | \
    jq -e '.error.code == "BAD_ARGS"' >/dev/null

# ---- Phase 11: topics + folders ----
run ./tg folders-list --json
FOLDER_COUNT=$(./tg folders-list --json | jq '.data.folders | length')
if [ "$FOLDER_COUNT" -gt 0 ]; then
  FIRST_FOLDER_ID=$(./tg folders-list --json | jq -r '.data.folders[0].id')
  run ./tg folder-show "$FIRST_FOLDER_ID" --json
fi
# Folder id 0 is reserved -> BAD_ARGS:
./tg folder-delete 0 --allow-write --confirm 0 --json | tee -a "$OUT" | \
    jq -e '.error.code == "BAD_ARGS"' >/dev/null
# Topics: exercise dry-run since topics require a forum-enabled channel that
# the account may not own. The runner code path still gets covered.
run ./tg topic-create $CHAT "live-verify-topic" --allow-write --dry-run --json
run ./tg topic-edit $CHAT 1 --title "renamed" --allow-write --dry-run --json
run ./tg topic-pin $CHAT 1 --allow-write --dry-run --json
run ./tg topic-unpin $CHAT 1 --allow-write --dry-run --json
run ./tg topics-list $CHAT --json
run ./tg chat-pinned-list $CHAT --json

# ---- Phase 12: admin (most must be dry-run; the live ones target Saved Msgs) ----
run ./tg chats-info $CHAT --json
run ./tg chat-members $CHAT --limit 50 --json
run ./tg account-sessions --json
run ./tg chat-title $CHAT "live-verify-title" --allow-write --dry-run --json
run ./tg chat-photo $CHAT /tmp/tgctl-live/pixel.png --allow-write --dry-run --json
run ./tg chat-description $CHAT "live-verify desc" --allow-write --dry-run --json
run ./tg set-permissions $CHAT --send-messages --allow-write --dry-run --json
run ./tg chat-invite-link $CHAT --allow-write --dry-run --json
run ./tg promote $CHAT $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg demote $CHAT $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg ban-from-chat $CHAT $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg unban-from-chat $CHAT $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg kick $CHAT $CHAT --allow-write --confirm $CHAT --dry-run --json

# ---- Phase 13: destructive (delete-msg lands; the rest are dry-run) ----
DEL_ID=$(./tg send $CHAT "live-verify: about-to-delete" --allow-write --json | jq -r '.data.message_id')
run ./tg delete-msg $CHAT "$DEL_ID" --allow-write --confirm $CHAT --json
run ./tg leave-chat $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg block-user $CHAT --allow-write --confirm $CHAT --dry-run --json
run ./tg unblock-user $CHAT --allow-write --confirm $CHAT --dry-run --json
SESSION_HASH=$(./tg account-sessions --json | jq -r '.data.sessions[0].hash // 0')
if [ "$SESSION_HASH" != "0" ]; then
  run ./tg terminate-session "$SESSION_HASH" --allow-write --confirm "$SESSION_HASH" --dry-run --json
fi

# ---- Phase 14: local DB ops ----
run ./tg discover --json
run ./tg sync-contacts --allow-write --json
run ./tg backfill $CHAT --max-messages 100 --allow-write --json

# ---- Phase 15: live ----
timeout 8 ./tg listen --once --json | tee -a "$OUT" || \
    echo "(no update inside 8s — acceptable)" | tee -a "$OUT"

# ---- Phase 16 surface ----
run ./tg --help > /dev/null && echo "help ok"
COUNT=$(./tg --help | grep -E "^  [a-z]" | wc -l | tr -d ' ')
echo "command count: $COUNT" | tee -a "$OUT"
[ "$COUNT" -ge 62 ]

# ---- Safety pipeline regressions (must all surface the right exit codes) ----
./tg send $CHAT "no-allow" --json; [ $? -eq 6 ] && echo "WRITE_DISALLOWED ok"
./tg --read-only send $CHAT "ro" --allow-write --json; [ $? -eq 6 ] && echo "read-only blocks ok"
./tg send Bjorn "fuzzy" --allow-write --json; [ $? -eq 2 ] && echo "fuzzy gate ok"
./tg delete-msg $CHAT 999999 --allow-write --json; [ $? -eq 2 ] && echo "needs-confirm ok"
./tg get-msg $CHAT 999999999999 --json; [ $? -eq 4 ] && echo "not-found ok"

echo "=== live verification complete ===" | tee -a "$OUT"
```

Deliberate skips (must be commented in the script with `# skip-rationale: …`):
- `tg login` — would invalidate the existing session.
- `tg import-telethon-session` — already verified in a prior session.
- `tg accounts-add/use/remove` — would mutate the user's account directory layout. Read-only listing is enough.

Any failed `run` invocation halts the script. If the failure is caused by a bug in a Phase-10–15 runner, fix the bug in source, port a unit test that locks the corrected behavior, then re-run the script. If it's a real-Telegram environmental issue (e.g. rate limit, server-side outage), retry once with a 30-second sleep and proceed; if it still fails, document under CHANGELOG.md "Known limitations" and continue.

When the script completes successfully, commit the transcript and the script with message `test: live-verify acceptance run for v0.1.0`.

### 2. Version injection

- Confirm `Version` is `var Version = "dev"` in `internal/commands/doctor.go` (or wherever it lives).
- Update the `Makefile`/`scripts` (or add a `Makefile` if missing) so `make build` runs:
  ```
  go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(git describe --tags --dirty --always)" -o tg ./cmd/tg
  ```
- Add a `tg version` command (or extend the existing one) so it prints both the semver tag and the git short SHA in JSON: `{"version": "v0.1.0", "commit": "<sha>"}`. The plain `--version` flag on root should output the semver only for compatibility with conventional CLIs.
- Verify: `make build && ./tg version --json` should print a non-`"dev"` envelope after step 6 below tags v0.1.0. Before tagging, `git describe --tags --dirty --always` may print a sha; that's fine.

### 3. Race detector

- Run `go test -race ./... -count=1`. If any package fails on race, fix the data race in source (don't add a `// nolint:race` or similar). The most likely offenders: `internal/safety/ratelimit.go` (already mutex-protected), the `GotdClient.events` channel pattern, and `RootConfig.ExitCode` writes from cobra closures.
- Update `.github/workflows/ci.yml` so the race step runs on `ubuntu-latest` and `macos-latest` (already there per the Phase 16 commit; verify and adjust if not).

### 4. README

Create `/Users/christiannikolov/Projects/tgctl-go/README.md` with these sections, in this order:

1. **What it is** — one paragraph: Go port of Python `tgctl`, single static binary, same CLI contract, MIT license (match Python).
2. **Install** — `go install github.com/b1rd33/tgctl-go/cmd/tg@latest`, plus a Homebrew tap line marked `(coming soon)` if no tap is configured yet, plus a "download release binaries" pointer to GitHub Releases.
3. **Setup** — `cp .env.example .env`, fill `TG_API_ID` / `TG_API_HASH` from https://my.telegram.org/apps, run `tg login` (or `tg import-telethon-session <path>` if migrating from Python).
4. **Quick start** — three examples, copy-pasteable:
   ```
   tg backfill-entities
   tg send 1240314255 "hello" --allow-write
   tg show 1240314255 --limit 5
   ```
   Use a generic chat_id placeholder like `<your-chat-id>` rather than the real one.
5. **Safety contract** — bullet list of the 9 exit codes + the 4 gates (`--allow-write`, `--read-only`, `--fuzzy`, `--confirm`) + idempotency keys + audit log path. One line per item.
6. **Migrating from Python** — one paragraph + the `import-telethon-session` command.
7. **Status** — table of phases 1–16 with ✅ next to each.
8. **Contributing** — link the plan files in `docs/superpowers/plans/`.

Don't include emojis except the ✅ in the status table. Don't include marketing copy. Match the Python README's terseness.

Also create `.env.example` if missing, mirroring `/Users/christiannikolov/Projects/tg-cli/.env.example`.

### 5. CHANGELOG

Create `CHANGELOG.md`:

```
# Changelog

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
- See `docs/superpowers/plans/` for the full implementation history.
```

### 6. Tag and ship

When everything above is green:

```bash
git add -A
git commit -m "chore(release): v0.1.0"
git tag -a v0.1.0 -m "Initial Go port of Python tgctl"
```

Print the final summary: command count from `./tg --help`, total commits since project start (`git rev-list --count main`), total LOC (`tokei .` if available, else `find . -name '*.go' | xargs wc -l | tail -1`), and the absolute path to the tagged release-built binary (`./tg`).

Operate autonomously. If a smoke test surfaces a real bug in the new commands, fix it as part of this run — don't punt. If you genuinely cannot complete a step (e.g. GoReleaser dry-run requires network and credentials you don't have), document the gap in CHANGELOG.md under "Known limitations" and continue. Do not add new dependencies, new commands, or new tests beyond what this prompt asks for.

Begin.
