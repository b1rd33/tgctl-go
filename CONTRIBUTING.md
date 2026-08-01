# Contributing

`tgctl-go` is a Go port of Python `tgctl` with a deliberately flat command
surface, JSON-first output, and a safety pipeline around every Telegram write.
The longer contributor guide lives in [docs/contributing.md](docs/contributing.md);
this file is the repo-root checklist for day-to-day development.

## How This Project Is Structured

The CLI entrypoint is under [cmd/tg](cmd/tg). Command wiring and Cobra runners
live in [internal/commands](internal/commands), the Telegram client interface
and gotd/td implementation live in [internal/client](internal/client), and the
SQLite cache is in [internal/store](internal/store). Write commands should move
through [internal/writes](internal/writes) so write gates, idempotency, resolver
checks, dry runs, rate limiting, and audit records stay consistent.

## Development Setup

Start from a clean checkout and copy the example environment file:

```bash
cp .env.example .env
go test ./...
make build
```

The `.env` file supplies Telegram API credentials for live commands. Unit tests
do not need a Telegram session.

Use the local test gate before sending a PR:

```bash
go test ./... -count=1
go vet ./...
make public-hygiene
```

For broader confidence before a release or risky change:

```bash
go test -race ./... -count=1
mkdocs build --strict
```

## Adding A New Command

Read [internal/commands/messages_write.go](internal/commands/messages_write.go)
before adding a write command. That file shows the normal runner shape:
parse flags, build a payload preview, call `runWrite`, and let
`writes.Run` handle safety and auditing.

The write pipeline is intentionally centralized:

```text
write gate -> idempotency lookup -> resolver/fuzzy gate -> dry-run
  -> rate limit -> audit_pre -> Telegram call -> idempotency record
  -> audit_post
```

Every new write command needs:

- a `FakeClient` unit test that proves the runner builds the intended request
- a dry-run test when the command has safety-sensitive arguments
- a live exercise in [scripts/live_verify.sh](scripts/live_verify.sh) when the
  command can be tested without contacting unrelated people

Do not bypass the write pipeline for convenience. If the pipeline cannot model
the command, adjust the pipeline with tests rather than adding a one-off path.

## Tests

Run the fast suite while developing:

```bash
go test ./... -count=1
```

Run the race suite before release work, concurrency changes, or command plumbing
that opens clients, sessions, databases, or goroutines:

```bash
go test -race ./... -count=1
```

Add unit tests for every behavior change. If a bug is fixed in
`internal/client/gotd.go`, add the matching `*_test.go` case before the fix and
verify the test fails for the old behavior.

Add integration or live verification when:

- gotd/td request serialization changes
- Telegram server behavior is the thing being validated
- a write command targets topics, folders, admin actions, media, or destructive
  operations

Live tests must only touch Saved Messages or explicitly created temporary test
chats. They must clean up temporary Telegram state with traps or equivalent
failure-safe cleanup.

Live scripts must require test chat/account selectors through environment
variables. They must not default to a maintainer's account, session, username, or
local path, and raw transcripts/reports must stay outside the repository.
Each run uses a mode-0700 temporary workspace, creates raw files with mode 0600,
and removes the workspace on success, failure, or signal. Raw-output retention
environment variables are intentionally rejected.
The import/export simulation additionally requires an explicit dedicated forum
chat in `TGCTL_LIVE_FORUM_CHAT` and four ordered, comma-separated dedicated
folder targets in `TGCTL_LIVE_FOLDER_TARGETS`; it never discovers write targets
from the local cache.

Before filing an issue or opening a PR, remove Telegram phone numbers, usernames,
peer/message IDs, invite links, session/auth data, local database paths, message
contents, and downloaded media. Use synthetic placeholders. Report suspected
vulnerabilities through the private process in [SECURITY.md](SECURITY.md), not a
public issue.

## Local Live Verification

Live verification requires credentials in `.env` and an authenticated session in
`accounts/default/tg.session`.

```bash
bash scripts/live_verify.sh
bash scripts/import_export_simulation.sh
```

The broad live script favors dry runs for risky write surfaces. More targeted
release scripts may perform real Telegram round-trips, but they must document
what they touch and why it is safe.

## Documentation

Documentation is built with MkDocs Material:

```bash
pip install mkdocs-material
mkdocs serve
mkdocs build --strict
```

Keep root documentation short and link to deeper pages under `docs/`.

## Conventional Commits

Use Conventional Commits so changelog and release notes stay scannable.

| Prefix | Use for |
| --- | --- |
| `feat:` | User-visible commands, flags, or behavior |
| `fix:` | Bug fixes and behavioral corrections |
| `docs:` | Documentation only |
| `test:` | Tests, fixtures, and verification scripts |
| `chore:` | Maintenance that is not app behavior |
| `ci:` | GitHub Actions, Dependabot, and release automation |

For release bug fixes, use the phase form when applicable:

```text
fix(phase-N): concise subject
```

## Releasing

The release flow is tag-driven.

1. Update [CHANGELOG.md](CHANGELOG.md) with a `vX.Y.Z` section.
2. Commit the release prep.
3. Create an annotated tag:

```bash
git tag -a vX.Y.Z -m 'Release vX.Y.Z'
git push origin main
git push origin vX.Y.Z
```

Do not rewrite published release tags. If `v0.1.1` is already published and a
new fix is needed, release `v0.1.2`.

## Pull Requests

Keep PRs narrow. Include the command output for the checks you ran, note any
live verification skips, and link to the issue or release gap the change closes.

## License

By contributing, you agree that your contributions are licensed under the MIT
license used by this repository.
