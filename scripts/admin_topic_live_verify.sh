#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/live_test_common.sh"

RUN_ID="admin-verify-$(date +%Y%m%d%H%M%S)"
CHAT="${TGCTL_LIVE_CHAT:?Set TGCTL_LIVE_CHAT to an isolated test chat or Saved Messages ID}"
FORUM_CHAT="${TGCTL_LIVE_FORUM_CHAT:?Set TGCTL_LIVE_FORUM_CHAT to a dedicated forum test chat ID}"
if [ -n "${TGCTL_LIVE_OUTPUT:-}" ]; then
  printf 'TGCTL_LIVE_OUTPUT retention is disabled; raw live output remains ephemeral\n' >&2
  exit 2
fi
live_require_numeric_selector TGCTL_LIVE_CHAT "$CHAT"
live_require_numeric_selector TGCTL_LIVE_FORUM_CHAT "$FORUM_CHAT"
ACCOUNT="adminverify-${RUN_ID}"
DEFAULT_SESSION="${TGCTL_SOURCE_SESSION:?Set TGCTL_SOURCE_SESSION to the authenticated test session path}"
DEFAULT_DB="${TGCTL_SOURCE_DB:?Set TGCTL_SOURCE_DB to the matching test database path}"
live_require_readable_file TGCTL_SOURCE_SESSION "$DEFAULT_SESSION"
live_require_readable_file TGCTL_SOURCE_DB "$DEFAULT_DB"
live_validate_tg_bin_config
live_workspace_init admin-topic
TMP_DIR="$LIVE_WORKSPACE"
OUT="$TMP_DIR/transcript.txt"
ACCOUNT_ROOT="$LIVE_WORKSPACE/accounts"
ACCOUNT_DIR="${ACCOUNT_ROOT}/${ACCOUNT}"
SESSION="${ACCOUNT_DIR}/tg.session"
DB="${ACCOUNT_DIR}/telegram.sqlite"
export GOCACHE="$LIVE_WORKSPACE/go-cache"

mkdir -p "$TMP_DIR"
> "$OUT"
chmod 600 "$OUT"

log() {
  printf '%s\n' "$*" | tee -a "$OUT"
}

known_ok='.ok == true or (.ok == false and (.error.code == "FLOOD_WAIT" or .error.code == "PREMIUM_REQUIRED"))'

with_timeout() {
  local seconds="$1"
  shift
  python3 - "$seconds" "$@" <<'PY'
import subprocess
import sys

seconds = int(sys.argv[1])
cmd = sys.argv[2:]
try:
    raise SystemExit(subprocess.run(cmd, timeout=seconds).returncode)
except subprocess.TimeoutExpired:
    raise SystemExit(124)
PY
}

run_json() {
  local dest="$1"
  shift
  log "+ $*"
  set +e
  with_timeout 45 "$@" >"$dest" 2>&1
  local status=$?
  set -e
  if [ "$status" -eq 124 ]; then
    printf '{"ok":false,"error":{"code":"TIMEOUT","message":"command timed out after 45 seconds"}}\n' >"$dest"
  fi
  cat "$dest" | tee -a "$OUT"
  jq -e '.ok == true' "$dest" >/dev/null
  if [ "$status" -ne 0 ]; then
    log "FAILED: expected exit 0, got $status"
    exit 1
  fi
  log ""
}

run_plain() {
  log "+ $*"
  set +e
  with_timeout 45 "$@" 2>&1 | tee -a "$OUT"
  local status=${PIPESTATUS[0]}
  set -e
  if [ "$status" -ne 0 ]; then
    log "FAILED: expected exit 0, got $status"
    exit 1
  fi
  log ""
}

run_json_allow_known() {
  local dest="$1"
  shift
  log "+ $*"
  set +e
  with_timeout 45 "$@" >"$dest" 2>&1
  local status=$?
  set -e
  if [ "$status" -eq 124 ]; then
    printf '{"ok":false,"error":{"code":"TIMEOUT","message":"command timed out after 45 seconds"}}\n' >"$dest"
  fi
  cat "$dest" | tee -a "$OUT"
  jq -e "$known_ok" "$dest" >/dev/null
  if [ "$status" -ne 0 ] && [ "$status" -ne 5 ] && [ "$status" -ne 9 ]; then
    log "FAILED: expected exit 0, 5, or 9; got $status"
    exit 1
  fi
  log ""
}

