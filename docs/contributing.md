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

Live smoke tests require a real Telegram account and credentials in
`.env`.

```bash
scripts/live_verify.sh
scripts/import_export_simulation.sh
scripts/live_permissions.sh
```

`scripts/live_verify.sh` covers the command surface. The import/export
simulation creates 20 synthetic self-chat inquiries, groups them into
country queues, exercises folders, and mirrors summaries into forum
topics when a forum test chat is available.

Set the required `TGCTL_LIVE_*` variables to dedicated test targets before
running these scripts. They fail closed when required targets are absent and
write raw output to a private temporary workspace that is removed on exit.
Raw-output retention variables are intentionally rejected. Never commit live
output.
For the permission smoke test, set `TGCTL_LIVE_PERMISSION_CHAT`,
`TGCTL_LIVE_ALLOWED_ACCOUNT`, and `TGCTL_LIVE_DENIED_ACCOUNT` to two already
authorized accounts and a disposable group/channel. The script sends one
uniquely marked message from the allowed account and expects the denied account
to return `PERMISSION_DENIED` (exit 10). It never deliberately creates a flood
wait and never bans, promotes, deletes, or mass-messages.
For the import/export simulation, set `TGCTL_LIVE_FORUM_CHAT` explicitly and set
`TGCTL_LIVE_FOLDER_TARGETS` to four ordered, comma-separated dedicated test chat
IDs. The script does not infer write targets from cached dialogs.

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
