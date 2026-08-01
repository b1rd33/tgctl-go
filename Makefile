VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)

.PHONY: build docs-commands docs-commands-check
build:
	go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION)" -o tg ./cmd/tg

docs-commands:
	@set -eu; \
	work="$$(mktemp -d "$${TMPDIR:-/tmp}/tgctl-docs.XXXXXX")"; \
	output=""; \
	cleanup() { if [ -n "$$output" ]; then rm -f "$$output"; fi; rm -f "$$work/tg"; rmdir "$$work" 2>/dev/null || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	output="$$(mktemp docs/.commands.md.tmp.XXXXXX)"; \
	go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION)" -o "$$work/tg" ./cmd/tg; \
	TGCTL_DOCS_BINARY="$$work/tg" go run ./tools/gen_commands_md/main.go > "$$output"; \
	chmod 0644 "$$output"; \
	mv "$$output" docs/commands.md

docs-commands-check:
	@set -eu; \
	work="$$(mktemp -d "$${TMPDIR:-/tmp}/tgctl-docs.XXXXXX")"; \
	output="$$work/commands.md"; \
	cleanup() { rm -f "$$output" "$$work/tg"; rmdir "$$work" 2>/dev/null || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	go build -ldflags "-X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION)" -o "$$work/tg" ./cmd/tg; \
	TGCTL_DOCS_BINARY="$$work/tg" go run ./tools/gen_commands_md/main.go > "$$output"; \
	if cmp -s docs/commands.md "$$output"; then exit 0; fi; \
	echo "docs/commands.md is out of date; run 'make docs-commands'" >&2; \
	diff -u docs/commands.md "$$output" || true; \
	exit 1