run_json_expect_error() {
  local expected="$1"
  local dest="$2"
  shift 2
  log "+ $*  # expect $expected"
  set +e
  with_timeout 45 "$@" >"$dest" 2>&1
  local status=$?
  set -e
  if [ "$status" -eq 124 ]; then
    printf '{"ok":false,"error":{"code":"TIMEOUT","message":"command timed out after 45 seconds"}}\n' >"$dest"
  fi
  cat "$dest" | tee -a "$OUT"
  jq -e --arg code "$expected" '.ok == false and .error.code == $code' "$dest" >/dev/null
  if [ "$status" -eq 0 ]; then
    log "FAILED: expected non-zero exit for $expected"
    exit 1
  fi
  log ""
}

folder_delete_if_set() {
  local id="$1"
  if [ -n "$id" ] && [ "$id" != "null" ]; then
    log "+ ${TG[*]} folder-delete $id --allow-write --confirm $id --json  # cleanup"
    set +e
    with_timeout 45 "${TG[@]}" folder-delete "$id" --allow-write --confirm "$id" --json 2>&1 | tee -a "$OUT"
    set -e
    log ""
  fi
}

cleanup_group_if_set() {
  local id="$1"
  if [ -n "$id" ] && [ "$id" != "null" ]; then
    log "+ ${TG[*]} leave-chat $id --allow-write --confirm $id --json  # cleanup"
    set +e
    with_timeout 45 "${TG[@]}" leave-chat "$id" --allow-write --confirm "$id" --json 2>&1 | tee -a "$OUT"
    set -e
    log ""
  fi
}

live_remote_cleanup() {
  folder_delete_if_set "${FOLDER_ID:-}"
  folder_delete_if_set "${SECOND_ID:-}"
  cleanup_group_if_set "${TEMP_GROUP_ID:-}"
}

