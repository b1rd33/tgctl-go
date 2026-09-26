# Contributing

`tgctl-go` is a small project but contributions are welcome — bug
reports, PRs, doc fixes, new commands.

## Quick links

- **Report a bug** — <https://github.com/b1rd33/tgctl-go/issues>
- **Read the safety model** — [Safety](safety.md) before adding any write command
- **CHANGELOG.md** — version history

## Local setup

```bash
git clone https://github.com/b1rd33/tgctl-go
cd tgctl-go
go test ./...
go vet ./...
```

The project targets Go 1.25.

## Running the gate locally

```bash
go test ./... -count=1
go vet ./...
go test -race ./... -count=1
make public-hygiene
```

Before a commit, keep both `go test ./... -count=1` and `go vet ./...`
clean.

## Running live verification

Use an already authenticated account and an absolute path to a freshly built
binary. The default run only reads; writes must be enabled explicitly:

```bash
python3 scripts/live_verify.py --binary "$PWD/tg" --account test
python3 scripts/live_verify.py --binary "$PWD/tg" --account test --writes --media
```

`--writes` creates synthetic Saved Messages text, replies, forwards and pins.
`--media` also requires ffmpeg and exercises single photo/video/voice/document
transfers plus photo/video, audio and document albums. The harness verifies
identity before writing, uses normal session ownership, captures private results
in memory, prints labels only, and deletes every confirmed test message even on
failure. Unknown outcomes stop without retry; inspect the local write ledger.
It never copies sessions or consumes pending updates. Fixture files are temporary.

For the separate `scripts/live_permissions.sh` test, provision a disposable
group/channel and two distinct authorized accounts. Set
`TGCTL_LIVE_PERMISSION_CHAT`, `TGCTL_LIVE_ALLOWED_ACCOUNT`, and
`TGCTL_LIVE_DENIED_ACCOUNT` explicitly. It expects an allowed send and a denied
send returning `PERMISSION_DENIED` (exit 10). The operator must clean up this
fixture and its allowed probe; it never changes membership or intentionally
triggers a flood wait. A single-account Saved Messages run does not validate
server permission transitions, forum behavior, contact changes or logout.

Redact Telegram identities, phone numbers, peer/message IDs, invite links,
session/auth material, message contents, local paths, SQLite data, audit logs,
and downloaded media from issues and pull requests. Use synthetic placeholders.
Security reports belong in GitHub private vulnerability reporting as described
in the repository's `SECURITY.md`.

## Running the docs site locally

```bash
pip install mkdocs-material
mkdocs serve     # http://127.0.0.1:8000
```

## Conventions

- **Conventional Commits** — `feat|fix|docs|refactor|test|chore|perf|security|ci(scope): subject`
- **Docs commits** — use `docs:`
- **Docs workflow commits** — use `ci(docs):`
- **Audit log is append-only NDJSON** — pre + post entries share `request_id`

## Adding a new write command

Read the existing command runner and write pipeline end-to-end first.
The pipeline is fixed:

```
write gate → read text → idempotency lookup → resolver + fuzzy gate
  → dry-run short-circuit → rate limit → audit_pre → Telegram
  → record_idempotency → audit_post
```

Don't bypass any of these. The pattern is verbose but it's the
whole point of the project — every write hits the same gates,
auditable.

## Adding a new read command

Resolve the chat, query SQLite or Telegram, return a data dict.
The dispatch layer handles envelope + exit codes for you.

## Tests

Every new command should ship with a test. Smoke tests at minimum;
unit tests for any non-trivial transformation. gotd/td is not hit in
unit tests — use fake clients and assert the constructed payload.

## Releasing

The release flow is tag-driven. Push a `v*` tag, then GoReleaser builds
cross-platform archives and publishes the GitHub release.

## License

By contributing you agree your contributions will be MIT licensed.
