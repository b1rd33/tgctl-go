---
name: tgctl-go
description: Use when operating the tgctl-go Telegram CLI for account-scoped messages, media albums, backfills, downloads, retries, or release verification
---

# tgctl-go Telegram CLI

Use the `tg` binary for scripted Telegram user-account operations. Commands
emit a stable JSON envelope; prefer `--json` for automation and keep account
state, sessions, SQLite caches, and audit logs outside source control.

## Safety gates

- Pass `--account <name>` when more than one account exists.
- Prefer numeric chat IDs or `@username`. A title-like selector requires
  `--fuzzy`; never infer a title match for a write.
- Telegram-side writes require `--allow-write`. `--read-only` wins over it.
- `upload-album` and `download-album` dry-runs require the write gate but make
  no Telegram call; they return metadata only. Inspect the JSON before writing.
- Do not put source paths, captions, API credentials, session files, or raw
  audit lines in prompts, logs, commits, or public issues.

## Upload a photo/video album

Albums are 2–10 ordered photo/video files. Validate every file before the
network call, then preview and send with a stable idempotency key:

```bash
tg --account test upload-album 123 \
  ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --allow-write --dry-run --json

tg --account test upload-album 123 \
  ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --idempotency-key album-release-001 \
  --allow-write --json
```

The caption is placed on the first item. The client uploads each file through
`messages.uploadMedia`, then makes one `messages.sendMultiMedia` call with a
unique random ID per item. The success response contains every message ID and
the shared `grouped_id` when Telegram returns it.

Reuse a key only for the identical account/chat, ordered files, file hashes,
caption, and options. A changed request is rejected; a pending or unknown
final send must not be retried blindly. Temporary uploads are not
transactional, so an earlier upload can remain if a later upload fails.

## Download an existing album

Album download works from cached/backfilled rows, selected by `--grouped-id`
or an anchor message ID:

```bash
tg --account test download-album 123 --grouped-id 9001 \
  --allow-write --dry-run --json
tg --account test download-album 123 --grouped-id 9001 \
  --output ./album-9001 --max-size-mb 100 --allow-write --json
```

An ungrouped anchor is a stable `not-an-album` error. Use `--overwrite` to
redownload existing artifacts; without it, only a verified cached artifact is
skipped. Results are per-item and may be partial; rerun failed items rather
than assuming an album is complete. Download has no album-level idempotency
key. Captions and local artifact paths are intentionally omitted from output.

## Backfill and inspect

```bash
tg --account test backfill 123 --allow-write --download-media --json
tg --account test show 123 --limit 50 --json
tg --account test search 123 invoice --limit 50 --json
tg --account test get-msg 123 456 --json
```

Backfill preserves Telegram `grouped_id`, de-duplicates overlapping history
pages, and reports `albums_seen`. Existing databases without the new column
remain readable in read-only mode; writable startup performs migrations.

## Verification and release hygiene

Before calling a change ready, run:

```bash
GOCACHE=/private/tmp/tgctl-gocache go test ./...
GOCACHE=/private/tmp/tgctl-gocache-race go test -race ./...
GOCACHE=/private/tmp/tgctl-gocache-vet go vet ./...
GOCACHE=/private/tmp/tgctl-gocache-build go build ./...
GOCACHE=/private/tmp/tgctl-gocache-docs go test ./tools/gen_commands_md -count=1
scripts/check_public_hygiene.sh
```

For live verification use a disposable account: confirm carousel order,
caption placement, returned IDs, shared `grouped_id`, backfill preservation,
dry-run zero network calls, partial failures, cancellation, size limits, and
safe retries. Never push sessions, database files, audit logs, private paths,
or unreviewed public history.

## Known scope

Album v1 intentionally covers photo/video albums only. Audio/document albums,
resumable transfers, manifests, thumbnails, disk-space preflight,
all-or-nothing orchestration, and album-level download discovery are later
hardening, not prerequisites for the current commands.
