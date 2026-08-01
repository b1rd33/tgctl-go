#!/bin/sh
# Check tracked public-repository content without printing matched values.
set -eu

REPO="${HYGIENE_REPO:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
failures=0

report_path() {
	rule="$1"
	path="$2"
	printf 'public-hygiene: %s: %s\n' "$rule" "$path" >&2
	failures=$((failures + 1))
}

report_matches() {
	rule="$1"
	pattern="$2"
	path="$3"
	# Keep sensitive text out of output: consume grep's match text and emit only
	# the rule, tracked path, and line number.
	grep -nE "$pattern" "$REPO/$path" 2>/dev/null | while IFS=: read -r line_number _; do
		printf 'public-hygiene: %s: %s:%s\n' "$rule" "$path" "$line_number" >&2
	done
	if grep -qE "$pattern" "$REPO/$path" 2>/dev/null; then
		return 0
	fi
	return 1
}

tracked_file_list="$(mktemp "${TMPDIR:-/tmp}/tgctl-hygiene-files.XXXXXX")"
trap 'rm -f "$tracked_file_list"' EXIT HUP INT TERM
git -C "$REPO" ls-files >"$tracked_file_list"

while IFS= read -r path; do
	# A file removed in the working tree is absent from the proposed public tip.
	[ -e "$REPO/$path" ] || continue
	case "$path" in
		scripts/check_public_hygiene_test.sh)
			# This synthetic fixture intentionally contains every forbidden class.
			continue
			;;
		*.transcript.txt|*.live-report.txt|*.live-report.json|*_live_report.txt|*_live_report.json|*_simulation_report.json|*.sqlite|*.sqlite3|*.db|*.sqlite-wal|*.sqlite-shm|*.db-wal|*.db-shm|*.audit.log|audit.ndjson|*.session)
			report_path forbidden-artifact "$path"
			continue
			;;
	esac

	[ -f "$REPO/$path" ] || continue
	LC_ALL=C grep -Iq . "$REPO/$path" 2>/dev/null || continue

	if report_matches local-machine-path '(/Users/[A-Za-z0-9._-]+/|/home/[A-Za-z0-9._-]+/|[A-Za-z]:\\Users\\[^\\[:space:]]+\\)' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-invite-link 'https?://(t\.me|telegram\.me)/(joinchat/|\+)[A-Za-z0-9_-]+' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-phone '(TG_PHONE|TELEGRAM_PHONE|PHONE_NUMBER)[[:space:]]*=[[:space:]]*[^+[:space:]]*\+[0-9][0-9 ()-]{7,}' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-numeric-selector '(TG_(CHAT|USER|TARGET|FORUM|OWNER)_ID|CHAT|FORUM_CHAT|TARGET_USER|OWNER_ID)[[:space:]]*=[[:space:]]*[^[:space:]0-9-]*-?[0-9]{7,15}' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-owner-selector '(SELF_USERNAME|OWNER_USERNAME|TG_(OWNER|TARGET)_USERNAME)[[:space:]]*=[[:space:]]*[^@[:space:]]*@[A-Za-z0-9_]{5,}' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-human-selector '(^|[;&|[:space:]])(\./)?tg[[:space:]]+send[[:space:]]+[A-Z][A-Za-z]{2,}([[:space:]]|$)' "$path"; then failures=$((failures + 1)); fi
	if report_matches telegram-human-selector '(CHAT|FORUM_CHAT|TARGET_CHAT|SELF_CHAT|TG_(CHAT|TARGET)_SELECTOR)[[:space:]]*=[[:space:]]*[^A-Z[:space:]]*[A-Z][A-Za-z]{2,}([[:space:]]|$)' "$path"; then failures=$((failures + 1)); fi
done <"$tracked_file_list"

if [ "$failures" -ne 0 ]; then
	printf 'public-hygiene: failed with %s finding(s); matched values are redacted\n' "$failures" >&2
	exit 1
fi

printf 'public-hygiene: passed\n'
