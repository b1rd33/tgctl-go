#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-preflight-test.XXXXXX")"
human_selector="$(printf '%s%s' Human Selector)"
synthetic_username="$(printf '@%s_%s' synthetic test)"
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

new_case() {
  local name="$1" case_dir="$TEST_ROOT/$1"
  mkdir -p "$case_dir/bin" "$case_dir/tmp"
  printf '%s\n' '#!/bin/sh' 'mkdir -p "$TMPDIR/workspace-init-was-reached"' 'exit 71' >"$case_dir/bin/mktemp"
  chmod +x "$case_dir/bin/mktemp"
  printf '%s\n' '#!/bin/sh' 'printf reached >>"$FAKE_TG_LOG"' 'exit 72' >"$case_dir/tg"
  chmod +x "$case_dir/tg"
  : >"$case_dir/source.session"
  : >"$case_dir/source.sqlite"
  printf '%s\n' "$case_dir"
}

assert_pure_failure() {
  local case_dir="$1" label="$2"
  if [ -e "$case_dir/tmp/workspace-init-was-reached" ]; then fail "$label reached workspace initialization"; fi
  if [ -s "$case_dir/tg.log" ]; then fail "$label reached tg"; fi
  if find "$case_dir/tmp" -mindepth 1 -print -quit | grep -q .; then fail "$label created temporary filesystem state"; fi
}

run_case() {
  local case_dir="$1" script="$2"
  shift 2
  env PATH="$case_dir/bin:$PATH" HOME="${HOME:-/tmp}" TMPDIR="$case_dir/tmp" \
    FAKE_TG_LOG="$case_dir/tg.log" TGCTL_LIVE_TG_BIN="$case_dir/tg" "$@" \
    bash "$SCRIPT_DIR/$script" >/dev/null 2>&1 || true
}

for kind in missing invalid; do
  live_case="$(new_case live-$kind)"
  if [ "$kind" = missing ]; then
    run_case "$live_case" live_verify.sh
  else
    run_case "$live_case" live_verify.sh TGCTL_LIVE_CHAT="$human_selector" TGCTL_LIVE_SELF_USERNAME="$synthetic_username" TGCTL_LIVE_ACCOUNT=synthetic-test
  fi
  assert_pure_failure "$live_case" "live $kind config"

  import_case="$(new_case import-$kind)"
  if [ "$kind" = missing ]; then
    run_case "$import_case" import_export_simulation.sh
  else
    run_case "$import_case" import_export_simulation.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_ACCOUNT=synthetic-test \
      TGCTL_LIVE_FOLDER_TARGETS=12001,12002,12003,12004 TGCTL_LIVE_FORUM_CHAT="$human_selector" \
      TGCTL_LIVE_DB="$import_case/source.sqlite"
  fi
  assert_pure_failure "$import_case" "import $kind config"

  admin_case="$(new_case admin-$kind)"
  if [ "$kind" = missing ]; then
    run_case "$admin_case" admin_topic_live_verify.sh
  else
    run_case "$admin_case" admin_topic_live_verify.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_FORUM_CHAT="$human_selector" \
      TGCTL_SOURCE_SESSION="$admin_case/source.session" TGCTL_SOURCE_DB="$admin_case/source.sqlite"
  fi
  assert_pure_failure "$admin_case" "admin $kind config"
done

live_option_case="$(new_case live-option)"
run_case "$live_option_case" live_verify.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_SELF_USERNAME="$synthetic_username" \
  TGCTL_LIVE_ACCOUNT=synthetic-test TGCTL_LIVE_OUTPUT=retained.txt
assert_pure_failure "$live_option_case" "live invalid retention option"

live_path_case="$(new_case live-bin-path)"
run_case "$live_path_case" live_verify.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_SELF_USERNAME="$synthetic_username" \
  TGCTL_LIVE_ACCOUNT=synthetic-test TGCTL_LIVE_TG_BIN="$live_path_case/missing-tg"
assert_pure_failure "$live_path_case" "live invalid binary path"

import_path_case="$(new_case import-db-path)"
run_case "$import_path_case" import_export_simulation.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_ACCOUNT=synthetic-test \
  TGCTL_LIVE_FOLDER_TARGETS=12001,12002,12003,12004 TGCTL_LIVE_FORUM_CHAT=13001 \
  TGCTL_LIVE_DB="$import_path_case/missing.sqlite"
assert_pure_failure "$import_path_case" "import invalid database path"

admin_path_case="$(new_case admin-source-path)"
run_case "$admin_path_case" admin_topic_live_verify.sh TGCTL_LIVE_CHAT=11001 TGCTL_LIVE_FORUM_CHAT=13001 \
  TGCTL_SOURCE_SESSION="$admin_path_case/missing.session" TGCTL_SOURCE_DB="$admin_path_case/source.sqlite"
assert_pure_failure "$admin_path_case" "admin invalid source path"

printf 'live preflight ordering tests passed\n'
