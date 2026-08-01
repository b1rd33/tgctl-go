#!/usr/bin/env bash
# Shared fail-closed primitives for live verification scripts.

live_workspace_init() {
  local label="${1:-verify}"
  umask 077
  LIVE_WORKSPACE="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-${label}.XXXXXX")" || {
    printf 'live verification: unable to create private workspace\n' >&2
    exit 2
  }
  chmod 700 "$LIVE_WORKSPACE"
  export LIVE_WORKSPACE
  trap 'live_cleanup "$?"' EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
}

live_cleanup() {
  local status="${1:-1}"
  trap - EXIT HUP INT TERM
  if declare -F live_remote_cleanup >/dev/null 2>&1; then
    set +e
    live_remote_cleanup
    set -e
  fi
  if [ -n "${LIVE_WORKSPACE:-}" ] && [ -d "$LIVE_WORKSPACE" ]; then
    rm -rf -- "$LIVE_WORKSPACE"
  fi
  exit "$status"
}

live_require_numeric_selector() {
  local name="$1" value="$2" digits limit
  if [[ ! "$value" =~ ^-?[1-9][0-9]{0,18}$ ]]; then
    printf '%s must be a nonzero numeric Telegram ID\n' "$name" >&2
    return 2
  fi
  digits="${value#-}"
  if [ "${#digits}" -eq 19 ]; then
    limit=9223372036854775807
    [ "${value:0:1}" = "-" ] && limit=9223372036854775808
    if [[ "$digits" > "$limit" ]]; then
      printf '%s is outside the signed 64-bit Telegram ID range\n' "$name" >&2
      return 2
    fi
  fi
}

live_require_username() {
  local name="$1" value="$2"
  if [[ ! "$value" =~ ^@?[A-Za-z][A-Za-z0-9_]{4,31}$ ]]; then
    printf '%s must be an explicit Telegram username\n' "$name" >&2
    return 2
  fi
}

live_require_account_name() {
  local value="$1"
  if [[ ! "$value" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]]; then
    printf 'TGCTL_LIVE_ACCOUNT must name an explicit test account\n' >&2
    return 2
  fi
}

live_require_readable_file() {
  local name="$1" value="$2"
  if [ ! -f "$value" ] || [ ! -r "$value" ]; then
    printf '%s must point to a readable regular test file\n' "$name" >&2
    return 2
  fi
}

live_validate_tg_bin_config() {
  if [ -n "${TGCTL_LIVE_TG_BIN:-}" ] && [ ! -x "$TGCTL_LIVE_TG_BIN" ]; then
    printf 'TGCTL_LIVE_TG_BIN must be an executable test binary\n' >&2
    return 2
  fi
}

live_prepare_tg() {
  local project_root="$1"
  LIVE_TG_CWD="${2:-$LIVE_WORKSPACE}"
  LIVE_TG_BIN="${TGCTL_LIVE_TG_BIN:-$LIVE_WORKSPACE/tg-bin}"
  if [ -z "${TGCTL_LIVE_TG_BIN:-}" ]; then
    GOCACHE="$LIVE_WORKSPACE/go-cache" go build -buildvcs=false -o "$LIVE_TG_BIN" "$project_root/cmd/tg"
  elif [ ! -x "$LIVE_TG_BIN" ]; then
    printf 'TGCTL_LIVE_TG_BIN must be an executable test binary\n' >&2
    return 2
  fi
  export LIVE_TG_BIN LIVE_TG_CWD
  LIVE_TG_LAUNCHER="$LIVE_WORKSPACE/tg"
  export LIVE_TG_LAUNCHER
  printf '%s\n' '#!/bin/sh' 'cd "$LIVE_TG_CWD"' 'exec "$LIVE_TG_BIN" "$@"' >"$LIVE_TG_LAUNCHER"
  chmod 700 "$LIVE_TG_LAUNCHER"
}
