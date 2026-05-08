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

### 1. Live smoke tests against real Telegram

Run each from a fresh shell (`./tg` rebuilt against latest main). All must succeed; capture the request_id from each JSON envelope and inspect `accounts/default/audit.log` afterwards to confirm the audit pre/post pair landed:

```bash
go build -o ./tg ./cmd/tg

# Phase 8 read regression — must still work:
./tg show 1240314255 --limit 5 --json | jq '.data.messages | length'

# Phase 9 regression — must still work:
./tg send 1240314255 "v0.1.0 acceptance run" --allow-write --json
LAST_ID=$(./tg send 1240314255 "edit-me-acc" --allow-write --json | jq -r '.data.message_id')
./tg edit-msg 1240314255 $LAST_ID "edited at acceptance" --allow-write --json
./tg delete-msg 1240314255 $LAST_ID --allow-write --confirm 1240314255 --json

# Phase 10 — media upload:
echo "smoke" > /tmp/up.txt
./tg upload-document 1240314255 /tmp/up.txt --caption "phase 10 acceptance" --allow-write --json | jq .

# Phase 11 — folders read (list shouldn't need any write gate):
./tg folders-list --json | jq '.data | keys'

# Phase 12 — chats-info read:
./tg chats-info 1240314255 --json | jq .

# Phase 14 — sync-contacts (this writes to local DB only, no Telegram-side mutation):
./tg sync-contacts --allow-write --json | jq '.data.synced'
./tg backfill 1240314255 --max-messages 100 --allow-write --json | jq '.data.messages_inserted'

# Phase 15 — listen (--once exits after first update; if no update arrives in 10s, that's OK):
timeout 10 ./tg listen --once --json || true
```

Any test that fails: investigate, fix, commit with `fix(phase-N):`. If a fix changes behavior visibly, port a unit test that locks the new behavior so it doesn't regress.

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
