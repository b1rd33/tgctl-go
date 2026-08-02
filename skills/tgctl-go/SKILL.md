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

## Upload an album

Albums are 2–10 ordered files. Photo/video groups may be mixed; audio groups
and document groups must each be same-type. Use `--media-kind auto` (the
default) or force `photo`, `video`, `audio`, or `document`:

```bash
tg --account test upload-album 123 \
  ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --allow-write --dry-run --json

tg --account test upload-album 123 \
  ./01.jpg ./02.jpg ./03.mp4 \
  --caption "release images" --idempotency-key album-release-001 \
  --allow-write --json

tg --account test upload-album 123 \
  ./01.mp3 ./02.mp3 --media-kind audio \
  --caption "soundtrack" --allow-write --json
```

The caption is placed on the first item. The client uploads each file through
`messages.uploadMedia`, then makes one `messages.sendMultiMedia` call with a
unique random ID per item. The success response contains every message ID and
the shared `grouped_id` when Telegram returns it.

Telegram can return a video as `MessageMediaDocument` without setting its
response-level `Video` flag. The client therefore also checks
`DocumentAttributeVideo`; use a real video container/codec for live tests
because a tiny or malformed MP4 may legitimately be classified as a document.

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
skipped. Results are per-item and may be partial; rerun the group and rely on
cache checks rather than assuming an album is complete. Download has no
album-level idempotency key or individual-item selector. Captions and local
artifact paths are intentionally omitted from output.

If media writes finish but cache persistence/finalization, Telegram client
close, SQLite close, or durable audit finalization fails, expect a nonzero
committed failure with
`committed:true`, `partial:true`, `audit_failed` when applicable, and bounded
chat/item/message/group/counter metadata. Do not blindly retry: artifacts may
already exist. Inspect the cache and output, then make an explicit recovery
decision; `--overwrite` forces a fresh download.

## Backfill and inspect

```bash
tg --account test backfill 123 --allow-write --download-media --json
tg --account test show 123 --limit 50 --json
tg --account test search 123 invoice --limit 50 --json
tg --account test get-msg 123 456 --json
```

Backfill preserves Telegram `grouped_id`, de-duplicates overlapping history
pages, and reports `albums_seen`. Existing databases without the new column
remain readable in read-only mode; writable startup performs the migration but
cannot reconstruct old album membership. Run backfill after migration if
album grouping is needed. A dry-run reads the local cache and cannot discover
a remote album.

## Durable sync and local export

Use `sync` when a chat needs restart-safe catch-up plus optional live follow:

```bash
tg --account test sync 123 --allow-write --download-media --json
tg --account test sync 123 --follow --once --allow-write --json
```

The command stores a per-account/chat checkpoint in SQLite, persists each
backfill/live mutation before advancing that checkpoint, reconnects with
bounded exponential backoff, and preserves message edits, deletions, and
album `grouped_id` values. `--once` is useful for a deterministic probe; it
requires `--follow`. A failed local persistence step stops before the cursor
advances, so rerunning is safe.

Export is local-only and never constructs a Telegram client:

```bash
tg --account test export 123 --format jsonl --output ./chat.jsonl --include-media --json
tg --account test export 123 --format csv --since 2026-08-01 --limit 100
tg --account test export 123 --format html --output ./chat.html
```

Exports read the cached SQLite snapshot oldest-first, exclude tombstoned rows
by default, constrain media paths to the account media root, and refuse to
overwrite an existing output file. Add and verify a manifest locally:

```bash
tg --account test export 123 --format jsonl --output ./chat.jsonl \
  --manifest ./chat.manifest.json --manifest-hash --include-media
tg --account test export --verify ./chat.manifest.json --json
```

Verification reports stable `ARCHIVE_MISSING`, `ARCHIVE_CHANGED`, or
`ARCHIVE_EXTRA` codes. gotd's public downloader does not expose a safe resume
offset; rerun downloads instead of appending to a partial file.

For first-time credentials, use `tg setup`; it preserves unrelated `.env`
entries, writes mode `0600` on Unix, and never prints the API hash. `tg login
--qr` renders a QR in an interactive terminal; `--qr-uri` is an explicit
text-URI fallback. Both still require `TG_API_ID` and `TG_API_HASH`.

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

For a tagged distribution release, verify the generated Homebrew tap too:

```bash
brew update
brew info b1rd33/tap/tgctl-go
brew fetch b1rd33/tap/tgctl-go
if brew list --versions tgctl-go >/dev/null 2>&1; then
  brew upgrade b1rd33/tap/tgctl-go
else
  brew install b1rd33/tap/tgctl-go
fi
brew test b1rd33/tap/tgctl-go
"$(brew --prefix tgctl-go)/bin/tg" --version
```

GoReleaser publishes the GitHub archives, checksums, and tap formula from the
release tag. The binary's semver may be printed with or without a leading
`v`; the formula test must accept both forms.

For live verification, use Saved Messages or another disposable chat and a
valid JPEG/H.264 test fixture: confirm carousel order, caption placement,
returned IDs, shared `grouped_id`, backfill preservation, album-aware download,
idempotent replay, and dry-run zero Telegram calls. Exercise failure,
cancellation, size-limit, client-close, durable-audit, nonzero committed
envelope, bounded recovery metadata, and path/caption-leak cases locally with
fakes. Never push sessions, database files, audit logs, private paths, or
unreviewed public history.

## Known scope

Album download discovery remains cache/group based. Resumable transfers,
thumbnails, disk-space preflight, concurrency controls, and all-or-nothing
orchestration remain later hardening.
