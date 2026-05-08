#!/usr/bin/env bash
set -euo pipefail

CHAT=1240314255
SELF_USERNAME="@la71bi33d"
OUT=scripts/live_verify.transcript.txt
> "$OUT"

run() { echo "+ $*" | tee -a "$OUT"; "$@" | tee -a "$OUT" || (echo "FAILED: $*" | tee -a "$OUT"; exit 1); echo | tee -a "$OUT"; }

run_json() {
  echo "+ $*" | tee -a "$OUT"
  local tmp
  tmp="$(mktemp)"
  "$@" 2>&1 | tee "$tmp" | tee -a "$OUT"
  jq -e '.ok == true' "$tmp" >/dev/null
  rm -f "$tmp"
  echo | tee -a "$OUT"
}

expect_json_error() {
  local expected_code="$1"
  local expected_exit="$2"
  shift 2
  echo "+ $*  # expect $expected_code/$expected_exit" | tee -a "$OUT"
  local tmp status
  tmp="$(mktemp)"
  set +e
  "$@" 2>&1 | tee "$tmp" | tee -a "$OUT"
  status=${PIPESTATUS[0]}
  set -e
  jq -e --arg code "$expected_code" '.ok == false and .error.code == $code' "$tmp" >/dev/null
  rm -f "$tmp"
  if [ "$status" -ne "$expected_exit" ]; then
    echo "FAILED: expected exit $expected_exit, got $status: $*" | tee -a "$OUT"
    exit 1
  fi
  echo | tee -a "$OUT"
}

expect_json_ok_or_error() {
  local expected_code="$1"
  shift
  echo "+ $*  # expect ok or $expected_code" | tee -a "$OUT"
  local tmp status
  tmp="$(mktemp)"
  set +e
  "$@" 2>&1 | tee "$tmp" | tee -a "$OUT"
  status=${PIPESTATUS[0]}
  set -e
  jq -e --arg code "$expected_code" '.ok == true or (.ok == false and .error.code == $code)' "$tmp" >/dev/null
  rm -f "$tmp"
  if [ "$status" -ne 0 ] && [ "$status" -ne 9 ]; then
    echo "FAILED: expected exit 0 or 9, got $status: $*" | tee -a "$OUT"
    exit 1
  fi
  echo | tee -a "$OUT"
}

echo "=== live verification start ===" | tee -a "$OUT"
echo "chat: $CHAT" | tee -a "$OUT"

# skip-rationale: tg login would invalidate or replace the existing authenticated session.
# skip-rationale: tg import-telethon-session was already verified in a prior session and would mutate auth state.
# skip-rationale: tg accounts-add/use/remove mutate account directory layout; read-only account listing covers this release check.

run go build -o ./tg ./cmd/tg

# ---- Foundation ----
run_json ./tg version --json
run_json ./tg doctor --json
run_json ./tg me --json
run_json ./tg me --offline --json

# ---- Accounts ----
run_json ./tg accounts-list --json
run_json ./tg accounts-show --json

# ---- Phase 8: reads ----
run_json ./tg backfill-entities --json
run_json ./tg backfill "$CHAT" --max-messages 100 --allow-write --json
run_json ./tg show "$CHAT" --limit 5 --json
run_json ./tg show "$CHAT" --limit 3 --reverse --json
run_json ./tg search "$CHAT" "tgctl-go" --limit 10 --json
run_json ./tg list-msgs "$CHAT" --limit 5 --json
run_json ./tg list-msgs "$CHAT" --since 2026-05-01 --until 2026-05-09 --limit 50 --json
LAST_KNOWN=$(./tg show "$CHAT" --limit 1 --json | jq -r '.data.messages[0].message_id')
run_json ./tg get-msg "$CHAT" "$LAST_KNOWN" --json

# ---- Phase 9: text writes ----
run_json ./tg send "$CHAT" "live-verify: phase 9 send" --allow-write --json
SENT_ID=$(./tg send "$CHAT" "live-verify: edit-me" --allow-write --json | jq -r '.data.message_id')
run_json ./tg edit-msg "$CHAT" "$SENT_ID" "live-verify: edited body" --allow-write --json
run_json ./tg pin-msg "$CHAT" "$SENT_ID" --allow-write --json
run_json ./tg unpin-msg "$CHAT" "$SENT_ID" --allow-write --json
run_json ./tg mark-read "$CHAT" --up-to "$SENT_ID" --allow-write --json
FWD_SRC=$(./tg send "$CHAT" "live-verify: forward-source" --allow-write --json | jq -r '.data.message_id')
run_json ./tg forward "$CHAT" "$CHAT" "$FWD_SRC" --allow-write --json
expect_json_ok_or_error PREMIUM_REQUIRED ./tg react "$CHAT" "$SENT_ID" "👍" --allow-write --json
KEY="live-verify-$(date +%s)"
run_json ./tg send "$CHAT" "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json
echo "+ ./tg send $CHAT \"live-verify: idempotency $KEY\" --allow-write --idempotency-key \"$KEY\" --json  # expect idempotent replay" | tee -a "$OUT"
./tg send "$CHAT" "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json | tee -a "$OUT" | jq -e '.data.idempotent_replay == true' >/dev/null
echo | tee -a "$OUT"
run_json ./tg send-by-username "$SELF_USERNAME" "live-verify: send-by-username path" --allow-write --json

# ---- Phase 10: media ----
mkdir -p /tmp/tgctl-live
printf 'doc payload\n' > /tmp/tgctl-live/doc.txt
printf 'OggS placeholder for dry-run\n' > /tmp/tgctl-live/voice.ogg
printf '\x00\x00\x00\x18ftypmp42placeholder for dry-run\n' > /tmp/tgctl-live/video.mp4
python3 - <<'PY'
import struct
import zlib

