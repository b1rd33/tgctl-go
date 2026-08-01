#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/live_test_common.sh"
live_workspace_init live-verify

CHAT="${TGCTL_LIVE_CHAT:?Set TGCTL_LIVE_CHAT to an isolated test chat or Saved Messages ID}"
SELF_USERNAME="${TGCTL_LIVE_SELF_USERNAME:?Set TGCTL_LIVE_SELF_USERNAME to the authenticated test account username}"
export TG_ACCOUNT="${TGCTL_LIVE_ACCOUNT:?Set TGCTL_LIVE_ACCOUNT to a dedicated authenticated test account}"
if [ -n "${TGCTL_LIVE_OUTPUT:-}" ]; then
  printf 'TGCTL_LIVE_OUTPUT retention is disabled; raw live output remains ephemeral\n' >&2
  exit 2
fi
live_require_numeric_selector TGCTL_LIVE_CHAT "$CHAT"
live_require_username TGCTL_LIVE_SELF_USERNAME "$SELF_USERNAME"
if [[ ! "$TG_ACCOUNT" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]]; then
  printf 'TGCTL_LIVE_ACCOUNT must name an explicit test account\n' >&2
  exit 2
fi
OUT="$LIVE_WORKSPACE/transcript.txt"
MEDIA_DIR="$LIVE_WORKSPACE/media"
mkdir -m 700 "$MEDIA_DIR"
export MEDIA_DIR
> "$OUT"
chmod 600 "$OUT"

run() { echo "+ $*" | tee -a "$OUT"; "$@" | tee -a "$OUT" || (echo "FAILED: $*" | tee -a "$OUT"; exit 1); echo | tee -a "$OUT"; }

