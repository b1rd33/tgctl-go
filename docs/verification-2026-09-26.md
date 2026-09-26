# Verification, 26 September 2026

Tests used the existing authorized account, with mutations restricted to
synthetic messages in Saved Messages. No customers were contacted. Reports
contain no account IDs, message IDs, captions, hashes, credentials or paths to
private account data. The installed v0.7.0 binary established the baseline;
the candidate was built from `codex/finish-cli-roadmap`.

| Check | Result and scope |
| --- | --- |
| Identity and selection | Cached and live `me` returned the same account; live `resolve self` matched it |
| Limits and remote reads | `account-limits`, bounded Saved Messages history, get and search returned successful envelopes; this does not test all account tiers or pagination boundaries |
| Basic writes | Send, identical idempotent replay, edit, explicit reply and forwarding of synthetic self messages succeeded; test messages were deleted |
| Formatted text | Sent text with a non-BMP emoji before a bold span; remote retrieval confirmed text and UTF-16 entity offsets; an italic edit replaced formatting and a plain edit cleared it |
| Single uploads | Synthetic PNG photo and document uploads returned IDs; remote text, entities and media presence matched the request |
| Exact hash lookup | Original upload digests located their messages. Document download bytes matched the original, and lookup survived removing the downloaded file |
| Albums | Two synthetic pictures shared a Telegram grouped ID; only the first caption carried formatting. Both downloaded successfully without index warnings, and each downloaded digest still found its message after local files were removed |
| Cleanup | Every synthetic message created by these tests was deleted, including an initially overlooked forwarded copy. Temporary fixtures and album downloads were removed |

The first download lookup assertion exposed a real regression: persisting a
download path cleared `media_id`, invalidating both original and downloaded
hash lookup. The fix publishes verified path and identity atomically and refuses
to overwrite a newer replacement or deletion. Store and command regressions
cover this behavior; the document and album live checks passed afterward.

The initial album formatting invocation also exposed a missing flag registration.
`--entities` is now registered and exercised through the actual command, in
addition to adapter tests. A clean-copy documentation test initially excluded
untracked new source files; staging the source made the test exercise the full
candidate, without weakening that check.

Reproduction uses a freshly built binary with an explicit account and bounded
timeouts: send uniquely labeled synthetic content to `self`, retrieve it with
`get-msg --source telegram --read-only`, compare values programmatically, then
delete only returned test IDs using the authenticated self confirmation. For
hash checks, generate fresh local fixtures, compute SHA-256, inspect `media-find`,
download with a small size cap, compare bytes, remove only the returned test
file, and repeat lookup. On uncertain sends, inspect before attempting a retry.
Never print raw result envelopes to a public test log.

Offline acceptance passed:

- `go test ./... -count=1`
- `go test -race ./internal/... -count=1`
- `go vet ./...`
- Native build and `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/tg`
- `make docs-commands-check public-hygiene`, `gofmt` and `git diff --check`

After the final unknown-identity guard was tightened, command/store race tests
were rerun. These checks are separate from live Telegram acceptance.

Still unverified live: contact add/remove, logout/re-login, archive/mute state,
forum and linked-discussion behavior, owner/admin/member/restricted transitions,
real reconnect gaps, and server flood/error conditions. No destructive session
or contact test was run against the working account. There is no claim that the
whole command matrix or all roadmap release gates have passed.

## Durable consumer acknowledgement follow-up

The candidate on `codex/durable-consumer-ack` passed a live restart test against
existing pending updates: two separate `listen --once --manual-ack` processes
returned the same event ID and receipt. Offline `events-list --limit 1` matched
that receipt; `events-ack --dry-run` reported it pending, and another list proved
it remained queued. No real pending event was acknowledged or removed, and raw
payloads/receipts were not printed. This verifies replay across process restarts,
not arrival of a newly generated incoming message or recovery of a network gap.
Actual queue deletion, repeat acknowledgement and concurrent acknowledgement
were verified with isolated synthetic databases.

Full unit and race suites, vet, native/Windows builds, command-reference checks,
formatting and diff checks passed for this follow-up. The bundled skill documents
the new consumer contract; the installed skill remains paired with the installed
release until a matching binary is installed.

## v0.8.1 acceptance follow-up

The committed `scripts/live_verify.py` reproduces bounded acceptance using an
explicit account and absolute candidate-binary path. `--writes --media` was run
in Saved Messages with small ffmpeg-generated JPEG, H.264/AAC MP4, OGG/Opus, MP3
and text fixtures. Private responses were parsed in memory, never printed.

Passed: matching cached/live identity and self resolution; account limits,
diagnostics and bounded history; write denial/read-only override and dry-run;
text send/replay/edit, reply root, forwarded content, remote search, pin/unpin
state; single photo/video/voice/document type and original-hash assertions;
non-photo downloaded bytes matching originals; mixed photo/video, audio-only
and document-only album IDs, shared grouping and first-item-only caption.
Every confirmed test message was deleted and checked remotely as deleted,
including cleanup of the initial failed run. No private updates were acknowledged.

The first expanded media check failed because reads classified videos as generic
documents. The new offline regression reproduced this across video, voice, audio,
sticker, animation and video-note inputs. Reusing the existing download classifier
fixed it; the complete live scope above then passed. Existing cached classifications
are not migrated and need refresh. Video duration/dimensions, full media-format
coverage and native Telegram rendering were not asserted by this run.

Permission regressions separately reproduce wrong selected-member roles, missing
self/default restrictions and administrator posting. Tests cover basic groups,
broadcasts, expiry, unknown participants, boost exemptions and gigagroups. These
are synthetic adapter tests, not proof of real server-side rights transitions.
Their semantics follow [Telegram rights](https://core.telegram.org/api/rights),
[restriction flags](https://core.telegram.org/constructor/chatBannedRights) and
[TDLib's participant logic](https://github.com/tdlib/td/blob/master/td/telegram/DialogParticipant.cpp).
Missing effective keys are unknown; other-member boost exemption can be unknown.
Slow-mode next-send metadata describes the authenticated account.

Remaining fixture and sponsored-display gates listed above are unchanged. This
report does not claim full roadmap or whole-command live acceptance.

Patch validation passed: 1,329 unit tests and the same full race suite across
17 packages; vet, native and Windows builds, formatting, generated command
references, dependency metadata, public repository hygiene and eight offline
live-harness safety tests. Native platform CI remains required before publication.
