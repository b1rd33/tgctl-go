#!/usr/bin/env bash
# Inspect tracked Git objects without printing matched values.
set -euo pipefail

REPO="${HYGIENE_REPO:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
failures=0

if ! git -C "$REPO" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'public-hygiene: requires a Git checkout\n' >&2
  exit 2
fi

quoted_path() { printf '%q' "$1"; }

report_path() {
  local rule="$1" path="$2"
  printf 'public-hygiene: %s: %s\n' "$rule" "$(quoted_path "$path")" >&2
  failures=$((failures + 1))
}

report_matches() {
  local rule="$1" pattern="$2" path="$3" oid="$4"
  git -C "$REPO" cat-file blob "$oid" 2>/dev/null |
    grep -nE "$pattern" 2>/dev/null |
    while IFS=: read -r line_number _; do
      printf 'public-hygiene: %s: %s:%s\n' "$rule" "$(quoted_path "$path")" "$line_number" >&2
    done
  grep -qE "$pattern" 2>/dev/null < <(git -C "$REPO" cat-file blob "$oid" 2>/dev/null)
}

while IFS= read -r -d '' entry; do
  header="${entry%%$'\t'*}"
  path="${entry#*$'\t'}"
  read -r mode oid stage <<<"$header"

  case "$mode" in
    120000) report_path tracked-symlink "$path"; continue ;;
    100644|100755) ;;
    *) report_path tracked-nonfile "$path"; continue ;;
  esac
  if ! git -C "$REPO" cat-file -e "$oid" 2>/dev/null; then
    report_path invalid-index-entry "$path"
    continue
  fi

  case "$path" in
    *.transcript.txt|*.live-report.txt|*.live-report.json|*_live_report.txt|*_live_report.json|*_simulation_report.json|*.sqlite|*.sqlite3|*.db|*.sqlite-wal|*.sqlite-shm|*.db-wal|*.db-shm|*.audit.log|audit.ndjson|*.session)
      report_path forbidden-artifact "$path"
      continue
      ;;
  esac

  size="$(git -C "$REPO" cat-file -s "$oid")"
  if [ "$size" -gt 0 ] && ! LC_ALL=C grep -Iq '' < <(git -C "$REPO" cat-file blob "$oid"); then
    report_path unexpected-binary "$path"
    continue
  fi

  if report_matches local-machine-path '(/Users/[A-Za-z0-9._-]+/|/home/[A-Za-z0-9._-]+/|[A-Za-z]:\\Users\\[^\\[:space:]]+\\)' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-invite-link 'https?://(t\.me|telegram\.me)/(joinchat/|\+)[A-Za-z0-9_-]+' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-phone '(TG_PHONE|TELEGRAM_PHONE|PHONE_NUMBER)[[:space:]]*=[[:space:]]*[^+[:space:]]*\+[0-9][0-9 ()-]{7,}' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-numeric-selector '(TG_(CHAT|USER|TARGET|FORUM|OWNER)_ID|CHAT|FORUM_CHAT|TARGET_USER|OWNER_ID)[[:space:]]*=[[:space:]]*[^[:space:]0-9-]*-?[0-9]{7,15}' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-owner-selector '(SELF_USERNAME|OWNER_USERNAME|TG_(OWNER|TARGET)_USERNAME)[[:space:]]*=[[:space:]]*[^@[:space:]]*@[A-Za-z0-9_]{5,}' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-human-selector '(^|[;&|[:space:]])(\./)?tg[[:space:]]+send[[:space:]]+[A-Z][A-Za-z]{2,}([[:space:]]|$)' "$path" "$oid"; then failures=$((failures + 1)); fi
  if report_matches telegram-human-selector '(CHAT|FORUM_CHAT|TARGET_CHAT|SELF_CHAT|TG_(CHAT|TARGET)_SELECTOR)[[:space:]]*=[[:space:]]*[^A-Z[:space:]]*[A-Z][A-Za-z]{2,}([[:space:]]|$)' "$path" "$oid"; then failures=$((failures + 1)); fi
done < <(git -C "$REPO" ls-files -s -z)

if [ "$failures" -ne 0 ]; then
  printf 'public-hygiene: failed with %s finding(s); matched values are redacted\n' "$failures" >&2
  exit 1
fi

printf 'public-hygiene: passed\n'