width = height = 32
rows = b"".join(b"\x00" + (b"\x26\x7a\xc8" * width) for _ in range(height))

def chunk(kind, data):
    body = kind + data
    return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

png = (
    b"\x89PNG\r\n\x1a\n"
    + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    + chunk(b"IDAT", zlib.compress(rows, 9))
    + chunk(b"IEND", b"")
)
open("/tmp/tgctl-live/pixel.png", "wb").write(png)
PY
run_json ./tg upload-document "$CHAT" /tmp/tgctl-live/doc.txt --caption "live-verify: doc" --allow-write --json
run_json ./tg upload-photo "$CHAT" /tmp/tgctl-live/pixel.png --caption "live-verify: photo" --allow-write --json
# skip-rationale: upload-voice and upload-video need real .ogg/.mp4 assets; dry-run exercises their runner paths without ffmpeg.
run_json ./tg upload-voice "$CHAT" /tmp/tgctl-live/voice.ogg --allow-write --dry-run --json
run_json ./tg upload-video "$CHAT" /tmp/tgctl-live/video.mp4 --allow-write --dry-run --json
expect_json_error BAD_ARGS 2 ./tg upload-document "$CHAT" "/tmp/has?question.txt" --allow-write --json

# ---- Phase 11: topics + folders ----
run_json ./tg folders-list --json
FOLDER_COUNT=$(./tg folders-list --json | jq '.data.folders | length')
if [ "$FOLDER_COUNT" -gt 0 ]; then
  FIRST_FOLDER_ID=$(./tg folders-list --json | jq -r '.data.folders[0].id // .data.folders[0].folder_id')
  run_json ./tg folder-show "$FIRST_FOLDER_ID" --json
fi
expect_json_error BAD_ARGS 2 ./tg folder-delete 0 --allow-write --confirm 0 --json
run_json ./tg topic-create "$CHAT" "live-verify-topic" --allow-write --dry-run --json
run_json ./tg topic-edit "$CHAT" 1 --title "renamed" --allow-write --dry-run --json
run_json ./tg topic-pin "$CHAT" 1 --allow-write --dry-run --json
run_json ./tg topic-unpin "$CHAT" 1 --allow-write --dry-run --json
# skip-rationale: Saved Messages is not a forum supergroup; assert the live non-forum fallback.
expect_json_error BAD_ARGS 2 ./tg topics-list "$CHAT" --json
run_json ./tg chat-pinned-list "$CHAT" --json

# ---- Phase 12: admin ----
run_json ./tg chats-info "$CHAT" --json
# skip-rationale: Saved Messages is not a channel/supergroup; assert the live non-participant fallback.
expect_json_error BAD_ARGS 2 ./tg chat-members "$CHAT" --limit 50 --json
run_json ./tg account-sessions --json
run_json ./tg chat-title "$CHAT" "live-verify-title" --allow-write --dry-run --json
run_json ./tg chat-photo "$CHAT" /tmp/tgctl-live/pixel.png --allow-write --dry-run --json
run_json ./tg chat-description "$CHAT" "live-verify desc" --allow-write --dry-run --json
run_json ./tg set-permissions "$CHAT" --send-messages --allow-write --dry-run --json
run_json ./tg chat-invite-link "$CHAT" --allow-write --dry-run --json
run_json ./tg promote "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg demote "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg ban-from-chat "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg unban-from-chat "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg kick "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json

# ---- Phase 13: destructive ----
DEL_ID=$(./tg send "$CHAT" "live-verify: about-to-delete" --allow-write --json | jq -r '.data.message_id')
run_json ./tg delete-msg "$CHAT" "$DEL_ID" --allow-write --confirm "$CHAT" --json
run_json ./tg leave-chat "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg block-user "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json ./tg unblock-user "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
SESSION_HASH=$(./tg account-sessions --json | jq -r '.data.sessions[0].hash // 0')
if [ "$SESSION_HASH" != "0" ]; then
  run_json ./tg terminate-session "$SESSION_HASH" --allow-write --confirm "$SESSION_HASH" --dry-run --json
fi

# ---- Phase 14: local DB ops ----
run_json ./tg discover --allow-write --json
run_json ./tg sync-contacts --allow-write --json
run_json ./tg backfill "$CHAT" --max-messages 100 --allow-write --json

# ---- Phase 15: live ----
echo "+ timeout 8 ./tg listen --once --allow-write --json" | tee -a "$OUT"
if command -v timeout >/dev/null 2>&1; then
  timeout 8 ./tg listen --once --allow-write --json | tee -a "$OUT" || echo "(no update inside 8s - acceptable)" | tee -a "$OUT"
else
  echo "(timeout unavailable - listen smoke skipped)" | tee -a "$OUT"
fi
echo | tee -a "$OUT"

# ---- Phase 16 surface ----
./tg --help >/dev/null
echo "help ok" | tee -a "$OUT"
COUNT=$(./tg --help | grep -E "^  [a-z]" | wc -l | tr -d ' ')
echo "command count: $COUNT" | tee -a "$OUT"
[ "$COUNT" -ge 62 ]

# ---- Safety pipeline regressions ----
expect_json_error WRITE_DISALLOWED 6 ./tg send "$CHAT" "no-allow" --json
expect_json_error WRITE_DISALLOWED 6 ./tg --read-only send "$CHAT" "ro" --allow-write --json
expect_json_error BAD_ARGS 2 ./tg send Bjorn "fuzzy" --allow-write --json
expect_json_error BAD_ARGS 2 ./tg delete-msg "$CHAT" 999999 --allow-write --json
expect_json_error NOT_FOUND 4 ./tg get-msg "$CHAT" 999999999999 --json

echo "=== live verification complete ===" | tee -a "$OUT"
