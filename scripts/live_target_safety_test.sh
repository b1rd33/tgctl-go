#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
IMPORT_SCRIPT="$SCRIPT_DIR/import_export_simulation.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-live-target-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

new_case() {
	case_dir="$TMP_ROOT/$1"
	mkdir -p "$case_dir"
	: >"$case_dir/test.sqlite"
	cat >"$case_dir/tg" <<'FAKE'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_TG_LOG"
case "${1:-}" in
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

printf 'live target safety tests passed\n'
