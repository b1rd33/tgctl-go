#!/usr/bin/env bash
set -eu

CHECKER="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/check_public_hygiene.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tgctl-hygiene-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

new_repo() {
	repo="$TMP_ROOT/$1"
	mkdir -p "$repo"
	git -C "$repo" init -q
	printf '# clean fixture\n' >"$repo/README.md"
	git -C "$repo" add README.md
	printf '%s\n' "$repo"
}

expect_failure() {
	name="$1"
	rule="$2"
	path="$3"
	secret="$4"
	repo="$(new_repo "$name")"
	mkdir -p "$repo/$(dirname -- "$path")"
	printf '%s\n' "$secret" >"$repo/$path"
	git -C "$repo" add "$path"
	if HYGIENE_REPO="$repo" "$CHECKER" >"$repo/out" 2>&1; then
		fail "$name unexpectedly passed"
	fi
	grep -q "$rule" "$repo/out" || fail "$name did not report its rule"
	grep -q "$path" "$repo/out" || fail "$name did not report its path"
	if grep -Fq "$secret" "$repo/out"; then
		fail "$name printed sensitive content"
	fi
}

join() {
	local out="" part
	for part in "$@"; do out+="$part"; done
	printf '%s' "$out"
}

clean_repo="$(new_repo clean)"
HYGIENE_REPO="$clean_repo" "$CHECKER" >/dev/null || fail "clean repository failed"

expect_failure transcript forbidden-artifact scripts/live.transcript.txt 'synthetic transcript body'
expect_failure sqlite forbidden-artifact accounts/test/telegram.sqlite 'synthetic database body'
expect_failure local_path local-machine-path config.txt "$(join '/Users/' 'example/Projects/private/session')"
expect_failure invite telegram-invite-link notes.txt "$(join 'https://t.' 'me/+SyntheticInviteToken123')"
expect_failure phone telegram-phone config.env "$(join 'TG_PHONE=' '+1555' '5550123')"
expect_failure chat_id telegram-numeric-selector config.env "$(join 'TG_CHAT_' 'ID=1234' '56789')"
expect_failure username telegram-owner-selector config.env "$(join 'SELF_USER' 'NAME=@synthetic_owner')"
expect_failure human_selector telegram-human-selector scripts/live.sh "$(join './tg se' 'nd SyntheticPerson ' '"fixture" --allow-write --json')"
expect_failure human_assignment telegram-human-selector scripts/live.sh "$(join 'CH' 'AT=SyntheticPerson')"

symlink_repo="$(new_repo symlink)"
ln -s README.md "$symlink_repo/tracked-link"
git -C "$symlink_repo" add tracked-link
if HYGIENE_REPO="$symlink_repo" "$CHECKER" >"$symlink_repo/out" 2>&1; then
	fail "tracked symlink unexpectedly passed"
fi
grep -q tracked-symlink "$symlink_repo/out" || fail "tracked symlink rule missing"

binary_repo="$(new_repo binary)"
printf '\000\001\002' >"$binary_repo/payload.bin"
git -C "$binary_repo" add payload.bin
if HYGIENE_REPO="$binary_repo" "$CHECKER" >"$binary_repo/out" 2>&1; then
	fail "unexpected binary unexpectedly passed"
fi
grep -q unexpected-binary "$binary_repo/out" || fail "unexpected binary rule missing"

allowed_binary_repo="$(new_repo allowed-binary)"
mkdir -p "$allowed_binary_repo/docs/assets"
printf '\211PNG\r\n\032\n\000' >"$allowed_binary_repo/docs/assets/logo.png"
git -C "$allowed_binary_repo" add docs/assets/logo.png
HYGIENE_REPO="$allowed_binary_repo" "$CHECKER" >/dev/null || fail "allowlisted docs image failed"

index_repo="$(new_repo index-object)"
printf '%s\n' 'tracked clean text' >"$index_repo/config.txt"
git -C "$index_repo" add config.txt
rm "$index_repo/config.txt"
ln -s "$(join '/Users/' 'synthetic/private')" "$index_repo/config.txt"
HYGIENE_REPO="$index_repo" "$CHECKER" >/dev/null || fail "checker dereferenced a worktree symlink instead of the index"

hidden_index_repo="$(new_repo hidden-index)"
hidden_value="$(join 'TG_CHAT_' 'ID=9876' '54321')"
printf '%s\n' "$hidden_value" >"$hidden_index_repo/config.txt"
git -C "$hidden_index_repo" add config.txt
printf '%s\n' 'clean worktree replacement' >"$hidden_index_repo/config.txt"
if HYGIENE_REPO="$hidden_index_repo" "$CHECKER" >"$hidden_index_repo/out" 2>&1; then
	fail "checker trusted worktree content over the tracked object"
fi
grep -q telegram-numeric-selector "$hidden_index_repo/out" || fail "tracked object finding missing"
grep -Fq "$hidden_value" "$hidden_index_repo/out" && fail "tracked object value was printed"

space_repo="$(new_repo space-path)"
space_path='folder/with space.txt'
mkdir -p "$space_repo/folder"
printf '%s\n' "$(join 'TG_CHAT_' 'ID=2468' '13579')" >"$space_repo/$space_path"
git -C "$space_repo" add "$space_path"
if HYGIENE_REPO="$space_repo" "$CHECKER" >"$space_repo/out" 2>&1; then fail "space path unexpectedly passed"; fi
grep -q 'with\\ space.txt' "$space_repo/out" || fail "space path was not safely escaped"

newline_repo="$(new_repo newline-path)"
newline_path=$'bad\nname.transcript.txt'
printf '%s\n' 'synthetic body' >"$newline_repo/$newline_path"
git -C "$newline_repo" add "$newline_path"
if HYGIENE_REPO="$newline_repo" "$CHECKER" >"$newline_repo/out" 2>&1; then fail "newline path unexpectedly passed"; fi
grep -q forbidden-artifact "$newline_repo/out" || fail "newline path rule missing"
grep -q '\\n' "$newline_repo/out" || fail "newline path was not escaped"

archive_dir="$TMP_ROOT/no-git"
mkdir -p "$archive_dir"
if HYGIENE_REPO="$archive_dir" "$CHECKER" >"$archive_dir/out" 2>&1; then fail "archive execution unexpectedly passed"; fi
grep -q 'requires a Git checkout' "$archive_dir/out" || fail "archive diagnostic missing"

untracked_repo="$(new_repo untracked)"
printf '%s\n' "$(join 'TG_CHAT_' 'ID=1357' '92468')" >"$untracked_repo/local.env"
HYGIENE_REPO="$untracked_repo" "$CHECKER" >/dev/null || fail "untracked content was scanned"

printf 'public hygiene checker tests passed\n'