create_temp_group() {
  local title="$1"
  local helper="$TMP_DIR/create_temp_group.go"
  cat >"$helper" <<'GO'
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: create_temp_group <session> <title>")
		os.Exit(2)
	}
	apiID, err := strconv.Atoi(os.Getenv("TG_API_ID"))
	if err != nil || apiID == 0 || os.Getenv("TG_API_HASH") == "" {
		fmt.Fprintln(os.Stderr, "TG_API_ID/TG_API_HASH missing")
		os.Exit(2)
	}
	client := telegram.NewClient(apiID, os.Getenv("TG_API_HASH"), telegram.Options{
		SessionStorage: &session.FileStorage{Path: os.Args[1]},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var chatID int64
	var accessHash int64
	err = client.Run(ctx, func(ctx context.Context) error {
		updatesClass, err := client.API().ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
			Megagroup: true,
			Title:     os.Args[2],
			About:     "tgctl-go verification run",
		})
		if err != nil {
			return err
		}
		if updates, ok := updatesClass.(*tg.Updates); ok {
			for _, c := range updates.Chats {
				if ch, ok := c.(*tg.Channel); ok {
					chatID = ch.ID
					accessHash = ch.AccessHash
					break
				}
			}
		}
		if chatID == 0 {
			return fmt.Errorf("created channel id not found in %T", updatesClass)
		}
		fmt.Printf("%d %d\n", chatID, accessHash)
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
GO
  go run "$helper" "$SESSION" "$title"
}

log "=== admin/topic live verification start ==="
log "run_id: $RUN_ID"
log "self_chat: $CHAT"
log "forum_chat: $FORUM_CHAT"
log ""

if [ -f "$PROJECT_ROOT/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$PROJECT_ROOT/.env"
  set +a
fi

live_prepare_tg "$PROJECT_ROOT"
TG=("$LIVE_TG_LAUNCHER" --account "$ACCOUNT")
run_json "$TMP_DIR/version.json" "${TG[@]}" version --json
mkdir -p "$ACCOUNT_DIR"
cp "$DEFAULT_SESSION" "$SESSION"
cp "$DEFAULT_DB" "$DB"
log "local_account_copy: $ACCOUNT"
log "skip: backfill-entities is omitted here; this targeted script uses a copied entity cache and inserts its temporary admin group directly"
log ""

log "=== topics ==="
if with_timeout 45 "${TG[@]}" topics-list "$FORUM_CHAT" --limit 1 --json >"$TMP_DIR/topics_probe.json" 2>&1; then
  cat "$TMP_DIR/topics_probe.json" | tee -a "$OUT"
  log ""
  run_json_allow_known "$TMP_DIR/topic_create.json" "${TG[@]}" topic-create "$FORUM_CHAT" "av-${RUN_ID}" --allow-write --json
  TOPIC_ID="$(jq -r '.data.topic_id // empty' "$TMP_DIR/topic_create.json")"
  if [ -n "$TOPIC_ID" ]; then
    live_require_numeric_selector topic_id "$TOPIC_ID"
    run_json_allow_known "$TMP_DIR/topic_edit.json" "${TG[@]}" topic-edit "$FORUM_CHAT" "$TOPIC_ID" --title "edited-${RUN_ID}" --allow-write --json
    run_json_allow_known "$TMP_DIR/topic_pin.json" "${TG[@]}" topic-pin "$FORUM_CHAT" "$TOPIC_ID" --allow-write --json
    run_json_allow_known "$TMP_DIR/topic_unpin.json" "${TG[@]}" topic-unpin "$FORUM_CHAT" "$TOPIC_ID" --allow-write --json
  else
    log "topic-create returned no topic_id; skipping edit/pin/unpin"
  fi
else
  cat "$TMP_DIR/topics_probe.json" | tee -a "$OUT"
  log "skip: forum chat $FORUM_CHAT is unavailable or not owned by this account"
  log ""
fi

log "=== folders ==="
# Telegram caps DialogFilter.title at 12 chars; RUN_ID is too long for that field,
# so we use short fixed names for folders (which get deleted on cleanup anyway).
run_json_allow_known "$TMP_DIR/folder_create.json" "${TG[@]}" folder-create "av-fld-1" --include-chats "$CHAT" --allow-write --json
FOLDER_ID="$(jq -r '.data.folder_id // empty' "$TMP_DIR/folder_create.json")"
live_require_numeric_selector folder_id "$FOLDER_ID"
# Add a second peer so we can later remove one without leaving the folder empty
# (Telegram rejects DialogFilters with no include peers: FILTER_INCLUDE_EMPTY).
run_json_allow_known "$TMP_DIR/folder_add_chat.json" "${TG[@]}" folder-add-chat "$FOLDER_ID" "$FORUM_CHAT" --allow-write --json
run_json_allow_known "$TMP_DIR/folder_remove_chat.json" "${TG[@]}" folder-remove-chat "$FOLDER_ID" "$CHAT" --allow-write --json
run_json_allow_known "$TMP_DIR/folder_edit.json" "${TG[@]}" folder-edit "$FOLDER_ID" --name "av-fld-1r" --allow-write --json
run_json_allow_known "$TMP_DIR/second_folder_create.json" "${TG[@]}" folder-create "av-fld-2" --include-chats "$CHAT" --allow-write --json
SECOND_ID="$(jq -r '.data.folder_id // empty' "$TMP_DIR/second_folder_create.json")"
live_require_numeric_selector folder_id "$SECOND_ID"
run_json_allow_known "$TMP_DIR/folders_reorder.json" "${TG[@]}" folders-reorder "$FOLDER_ID,$SECOND_ID" --allow-write --json
run_json_allow_known "$TMP_DIR/folder_delete_first.json" "${TG[@]}" folder-delete "$FOLDER_ID" --allow-write --confirm "$FOLDER_ID" --json
FOLDER_ID=""
run_json_allow_known "$TMP_DIR/folder_delete_second.json" "${TG[@]}" folder-delete "$SECOND_ID" --allow-write --confirm "$SECOND_ID" --json
SECOND_ID=""

log "=== admin ==="
set +e
TEMP_GROUP_CREATED="$(create_temp_group "av-${RUN_ID}" 2>"$TMP_DIR/create_group.err")"
create_status=$?
set -e
if [ "$create_status" -eq 0 ] && [ -n "$TEMP_GROUP_CREATED" ]; then
  TEMP_GROUP_ID="${TEMP_GROUP_CREATED%% *}"
  TEMP_GROUP_HASH="${TEMP_GROUP_CREATED#* }"
  live_require_numeric_selector temporary_group_id "$TEMP_GROUP_ID"
  live_require_numeric_selector temporary_group_access_hash "$TEMP_GROUP_HASH"
  log "created_temp_group: $TEMP_GROUP_ID"
  sqlite3 "$DB" \
    "INSERT INTO tg_entities(id, kind, access_hash, updated_at) VALUES ($TEMP_GROUP_ID, 'channel', $TEMP_GROUP_HASH, datetime('now')) ON CONFLICT(id) DO UPDATE SET kind='channel', access_hash=$TEMP_GROUP_HASH, updated_at=datetime('now'); INSERT INTO tg_chats(chat_id, type, title, username) VALUES ($TEMP_GROUP_ID, 'supergroup', 'av-${RUN_ID}', NULL) ON CONFLICT(chat_id) DO UPDATE SET type='supergroup', title='av-${RUN_ID}', username=NULL;"
  # Use a different title than the one create_temp_group set; Telegram rejects
  # no-op edits with CHAT_NOT_MODIFIED.
  run_json_allow_known "$TMP_DIR/chat_title.json" "${TG[@]}" chat-title "$TEMP_GROUP_ID" "av-${RUN_ID}-r" --allow-write --json
  run_json_allow_known "$TMP_DIR/chat_description.json" "${TG[@]}" chat-description "$TEMP_GROUP_ID" "verification run" --allow-write --json
  run_json_allow_known "$TMP_DIR/chat_invite_link.json" "${TG[@]}" chat-invite-link "$TEMP_GROUP_ID" --allow-write --json
  run_json_allow_known "$TMP_DIR/chat_members.json" "${TG[@]}" chat-members "$TEMP_GROUP_ID" --limit 50 --json
  run_json_allow_known "$TMP_DIR/promote.json" "${TG[@]}" promote "$TEMP_GROUP_ID" "$CHAT" --allow-write --confirm "$CHAT" --json
  run_json_allow_known "$TMP_DIR/demote.json" "${TG[@]}" demote "$TEMP_GROUP_ID" "$CHAT" --allow-write --confirm "$CHAT" --json
  log "skip: ban-from-chat/unban-from-chat/kick use --dry-run deliberately to avoid locking out the only test account"
  run_json "$TMP_DIR/ban_dry_run.json" "${TG[@]}" ban-from-chat "$TEMP_GROUP_ID" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
  run_json "$TMP_DIR/unban_dry_run.json" "${TG[@]}" unban-from-chat "$TEMP_GROUP_ID" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
  run_json "$TMP_DIR/kick_dry_run.json" "${TG[@]}" kick "$TEMP_GROUP_ID" "$CHAT" --allow-write --confirm "$CHAT" --dry-run --json
  run_json_allow_known "$TMP_DIR/set_permissions.json" "${TG[@]}" set-permissions "$TEMP_GROUP_ID" --send-messages --allow-write --json
  log "skip: chat-photo uses --dry-run because this script does not carry a PNG fixture"
  run_json "$TMP_DIR/chat_photo_dry_run.json" "${TG[@]}" chat-photo "$TEMP_GROUP_ID" "${TGCTL_TEST_PHOTO:-${TMPDIR:-/tmp}/tgctl-test-photo.png}" --allow-write --dry-run --json
else
  log "skip: could not create a temporary self-only admin group"
  sed 's/^/create-group: /' "$TMP_DIR/create_group.err" | tee -a "$OUT"
fi

log "=== admin/topic live verification complete ==="