run_json() {
  echo "+ $*" | tee -a "$OUT"
  local tmp
  tmp="$(mktemp "$LIVE_WORKSPACE/json.XXXXXX")"
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
  tmp="$(mktemp "$LIVE_WORKSPACE/json.XXXXXX")"
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
  tmp="$(mktemp "$LIVE_WORKSPACE/json.XXXXXX")"
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

live_prepare_tg "$PROJECT_ROOT" "$PROJECT_ROOT"
TG=("$LIVE_TG_LAUNCHER")

# ---- Foundation ----
run_json "${TG[@]}" version --json
run_json "${TG[@]}" doctor --json
run_json "${TG[@]}" me --json
run_json "${TG[@]}" me --offline --json

# ---- Accounts ----
run_json "${TG[@]}" accounts-list --json
run_json "${TG[@]}" accounts-show --json

# ---- Phase 8: reads ----
run_json "${TG[@]}" backfill-entities --json
run_json "${TG[@]}" backfill "$CHAT" --max-messages 100 --allow-write --json
run_json "${TG[@]}" show "$CHAT" --limit 5 --json
run_json "${TG[@]}" show "$CHAT" --limit 3 --reverse --json
run_json "${TG[@]}" search "$CHAT" "tgctl-go" --limit 10 --json
run_json "${TG[@]}" list-msgs "$CHAT" --limit 5 --json
run_json "${TG[@]}" list-msgs "$CHAT" --since 2026-05-01 --until 2026-05-09 --limit 50 --json
LAST_KNOWN=$("${TG[@]}" show "$CHAT" --limit 1 --json | jq -r '.data.messages[0].message_id')
live_require_numeric_selector message_id "$LAST_KNOWN"
run_json "${TG[@]}" get-msg "$CHAT" "$LAST_KNOWN" --json

# ---- Phase 9: text writes ----
run_json "${TG[@]}" send "$CHAT" "live-verify: phase 9 send" --allow-write --json
SENT_ID=$("${TG[@]}" send "$CHAT" "live-verify: edit-me" --allow-write --json | jq -r '.data.message_id')
live_require_numeric_selector message_id "$SENT_ID"
run_json "${TG[@]}" edit-msg "$CHAT" "$SENT_ID" "live-verify: edited body" --allow-write --json
run_json "${TG[@]}" pin-msg "$CHAT" "$SENT_ID" --allow-write --json
run_json "${TG[@]}" unpin-msg "$CHAT" "$SENT_ID" --allow-write --json
run_json "${TG[@]}" mark-read "$CHAT" --up-to "$SENT_ID" --allow-write --json
FWD_SRC=$("${TG[@]}" send "$CHAT" "live-verify: forward-source" --allow-write --json | jq -r '.data.message_id')
live_require_numeric_selector message_id "$FWD_SRC"
run_json "${TG[@]}" forward "$CHAT" "$CHAT" "$FWD_SRC" --allow-write --json
expect_json_ok_or_error PREMIUM_REQUIRED "${TG[@]}" react "$CHAT" "$SENT_ID" "👍" --allow-write --json
KEY="live-verify-$(date +%s)"
run_json "${TG[@]}" send "$CHAT" "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json
echo "+ ${TG[*]} send <test-chat> <idempotency-payload> --allow-write --json  # expect idempotent replay" | tee -a "$OUT"
"${TG[@]}" send "$CHAT" "live-verify: idempotency $KEY" --allow-write --idempotency-key "$KEY" --json | tee -a "$OUT" | jq -e '.data.idempotent_replay == true' >/dev/null
echo | tee -a "$OUT"
run_json "${TG[@]}" send-by-username "$SELF_USERNAME" "live-verify: send-by-username path" --allow-write --json

# ---- Phase 10: media ----
printf 'doc payload\n' > "$MEDIA_DIR/doc.txt"
printf 'OggS placeholder for dry-run\n' > "$MEDIA_DIR/voice.ogg"
printf '\x00\x00\x00\x18ftypmp42placeholder for dry-run\n' > "$MEDIA_DIR/video.mp4"
python3 - <<'PY'
import os
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
open(os.path.join(os.environ["MEDIA_DIR"], "pixel.png"), "wb").write(png)
PY
run_json "${TG[@]}" upload-document "$CHAT" "$MEDIA_DIR/doc.txt" --caption "live-verify: doc" --allow-write --json
run_json "${TG[@]}" upload-photo "$CHAT" "$MEDIA_DIR/pixel.png" --caption "live-verify: photo" --allow-write --json
# skip-rationale: upload-voice and upload-video need real .ogg/.mp4 assets; dry-run exercises their runner paths without ffmpeg.
run_json "${TG[@]}" upload-voice "$CHAT" "$MEDIA_DIR/voice.ogg" --allow-write --dry-run --json
run_json "${TG[@]}" upload-video "$CHAT" "$MEDIA_DIR/video.mp4" --allow-write --dry-run --json
expect_json_error BAD_ARGS 2 "${TG[@]}" upload-document "$CHAT" "/tmp/has?question.txt" --allow-write --json

# ---- Phase 11: topics + folders ----
run_json "${TG[@]}" folders-list --json
FOLDER_COUNT=$("${TG[@]}" folders-list --json | jq '.data.folders | length')
if [ "$FOLDER_COUNT" -gt 0 ]; then
  FIRST_FOLDER_ID=$("${TG[@]}" folders-list --json | jq -r '.data.folders[0].id // .data.folders[0].folder_id')
  live_require_numeric_selector folder_id "$FIRST_FOLDER_ID"
  run_json "${TG[@]}" folder-show "$FIRST_FOLDER_ID" --json
fi
expect_json_error BAD_ARGS 2 "${TG[@]}" folder-delete 0 --allow-write --confirm 0 --json
run_json "${TG[@]}" topic-create "$CHAT" "live-verify-topic" --allow-write --dry-run --json
run_json "${TG[@]}" topic-edit "$CHAT" 1 --title "renamed" --allow-write --dry-run --json
run_json "${TG[@]}" topic-pin "$CHAT" 1 --allow-write --dry-run --json
run_json "${TG[@]}" topic-unpin "$CHAT" 1 --allow-write --dry-run --json
# skip-rationale: Saved Messages is not a forum supergroup; assert the live non-forum fallback.
expect_json_error BAD_ARGS 2 "${TG[@]}" topics-list "$CHAT" --json
run_json "${TG[@]}" chat-pinned-list "$CHAT" --json

# ---- Phase 12: admin ----
run_json "${TG[@]}" chats-info "$CHAT" --json
# skip-rationale: Saved Messages is not a channel/supergroup; assert the live non-participant fallback.
expect_json_error BAD_ARGS 2 "${TG[@]}" chat-members "$CHAT" --limit 50 --json
run_json "${TG[@]}" account-sessions --json
run_json "${TG[@]}" chat-title "$CHAT" "live-verify-title" --allow-write --dry-run --json
run_json "${TG[@]}" chat-photo "$CHAT" "$MEDIA_DIR/pixel.png" --allow-write --dry-run --json
run_json "${TG[@]}" chat-description "$CHAT" "live-verify desc" --allow-write --dry-run --json
run_json "${TG[@]}" set-permissions "$CHAT" --send-messages --allow-write --dry-run --json
run_json "${TG[@]}" chat-invite-link "$CHAT" --allow-write --dry-run --json
run_json "${TG[@]}" promote "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" demote "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" ban-from-chat "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" unban-from-chat "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" kick "$CHAT" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json

# ---- Phase 13: destructive ----
DEL_ID=$("${TG[@]}" send "$CHAT" "live-verify: about-to-delete" --allow-write --json | jq -r '.data.message_id')
live_require_numeric_selector message_id "$DEL_ID"
run_json "${TG[@]}" delete-msg "$CHAT" "$DEL_ID" --allow-write --confirm "$CHAT" --json
run_json "${TG[@]}" leave-chat "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" block-user "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
run_json "${TG[@]}" unblock-user "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
SESSION_HASH=$("${TG[@]}" account-sessions --json | jq -r '.data.sessions[0].hash // 0')
if [ "$SESSION_HASH" != "0" ]; then
  live_require_numeric_selector session_hash "$SESSION_HASH"
  run_json "${TG[@]}" terminate-session "$SESSION_HASH" --allow-write --confirm "$SESSION_HASH" --dry-run --json
fi

# ---- Phase 14: local DB ops ----
run_json "${TG[@]}" discover --allow-write --json
run_json "${TG[@]}" sync-contacts --allow-write --json
run_json "${TG[@]}" backfill "$CHAT" --max-messages 100 --allow-write --json

# ---- Phase 15: live ----
echo "+ timeout 8 ${TG[*]} listen --once --allow-write --json" | tee -a "$OUT"
if command -v timeout >/dev/null 2>&1; then
  timeout 8 "${TG[@]}" listen --once --allow-write --json | tee -a "$OUT" || echo "(no update inside 8s - acceptable)" | tee -a "$OUT"
else
  echo "(timeout unavailable - listen smoke skipped)" | tee -a "$OUT"
fi
echo | tee -a "$OUT"

# ---- Phase 16 surface ----
"${TG[@]}" --help >/dev/null
echo "help ok" | tee -a "$OUT"
COUNT=$("${TG[@]}" --help | grep -E "^  [a-z]" | wc -l | tr -d ' ')
echo "command count: $COUNT" | tee -a "$OUT"
[ "$COUNT" -ge 62 ]

# ---- Safety pipeline regressions ----
expect_json_error WRITE_DISALLOWED 6 "${TG[@]}" send "$CHAT" "no-allow" --json
expect_json_error WRITE_DISALLOWED 6 "${TG[@]}" --read-only send "$CHAT" "ro" --allow-write --json
expect_json_error BAD_ARGS 2 "${TG[@]}" delete-msg "$CHAT" 999999 --allow-write --json
expect_json_error NOT_FOUND 4 "${TG[@]}" get-msg "$CHAT" 999999999999 --json

echo "=== live verification complete ===" | tee -a "$OUT"
