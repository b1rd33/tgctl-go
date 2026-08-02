#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
IMPORT_SCRIPT="$SCRIPT_DIR/import_export_simulation.sh"
LIVE_SCRIPT="$SCRIPT_DIR/live_verify.sh"
ADMIN_SCRIPT="$SCRIPT_DIR/admin_topic_live_verify.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-live-target-test.XXXXXX")"
synthetic_username="$(printf '@%s_%s' synthetic test)"
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

new_case() {
	case_dir="$TMP_ROOT/$1"
	mkdir -p "$case_dir/tmp"
	: >"$case_dir/test.sqlite"
	cat >"$case_dir/tg" <<'FAKE'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_TG_LOG"
command_name="${1:-}"
if [ "$command_name" = "--account" ]; then command_name="${3:-}"; fi
if [ "${FAKE_STOP_COMMAND:-}" = "$command_name" ]; then
	printf '%s\n' 'intentional-stop-after-target-capture'
	exit 77
fi
case "$command_name" in
	send) printf '%s\n' '{"ok":true,"data":{"message_id":1}}' ;;
	folder-create) printf '%s\n' '{"ok":true,"data":{"folder_id":41}}' ;;
	topic-create) printf '%s\n' '{"ok":true,"data":{"topic_id":51}}' ;;
	folder-delete) printf '%s\n' 'intentional-stop-after-target-capture'; exit 1 ;;
	*) printf '%s\n' '{"ok":true,"data":{}}' ;;
esac
FAKE
	chmod +x "$case_dir/tg"
	printf '%s\n' "$case_dir"
}

run_live() {
	case_dir="$1"
	chat="$2"
	(
		cd "$case_dir"
		env PATH="$PATH" HOME="${HOME:-/tmp}" TMPDIR="$case_dir/tmp" \
			FAKE_TG_LOG="$case_dir/tg.log" FAKE_STOP_COMMAND=backfill \
			TGCTL_LIVE_TG_BIN="$case_dir/tg" TGCTL_LIVE_CHAT="$chat" \
			TGCTL_LIVE_SELF_USERNAME="$synthetic_username" TGCTL_LIVE_ACCOUNT=synthetic-test \
			bash "$LIVE_SCRIPT" >/dev/null 2>&1
	)
}

run_admin() {
	case_dir="$1"
	chat="$2"
	forum="$3"
	(
		cd "$case_dir"
		env PATH="$PATH" HOME="${HOME:-/tmp}" TMPDIR="$case_dir/tmp" \
			FAKE_TG_LOG="$case_dir/tg.log" FAKE_STOP_COMMAND=folder-create \
			TGCTL_LIVE_TG_BIN="$case_dir/tg" TGCTL_LIVE_CHAT="$chat" TGCTL_LIVE_FORUM_CHAT="$forum" \
			TGCTL_SOURCE_SESSION="$case_dir/source.session" TGCTL_SOURCE_DB="$case_dir/source.sqlite" \
			bash "$ADMIN_SCRIPT" >/dev/null 2>&1
	)
}

run_import() {
	case_dir="$1"
	shift
	(
		cd "$case_dir"
		env \
			PATH="$PATH" \
			HOME="${HOME:-/tmp}" \
			TMPDIR="$case_dir/tmp" \
			FAKE_TG_LOG="$case_dir/tg.log" \
			TGCTL_LIVE_TG_BIN="$case_dir/tg" \
			TGCTL_LIVE_CHAT=11001 \
			TGCTL_LIVE_ACCOUNT=synthetic-test \
			TGCTL_LIVE_DB="$case_dir/test.sqlite" \
			"$@" \
			bash "$IMPORT_SCRIPT" >/dev/null 2>&1
	)
}

assert_no_tg_calls() {
	case_dir="$1"
	if [ -s "$case_dir/tg.log" ]; then
		fail "$2 reached tg before target validation"
	fi
}

missing="$(new_case missing)"
run_import "$missing" || true
assert_no_tg_calls "$missing" missing-selectors

invalid_list="$(new_case invalid-list)"
run_import "$invalid_list" \
	TGCTL_LIVE_FOLDER_TARGETS=12001,invalid,12003,12004 \
	TGCTL_LIVE_FORUM_CHAT=13001 || true
assert_no_tg_calls "$invalid_list" invalid-folder-list

invalid_forum="$(new_case invalid-forum)"
run_import "$invalid_forum" \
	TGCTL_LIVE_FOLDER_TARGETS=12001,12002,12003,12004 \
	TGCTL_LIVE_FORUM_CHAT=invalid || true
assert_no_tg_calls "$invalid_forum" invalid-forum

explicit="$(new_case explicit)"
run_import "$explicit" \
	TGCTL_LIVE_FOLDER_TARGETS=12001,12002,12003,12004 \
	TGCTL_LIVE_FORUM_CHAT=13001 || true

for target in 12001 12002 12003 12004; do
	grep -Eq "^folder-create .*--include-chats ${target}( |$)" "$explicit/tg.log" ||
		fail "explicit folder target was not passed exactly"
done
folder_calls="$(grep -c '^folder-create ' "$explicit/tg.log")"
[ "$folder_calls" -eq 4 ] || fail "unexpected folder-create call count"

forum_list_calls="$(grep -c '^topics-list 13001 ' "$explicit/tg.log")"
[ "$forum_list_calls" -eq 1 ] || fail "explicit forum target was not used for topics-list"
topic_create_calls="$(grep -c '^topic-create 13001 ' "$explicit/tg.log")"
[ "$topic_create_calls" -eq 4 ] || fail "explicit forum target was not used for every topic-create"
summary_calls="$(grep -Ec '^send 13001 .*--topic ' "$explicit/tg.log")"
[ "$summary_calls" -eq 4 ] || fail "explicit forum target was not used for every topic summary"

if grep -Eq '^folder-create .*--include-chats 11001( |$)' "$explicit/tg.log"; then
	fail "self chat was substituted for an explicit folder target"
fi

invalid_live="$(new_case invalid-live)"
run_live "$invalid_live" HumanSelector || true
assert_no_tg_calls "$invalid_live" invalid-live-chat

explicit_live="$(new_case explicit-live)"
run_live "$explicit_live" -21001 || true
grep -q '^backfill -21001 ' "$explicit_live/tg.log" || fail "live chat selector was not forwarded exactly"

invalid_admin="$(new_case invalid-admin)"
: >"$invalid_admin/source.session"
: >"$invalid_admin/source.sqlite"
run_admin "$invalid_admin" HumanSelector 23001 || true
assert_no_tg_calls "$invalid_admin" invalid-admin-chat

explicit_admin="$(new_case explicit-admin)"
: >"$explicit_admin/source.session"
: >"$explicit_admin/source.sqlite"
run_admin "$explicit_admin" -22001 -23001 || true
grep -Eq '^--account [^ ]+ topics-list -23001 ' "$explicit_admin/tg.log" || fail "admin forum selector was not forwarded exactly"
grep -Eq '^--account [^ ]+ folder-create .*--include-chats -22001( |$)' "$explicit_admin/tg.log" || fail "admin chat selector was not forwarded exactly"

printf 'live target safety tests passed\n'
