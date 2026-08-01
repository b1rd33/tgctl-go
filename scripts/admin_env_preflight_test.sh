#!/usr/bin/env bash
set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-admin-env-test.XXXXXX")"
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

new_project() {
  local name="$1" root="$TEST_ROOT/$1"
  mkdir -p "$root/scripts" "$root/tmp" "$root/bin"
  cp "$SOURCE_DIR/admin_topic_live_verify.sh" "$root/scripts/"
  cp "$SOURCE_DIR/live_test_common.sh" "$root/scripts/"
  : >"$root/source.session"
  : >"$root/source.sqlite"
  printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$*" >>"$FAKE_TG_LOG"' \
    'command_name="${1:-}"' 'if [ "$command_name" = --account ]; then command_name="${3:-}"; fi' \
    'if [ "$command_name" = folder-create ]; then printf stop; exit 77; fi' \
    'if [ "$command_name" = topic-create ]; then printf "%s\n" "{\"ok\":true,\"data\":{\"topic_id\":51}}"; exit 0; fi' \
    'printf "%s\n" "{\"ok\":true,\"data\":{}}"' >"$root/tg"
  chmod +x "$root/tg"
  printf '%s\n' "$root"
}

human_selector="$(printf '%s%s' Human Selector)"
invalid_root="$(new_project invalid)"
printf 'TGCTL_LIVE_CHAT=%s\nTGCTL_LIVE_FORUM_CHAT=%s\n' "$human_selector" "$human_selector" >"$invalid_root/.env"
printf '%s\n' '#!/bin/sh' 'mkdir -p "$TMPDIR/workspace-init-was-reached"' 'exit 71' >"$invalid_root/bin/mktemp"
chmod +x "$invalid_root/bin/mktemp"
env PATH="$invalid_root/bin:$PATH" HOME="${HOME:-/tmp}" TMPDIR="$invalid_root/tmp" \
  TGCTL_LIVE_TG_BIN="$invalid_root/tg" FAKE_TG_LOG="$invalid_root/tg.log" \
  TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_FORUM_CHAT=12001 \
  TGCTL_SOURCE_SESSION="$invalid_root/source.session" TGCTL_SOURCE_DB="$invalid_root/source.sqlite" \
  bash "$invalid_root/scripts/admin_topic_live_verify.sh" >/dev/null 2>&1 || true
[ ! -e "$invalid_root/tmp/workspace-init-was-reached" ] || fail "dotenv override was validated after workspace creation"
[ ! -s "$invalid_root/tg.log" ] || fail "invalid dotenv override reached tg"

valid_root="$(new_project valid)"
printf 'TGCTL_LIVE_CHAT=%s\nTGCTL_LIVE_FORUM_CHAT=%s\nTGCTL_SOURCE_SESSION=%s\nTGCTL_SOURCE_DB=%s\n' \
  -31001 -32001 "$valid_root/source.session" "$valid_root/source.sqlite" >"$valid_root/.env"
env PATH="$PATH" HOME="${HOME:-/tmp}" TMPDIR="$valid_root/tmp" \
  TGCTL_LIVE_TG_BIN="$valid_root/tg" FAKE_TG_LOG="$valid_root/tg.log" \
  TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_FORUM_CHAT=12001 \
  TGCTL_SOURCE_SESSION="$valid_root/source.session" TGCTL_SOURCE_DB="$valid_root/source.sqlite" \
  bash "$valid_root/scripts/admin_topic_live_verify.sh" >/dev/null 2>&1 || true
grep -Eq '^--account [^ ]+ topics-list -32001 ' "$valid_root/tg.log" || fail "valid dotenv forum selector was not forwarded exactly"
grep -Eq '^--account [^ ]+ folder-create .*--include-chats -31001( |$)' "$valid_root/tg.log" || fail "valid dotenv chat selector was not forwarded exactly"

printf 'admin dotenv preflight tests passed\n'
