#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-live-target-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

. "$SCRIPT_DIR/live_test_common.sh"
for valid_selector in 1 -1 9223372036854775807 -9223372036854775808; do
	live_require_numeric_selector synthetic "$valid_selector" >/dev/null || fail "valid signed selector was rejected"
done
for invalid_selector in 0 -0 HumanSelector 01 9223372036854775808 -9223372036854775809; do
	if live_require_numeric_selector synthetic "$invalid_selector" >/dev/null 2>&1; then
		fail "invalid selector was accepted"
	fi
done

# Invalid permission targets/accounts must fail before any CLI process starts.
cat >"$TMP_ROOT/tg" <<'FAKE'
#!/bin/sh
touch "$FAKE_TG_LOG"
exit 99
FAKE
chmod +x "$TMP_ROOT/tg"
for invalid_case in target account same-account; do
  chat=-1000000000001
  allowed=synthetic-allowed
  denied=synthetic-denied
  case "$invalid_case" in
    target) chat=HumanSelector ;;
    account) allowed=../other ;;
    same-account) denied="$allowed" ;;
  esac
  if env TGCTL_LIVE_PERMISSION_CHAT="$chat" TGCTL_LIVE_ALLOWED_ACCOUNT="$allowed" \
    TGCTL_LIVE_DENIED_ACCOUNT="$denied" TGCTL_LIVE_TG_BIN="$TMP_ROOT/tg" \
    FAKE_TG_LOG="$TMP_ROOT/called" bash "$SCRIPT_DIR/live_permissions.sh" >/dev/null 2>&1; then
    fail "invalid permission fixture accepted"
  fi
  [ ! -e "$TMP_ROOT/called" ] || fail "invalid fixture contacted CLI"
done
printf 'live target safety tests passed\n'
