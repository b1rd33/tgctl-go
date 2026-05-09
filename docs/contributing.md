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
```

Before a commit, keep both `go test ./... -count=1` and `go vet ./...`
clean.

## Running live verification

Live smoke tests require a real Telegram account and credentials in
`.env`.

```bash
scripts/live_verify.sh
scripts/import_export_simulation.sh
```

`scripts/live_verify.sh` covers the command surface. The import/export
simulation creates 20 synthetic self-chat inquiries, groups them into
country queues, exercises folders, and mirrors summaries into forum
topics when a forum test chat is available.

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
