#!/usr/bin/env bash
# Non-destructive permission smoke test for a disposable Telegram group/channel.
# It sends one uniquely marked message from an allowed account and confirms a
# denied member receives the stable PERMISSION_DENIED result. No bans,
# promotions, deletes, or flood-wait generation are attempted.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PROJECT_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
. "$SCRIPT_DIR/live_test_common.sh"

CHAT="${TGCTL_LIVE_PERMISSION_CHAT:?Set TGCTL_LIVE_PERMISSION_CHAT to a disposable group/channel ID}"
ALLOWED_ACCOUNT="${TGCTL_LIVE_ALLOWED_ACCOUNT:?Set TGCTL_LIVE_ALLOWED_ACCOUNT to an authorized test account}"
DENIED_ACCOUNT="${TGCTL_LIVE_DENIED_ACCOUNT:?Set TGCTL_LIVE_DENIED_ACCOUNT to a member without send permission}"

live_require_numeric_selector TGCTL_LIVE_PERMISSION_CHAT "$CHAT"
live_require_account_name "$ALLOWED_ACCOUNT"
live_require_account_name "$DENIED_ACCOUNT"
if [ "$ALLOWED_ACCOUNT" = "$DENIED_ACCOUNT" ]; then
  printf 'allowed and denied accounts must be different\n' >&2
  exit 2
fi
if [ -n "${TGCTL_LIVE_OUTPUT:-}" ]; then
  printf 'TGCTL_LIVE_OUTPUT retention is disabled; raw live output remains ephemeral\n' >&2
  exit 2
fi
live_validate_tg_bin_config
live_workspace_init permissions
live_prepare_tg "$PROJECT_ROOT" "$PROJECT_ROOT"
OUT="$LIVE_WORKSPACE/transcript.jsonl"
touch "$OUT"
chmod 600 "$OUT"

run_json() {
  local account="$1"; shift
  local tmp status
  tmp="$(mktemp "$LIVE_WORKSPACE/json.XXXXXX")"
  set +e
  "$LIVE_TG_LAUNCHER" --account "$account" "$@" --json >"$tmp" 2>&1
  status=$?
  set -e
  cat "$tmp" >>"$OUT"
  printf '%s\n' "$status"
  cat "$tmp"
  rm -f "$tmp"
}

RUN_ID="permissions-$(date +%s)-$$"
allowed_output="$(run_json "$ALLOWED_ACCOUNT" send "$CHAT" "tgctl permission probe $RUN_ID" --allow-write)"
allowed_status="${allowed_output%%$'\n'*}"
if [ "$allowed_status" -ne 0 ] || ! printf '%s\n' "$allowed_output" | tail -n +2 | jq -e '.ok == true' >/dev/null; then
  printf 'allowed account did not send successfully; inspect the private transcript\n' >&2
  exit 1
fi

denied_output="$(run_json "$DENIED_ACCOUNT" send "$CHAT" "tgctl permission denied probe $RUN_ID" --allow-write)"
denied_status="${denied_output%%$'\n'*}"
denied_json="$(printf '%s\n' "$denied_output" | tail -n +2)"
if [ "$denied_status" -ne 10 ] || ! printf '%s\n' "$denied_json" | jq -e '.ok == false and .error.code == "PERMISSION_DENIED"' >/dev/null; then
  printf 'denied account did not produce PERMISSION_DENIED (status %s); inspect the private transcript\n' "$denied_status" >&2
  exit 1
fi

printf 'permission smoke test passed: allowed send and denied send behaved as expected\n'
