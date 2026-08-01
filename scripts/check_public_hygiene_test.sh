#!/bin/sh
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

clean_repo="$(new_repo clean)"
HYGIENE_REPO="$clean_repo" "$CHECKER" >/dev/null || fail "clean repository failed"

expect_failure transcript forbidden-artifact scripts/live.transcript.txt 'synthetic transcript body'
expect_failure sqlite forbidden-artifact accounts/test/telegram.sqlite 'synthetic database body'
expect_failure local_path local-machine-path config.txt '/Users/example/Projects/private/session'
expect_failure invite telegram-invite-link notes.txt 'https://t.me/+SyntheticInviteToken123'
expect_failure phone telegram-phone config.env 'TG_PHONE=+15555550123'
expect_failure chat_id telegram-numeric-selector config.env 'TG_CHAT_ID=123456789'
expect_failure username telegram-owner-selector config.env 'SELF_USERNAME=@synthetic_owner'

untracked_repo="$(new_repo untracked)"
printf '%s\n' 'TG_CHAT_ID=123456789' >"$untracked_repo/local.env"
HYGIENE_REPO="$untracked_repo" "$CHECKER" >/dev/null || fail "untracked content was scanned"

printf 'public hygiene checker tests passed\n'
