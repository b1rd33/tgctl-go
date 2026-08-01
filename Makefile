ifeq ($(origin VERSION),undefined)
  IN_GIT_CHECKOUT := $(shell git rev-parse --is-inside-work-tree 2>/dev/null)
  ifeq ($(IN_GIT_CHECKOUT),true)
    VERSION := $(shell git describe --tags --dirty --always)
    BUILD_COMMIT := $(shell git rev-parse --verify HEAD)
    VERSION_SOURCE := git-describe
  else
    VERSION := dev
    BUILD_COMMIT :=
    VERSION_SOURCE :=
  endif
else
  BUILD_COMMIT ?=
  VERSION_SOURCE ?=
endif

VERSION_LDFLAGS := -X github.com/b1rd33/tgctl-go/internal/commands.Version=$(VERSION) -X github.com/b1rd33/tgctl-go/internal/commands.Commit=$(BUILD_COMMIT) -X github.com/b1rd33/tgctl-go/internal/commands.VersionSource=$(VERSION_SOURCE)

.PHONY: build docs-commands docs-commands-check public-hygiene
build:
	go build -ldflags "$(VERSION_LDFLAGS)" -o tg ./cmd/tg

docs-commands:
	@set -eu; \
	work="$$(mktemp -d "$${TMPDIR:-/tmp}/tgctl-docs.XXXXXX")"; \
	output=""; \
	cleanup() { if [ -n "$$output" ]; then rm -f "$$output"; fi; rm -f "$$work/tg"; rmdir "$$work" 2>/dev/null || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	output="$$(mktemp docs/.commands.md.tmp.XXXXXX)"; \
	go build -ldflags "$(VERSION_LDFLAGS)" -o "$$work/tg" ./cmd/tg; \
	TGCTL_DOCS_BINARY="$$work/tg" go run ./tools/gen_commands_md/main.go > "$$output"; \
	chmod 0644 "$$output"; \
	mv "$$output" docs/commands.md

docs-commands-check:
	@set -eu; \
	work="$$(mktemp -d "$${TMPDIR:-/tmp}/tgctl-docs.XXXXXX")"; \
	output="$$work/commands.md"; \
	cleanup() { rm -f "$$output" "$$work/tg"; rmdir "$$work" 2>/dev/null || true; }; \
	trap cleanup EXIT HUP INT TERM; \
	go build -ldflags "$(VERSION_LDFLAGS)" -o "$$work/tg" ./cmd/tg; \
	TGCTL_DOCS_BINARY="$$work/tg" go run ./tools/gen_commands_md/main.go > "$$output"; \
	if cmp -s docs/commands.md "$$output"; then exit 0; fi; \
	echo "docs/commands.md is out of date; run 'make docs-commands'" >&2; \
	diff -u docs/commands.md "$$output" || true; \
	exit 1

public-hygiene:
	./scripts/check_public_hygiene_test.sh
	./scripts/live_target_safety_test.sh
	./scripts/live_preflight_order_test.sh
	./scripts/admin_env_preflight_test.sh
	./scripts/live_workspace_test.sh
	./scripts/check_public_hygiene.sh
