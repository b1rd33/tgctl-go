VERSION := $(shell git describe --tags --dirty --always)

.PHONY: build docs-commands docs-commands-check
build:
	go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION)" -o tg ./cmd/tg

docs-commands:
	@set -eu; \
	tmp="$$(mktemp docs/.commands.md.tmp.XXXXXX)"; \
	trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
	go run ./tools/gen_commands_md/main.go > "$$tmp"; \
	mv "$$tmp" docs/commands.md

docs-commands-check:
	@set -eu; \
	tmp="$$(mktemp "$${TMPDIR:-/tmp}/tgctl-commands.XXXXXX")"; \
	trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
	go run ./tools/gen_commands_md/main.go > "$$tmp"; \
	if cmp -s docs/commands.md "$$tmp"; then exit 0; fi; \
	echo "docs/commands.md is out of date; run 'make docs-commands'" >&2; \
	diff -u docs/commands.md "$$tmp" || true; \
	exit 1
