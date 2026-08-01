#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
HELPER="$SCRIPT_DIR/live_test_common.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-workspace-test.XXXXXX")"
trap 'rm -rf "$TEST_ROOT"' EXIT HUP INT TERM

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

mode_of() {
  if stat -f '%Lp' "$1" >/dev/null 2>&1; then stat -f '%Lp' "$1"; else stat -c '%a' "$1"; fi
}

make_probe() {
  probe="$TEST_ROOT/probe-$1.sh"
  apply_mode="$2"
  cat >"$probe" <<EOF
#!/usr/bin/env bash
set -euo pipefail
source "$HELPER"
live_workspace_init probe
printf '%s\n' "\$LIVE_WORKSPACE" >"\$PROBE_PATH_FILE"
: >"\$LIVE_WORKSPACE/private-output"
chmod 600 "\$LIVE_WORKSPACE/private-output"
$apply_mode
EOF
  chmod +x "$probe"
  printf '%s\n' "$probe"
}

success_probe="$(make_probe success ':')"
success_path_file="$TEST_ROOT/success.path"
PROBE_PATH_FILE="$success_path_file" "$success_probe"
success_workspace="$(cat "$success_path_file")"
[ ! -e "$success_workspace" ] || fail "success workspace was not cleaned"

failure_probe="$(make_probe failure 'exit 23')"
failure_path_file="$TEST_ROOT/failure.path"
PROBE_PATH_FILE="$failure_path_file" "$failure_probe" || status=$?
[ "${status:-0}" -eq 23 ] || fail "failure status was not preserved"
failure_workspace="$(cat "$failure_path_file")"
[ ! -e "$failure_workspace" ] || fail "failure workspace was not cleaned"

hold_probe="$(make_probe hold 'printf ready >"$LIVE_WORKSPACE/ready"; while :; do sleep 0.05; done')"
hold_path_file="$TEST_ROOT/hold.path"
PROBE_PATH_FILE="$hold_path_file" "$hold_probe" &
hold_pid=$!
for _ in $(seq 1 100); do [ -s "$hold_path_file" ] && break; sleep 0.02; done
hold_workspace="$(cat "$hold_path_file")"
[ "$(mode_of "$hold_workspace")" = 700 ] || fail "workspace mode is not 0700"
[ "$(mode_of "$hold_workspace/private-output")" = 600 ] || fail "raw output mode is not 0600"
kill -TERM "$hold_pid"
wait "$hold_pid" || true
[ ! -e "$hold_workspace" ] || fail "signal workspace was not cleaned"

first_path_file="$TEST_ROOT/first.path"
second_path_file="$TEST_ROOT/second.path"
PROBE_PATH_FILE="$first_path_file" "$hold_probe" & first_pid=$!
PROBE_PATH_FILE="$second_path_file" "$hold_probe" & second_pid=$!
for _ in $(seq 1 100); do [ -s "$first_path_file" ] && [ -s "$second_path_file" ] && break; sleep 0.02; done
first_workspace="$(cat "$first_path_file")"
second_workspace="$(cat "$second_path_file")"
[ "$first_workspace" != "$second_workspace" ] || fail "concurrent workspaces collided"
kill -TERM "$first_pid" "$second_pid"
wait "$first_pid" || true
wait "$second_pid" || true
[ ! -e "$first_workspace" ] && [ ! -e "$second_workspace" ] || fail "concurrent workspaces were not cleaned"

printf 'live workspace tests passed\n'
