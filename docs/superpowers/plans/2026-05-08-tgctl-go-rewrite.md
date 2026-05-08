# tgctl-go Implementation Plan
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.
**Goal:** Rewrite the Python `tgcli` command-line tool as a Go `tg` binary while preserving the observable CLI contract for envelopes, exit codes, safety, account isolation, resolver behavior, SQLite cache shape, and write idempotency.
**Architecture:** The Python source in `/Users/christiannikolov/Projects/tg-cli/` is the compatibility specification and must remain read-only. The Go port uses a Cobra shell, a dispatch chokepoint equivalent to `tgcli.dispatch.run_command`, focused internal packages mirroring the Python modules, and a gotd/td client adapter so most behavior is testable without Telegram network access.
**Tech Stack:** Go 1.23, gotd/td, Cobra, modernc.org/sqlite, GoReleaser
---

## Planned File Structure

- `go.mod`: module `github.com/b1rd33/tgctl-go`, Go 1.23, and top-level dependencies.
- `go.sum`: dependency checksums.
- `cmd/tg/main.go`: process entrypoint that calls `commands.Execute()` and exits with the returned code.
- `internal/commands/root.go`: Cobra root command, global flags, command registration, and top-level execution.
- `internal/commands/root_test.go`: Cobra shell tests for no command, command registration, and top-level JSON failure behavior.
- `internal/commands/flags.go`: shared `--json`, `--human`, `--allow-write`, `--dry-run`, `--idempotency-key`, `--fuzzy`, and destructive `--confirm <id>` flag helpers.
- `internal/commands/flags_test.go`: tests for mutually exclusive output flags and write flag defaults.
- `internal/commands/auth.go`: `login` and `me` commands, including cached/offline `me` behavior from `tgcli.commands.auth`.
- `internal/commands/auth_test.go`: auth command tests with fake client and store.
- `internal/commands/messages_read.go`: `show`, `search`, `list-msgs`, and `get-msg` local-cache read commands from `tgcli.commands.messages`.
- `internal/commands/messages_read_test.go`: read-command tests for resolver use, ordering, date filters, deleted-message filters, and JSON data shape.
- `internal/commands/messages_write.go`: `send`, `edit-msg`, `forward`, `pin-msg`, `unpin-msg`, `react`, and `mark-read`.
- `internal/commands/messages_write_test.go`: write command tests for write gates, fuzzy opt-in, dry-run short-circuit, topic warnings, rate limit before client calls, audit payloads, and idempotent replay.
- `internal/commands/media.go`: media upload/download commands and user-supplied path handling.
- `internal/commands/media_test.go`: media command tests, including `_safe_user_path` parity where media paths flow into filesystem/SQLite URI handling.
- `internal/commands/topics.go`: forum topic commands.
- `internal/commands/topics_test.go`: topic command tests preserving resolver, write gate, dry-run, and audit behavior.
- `internal/commands/folders.go`: folder list/create/update/delete commands.
- `internal/commands/folders_test.go`: folder command tests, including reserved folder id behavior and idempotency for writes.
- `internal/commands/admin.go`: administrative commands.
- `internal/commands/admin_test.go`: admin command tests preserving write safety and gotd request mapping.
- `internal/commands/destructive.go`: destructive operations such as `delete-msg`, `leave-chat`, user blocking, and session termination.
- `internal/commands/destructive_test.go`: typed-confirm, tombstone, left-marker, and destructive dry-run tests.
- `internal/commands/localdb.go`: `backfill`, `discover`, `sync-contacts`, local cache mutation, media download, and caps.
- `internal/commands/localdb_test.go`: local DB operation tests, including read-only rejection for local writes and backfill cap warnings/refusals.
- `internal/commands/live.go`: live/listen update ingestion.
- `internal/commands/live_test.go`: fake update-stream tests for cache writes and multi-account path use.
- `internal/commands/accounts.go`: account management commands equivalent to `tgcli.commands.accounts`.
- `internal/commands/accounts_test.go`: CLI tests for accounts add/use/list/show/remove.
- `internal/commands/doctor.go`: diagnostics command.
- `internal/commands/doctor_test.go`: diagnostic JSON/human output tests.
- `internal/output/output.go`: envelope structs, failure error object, stable exit codes, request ID generation, and output emission.
- `internal/output/output_test.go`: Go port of `tests/tgcli/test_output.py`.
- `internal/dispatch/dispatch.go`: single command chokepoint; creates request IDs, stores them in context, classifies errors, chooses JSON/human output, and appends one audit entry per invocation.
- `internal/dispatch/dispatch_test.go`: Go port of `tests/tgcli/test_dispatch.py`.
- `internal/audit/audit.go`: NDJSON audit writer, `audit_pre` equivalent, and owner-only chmod helper.
- `internal/audit/audit_test.go`: Go port of audit assertions from `tests/tgcli/test_safety.py`.
- `internal/store/store.go`: SQLite open/close helpers, read-write schema application, read-only URI connections, and transaction helpers.
- `internal/store/schema.go`: exact SQL schema for `tg_chats`, `tg_messages`, `tg_contacts`, `tg_me`, and `tg_idempotency`, plus migrations for `media_path`, `deleted`, and `left`.
- `internal/store/store_test.go`: schema, migration, read-only, and missing database tests from `tgcli.db`.
- `internal/store/models.go`: typed row structs and JSON/raw conversion helpers.
- `internal/store/messages.go`: message cache queries, upserts, tombstones, date filters, and deleted-message filtering.
- `internal/store/messages_test.go`: message query/upsert/tombstone tests.
- `internal/store/chats.go`: chat cache queries and upserts.
- `internal/store/chats_test.go`: chat query/upsert tests.
- `internal/store/contacts.go`: contact cache and `tg_me` cache queries/upserts.
- `internal/store/contacts_test.go`: contact and self-user cache tests.
- `internal/resolve/resolve.go`: int chat ID, `@username`, and accent-insensitive fuzzy title resolver.
- `internal/resolve/resolve_test.go`: Go port of `tests/tgcli/test_resolve.py`.
- `internal/text/text.go`: accent stripping, lowercase normalization, and shared text helpers.
- `internal/text/text_test.go`: normalization tests based on `tgcli.text.strip_accents`.
- `internal/safety/errors.go`: typed errors matching Python exception categories: bad args, write disallowed, needs confirm, local rate limit, session locked, missing credentials, and premium required.
- `internal/safety/gates.go`: read-only gate, write gate, typed confirm against resolved IDs, and explicit-or-fuzzy selector gate.
- `internal/safety/gates_test.go`: Go ports of gate assertions from `tests/tgcli/test_safety.py`, `test_phase8_readonly.py`, and `test_phase9_typed_confirm.py`.
- `internal/safety/ratelimit.go`: sliding-window local rate limiter and rapid-send warning watcher.
- `internal/safety/ratelimit_test.go`: rate limiter tests from `tests/tgcli/test_safety.py`.
- `internal/safety/sessionlock_unix.go`: Unix `flock` implementation for `<session>.lock`.
- `internal/safety/sessionlock_windows.go`: Windows `LockFileEx` implementation for `<session>.lock`.
- `internal/safety/sessionlock_test.go`: lock-wait tests from `tests/tgcli/test_phase8_lockwait.py`.
- `internal/idempotency/idempotency.go`: lookup and record helpers over `tg_idempotency`.
- `internal/idempotency/idempotency_test.go`: Go port of `tests/tgcli/test_idempotency.py`.
- `internal/accounts/accounts.go`: account name validation, account directory creation, current account selection, and path resolution.
- `internal/accounts/migrate.go`: one-time migration from root files into `accounts/default/`.
- `internal/accounts/accounts_test.go`: multi-account tests, including account isolation and invalid name rejection.
- `internal/env/env.go`: `.env` loader with shell environment precedence.
- `internal/env/env_test.go`: `.env` parsing and precedence tests.
- `internal/client/client.go`: narrow Telegram client interface used by commands.
- `internal/client/gotd.go`: gotd/td production client implementation.
- `internal/client/auth.go`: credential validation and login flow.
- `internal/client/errors.go`: gotd/td error classification into dispatch-level errors.
- `internal/client/fake_test.go`: fake client primitives for command tests.
- `internal/client/client_test.go`: credential and factory tests based on `tgcli.client`.
- `internal/media/media.go`: media type detection, download path assembly, and upload request assembly.
- `internal/media/media_test.go`: media type/path tests derived from `tgcli.commands.messages` media helpers and media command behavior.
- `internal/config/paths.go`: root path binding, env path overrides, account path binding, and safe user path validation.
- `internal/config/paths_test.go`: Go port of `tests/tgcli/test_phase8_paths.py`.
- `.github/workflows/ci.yml`: Linux, macOS, and Windows CI running `go test ./...`, `go test -race ./...` where supported, and `go build ./cmd/tg`.
- `.goreleaser.yaml`: multi-arch binary release configuration for GitHub Releases.

## Python Compatibility Notes

Required Python files read for this plan: `tgcli/__main__.py`, `tgcli/output.py`, `tgcli/safety.py`, `tgcli/dispatch.py`, `tgcli/resolve.py`, `tgcli/client.py`, `tgcli/db.py`, `tgcli/idempotency.py`, `tgcli/accounts.py`, `tgcli/env.py`, `tgcli/text.py`, `tgcli/commands/_common.py`, `tgcli/commands/auth.py`, `tgcli/commands/messages.py`, `tests/tgcli/test_safety.py`, `test_idempotency.py`, `test_resolve.py`, `test_dispatch.py`, `test_output.py`, `test_phase9_typed_confirm.py`, `test_phase8_paths.py`, and `test_phase8_lockwait.py`. Key contracts: `tgcli.output` owns the JSON envelopes and stable exit code values; `tgcli.dispatch.run_command` is the single envelope/audit/error-mapping chokepoint; `tgcli.safety` defines the write gate, read-only gate, typed confirm gate, fuzzy selector gate, sliding-window rate limiter, rapid-send watcher, and `audit_pre`; `tgcli.db` defines the exact cache schema and read-only connection behavior; `tgcli.resolve` resolves `int -> @username -> fuzzy title substring` using accent-insensitive matching; `tgcli.accounts` isolates `accounts/<name>/{tg.session,telegram.sqlite,audit.log,media/}` and validates names with `[A-Za-z0-9][A-Za-z0-9_-]{0,63}`.

## Phase 1: Package skeleton + Cobra shell + envelope + exit codes

### Task 1: Initialize module, binary entrypoint, and package skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/tg/main.go`
- Create: `internal/commands/root.go`
- Test: `internal/commands/root_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/commands/root_test.go`:

```go
package commands

import (
	"bytes"
	"testing"
)

func TestNewRootCommandHasNameAndNoCommandReturnsOK(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root.Use != "tg" {
		t.Fatalf("Use = %q, want tg", root.Use)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("Telegram agent CLI")) {
		t.Fatalf("stderr help = %q, want description", stderr.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run TestNewRootCommandHasNameAndNoCommandReturnsOK -count=1`

Expected failure:

```text
go: cannot find main module, but found no go.mod
```

- [ ] **Step 3: Write minimal implementation**

Create `go.mod`:

```go
module github.com/b1rd33/tgctl-go

go 1.23

require github.com/spf13/cobra v1.8.1
```

Create `cmd/tg/main.go`:

```go
package main

import (
	"os"

	"github.com/b1rd33/tgctl-go/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
```

Create `internal/commands/root.go`:

```go
package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.SetOut(io.Discard)
	cmd.SetErr(os.Stderr)
	return cmd
}

func ExecuteRoot(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return 1
	}
	return 0
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./internal/commands -run TestNewRootCommandHasNameAndNoCommandReturnsOK -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/commands	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/tg/main.go internal/commands/root.go internal/commands/root_test.go
git commit -m "feat: initialize cobra shell"
```

### Task 2: Add stable exit codes

**Files:**
- Create: `internal/output/output.go`
- Test: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/output/output_test.go`:

```go
package output

import "testing"

func TestExitCodeValuesAreStable(t *testing.T) {
	tests := map[ExitCode]int{
		OK:              0,
		Generic:         1,
		BadArgs:         2,
		NotAuthed:       3,
		NotFound:        4,
		FloodWait:       5,
		WriteDisallowed: 6,
		NeedsConfirm:    7,
		LocalRateLimit:  8,
		PremiumRequired: 9,
	}
	for code, want := range tests {
		if int(code) != want {
			t.Fatalf("%s = %d, want %d", code.String(), code, want)
		}
	}
}

func TestExitCodeStringNamesMatchPythonEnum(t *testing.T) {
	tests := map[ExitCode]string{
		OK:              "OK",
		Generic:         "GENERIC",
		BadArgs:         "BAD_ARGS",
		NotAuthed:       "NOT_AUTHED",
		NotFound:        "NOT_FOUND",
		FloodWait:       "FLOOD_WAIT",
		WriteDisallowed: "WRITE_DISALLOWED",
		NeedsConfirm:    "NEEDS_CONFIRM",
		LocalRateLimit:  "LOCAL_RATE_LIMIT",
		PremiumRequired: "PREMIUM_REQUIRED",
	}
	for code, want := range tests {
		if got := code.String(); got != want {
			t.Fatalf("String(%d) = %q, want %q", code, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output -run TestExitCode -count=1`

Expected failure:

```text
# github.com/b1rd33/tgctl-go/internal/output [github.com/b1rd33/tgctl-go/internal/output.test]
internal/output/output_test.go:6:15: undefined: ExitCode
```

- [ ] **Step 3: Write minimal implementation**

Create `internal/output/output.go`:

```go
package output

type ExitCode int

const (
	OK ExitCode = iota
	Generic
	BadArgs
	NotAuthed
	NotFound
	FloodWait
	WriteDisallowed
	NeedsConfirm
	LocalRateLimit
	PremiumRequired
)

func (c ExitCode) String() string {
	switch c {
	case OK:
		return "OK"
	case Generic:
		return "GENERIC"
	case BadArgs:
		return "BAD_ARGS"
	case NotAuthed:
		return "NOT_AUTHED"
	case NotFound:
		return "NOT_FOUND"
	case FloodWait:
		return "FLOOD_WAIT"
	case WriteDisallowed:
		return "WRITE_DISALLOWED"
	case NeedsConfirm:
		return "NEEDS_CONFIRM"
	case LocalRateLimit:
		return "LOCAL_RATE_LIMIT"
	case PremiumRequired:
		return "PREMIUM_REQUIRED"
	default:
		return "GENERIC"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output -run TestExitCode -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/output	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/output/output.go internal/output/output_test.go
git commit -m "feat: add stable exit codes"
```

### Task 3: Add success and failure envelope builders

**Files:**
- Modify: `internal/output/output.go`
- Modify: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/output/output_test.go`:

```go
func TestSuccessEnvelopeShape(t *testing.T) {
	env := Success("stats", map[string]any{"chats": 5}, "req-abc", nil)
	if !env.OK {
		t.Fatalf("ok = false")
	}
	if env.Command != "stats" {
		t.Fatalf("command = %q", env.Command)
	}
	if env.RequestID != "req-abc" {
		t.Fatalf("request_id = %q", env.RequestID)
	}
	if len(env.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want empty slice", env.Warnings)
	}
}

func TestSuccessEnvelopeWithWarnings(t *testing.T) {
	env := Success("stats", map[string]any{}, "r", []string{"truncated"})
	if len(env.Warnings) != 1 || env.Warnings[0] != "truncated" {
		t.Fatalf("warnings = %#v", env.Warnings)
	}
}

func TestFailEnvelopeShape(t *testing.T) {
	env := Fail("messages.send", FloodWait, "wait 30s", "req-xyz", map[string]any{
		"retry_after_seconds": 30,
	})
	if env.OK {
		t.Fatalf("failure envelope ok = true")
	}
	if env.Command != "messages.send" {
		t.Fatalf("command = %q", env.Command)
	}
	if env.Error == nil {
		t.Fatalf("error is nil")
	}
	if env.Error.Code != "FLOOD_WAIT" || env.Error.Message != "wait 30s" {
		t.Fatalf("error = %#v", env.Error)
	}
	if got := env.Error.Extra["retry_after_seconds"]; got != 30 {
		t.Fatalf("retry_after_seconds = %#v, want 30", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output -run 'Test(Success|Fail)' -count=1`

Expected failure:

```text
internal/output/output_test.go:41:9: undefined: Success
internal/output/output_test.go:64:9: undefined: Fail
```

- [ ] **Step 3: Write minimal implementation**

Replace `internal/output/output.go` with:

```go
package output

type ExitCode int

const (
	OK ExitCode = iota
	Generic
	BadArgs
	NotAuthed
	NotFound
	FloodWait
	WriteDisallowed
	NeedsConfirm
	LocalRateLimit
	PremiumRequired
)

type Envelope struct {
	OK        bool        `json:"ok"`
	Command   string      `json:"command"`
	RequestID string      `json:"request_id"`
	Data      any         `json:"data,omitempty"`
	Warnings  []string    `json:"warnings,omitempty"`
	Error     *ErrorBody  `json:"error,omitempty"`
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Extra   map[string]any `json:"-"`
}

func Success(command string, data any, requestID string, warnings []string) Envelope {
	if warnings == nil {
		warnings = []string{}
	}
	return Envelope{
		OK:        true,
		Command:   command,
		RequestID: requestID,
		Data:      data,
		Warnings:  append([]string{}, warnings...),
	}
}

func Fail(command string, code ExitCode, message string, requestID string, extra map[string]any) Envelope {
	if extra == nil {
		extra = map[string]any{}
	}
	return Envelope{
		OK:        false,
		Command:   command,
		RequestID: requestID,
		Error: &ErrorBody{
			Code:    code.String(),
			Message: message,
			Extra:   extra,
		},
	}
}

func (c ExitCode) String() string {
	switch c {
	case OK:
		return "OK"
	case Generic:
		return "GENERIC"
	case BadArgs:
		return "BAD_ARGS"
	case NotAuthed:
		return "NOT_AUTHED"
	case NotFound:
		return "NOT_FOUND"
	case FloodWait:
		return "FLOOD_WAIT"
	case WriteDisallowed:
		return "WRITE_DISALLOWED"
	case NeedsConfirm:
		return "NEEDS_CONFIRM"
	case LocalRateLimit:
		return "LOCAL_RATE_LIMIT"
	case PremiumRequired:
		return "PREMIUM_REQUIRED"
	default:
		return "GENERIC"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output -run 'Test(Success|Fail|ExitCode)' -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/output	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/output/output.go internal/output/output_test.go
git commit -m "feat: add output envelopes"
```

### Task 4: Marshal failure error extras byte-for-byte reasonably close to Python

**Files:**
- Modify: `internal/output/output.go`
- Modify: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/output/output_test.go`:

```go
func TestEnvelopeJSONMatchesPythonShape(t *testing.T) {
	env := Fail("x", LocalRateLimit, "slow down", "req-1", map[string]any{
		"retry_after_seconds": 2.5,
	})
	got, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ok":false,"command":"x","request_id":"req-1","error":{"code":"LOCAL_RATE_LIMIT","message":"slow down","retry_after_seconds":2.5}}`
	if string(got) != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func TestSuccessEnvelopeJSONIncludesEmptyWarnings(t *testing.T) {
	env := Success("stats", map[string]any{"x": 1}, "r", nil)
	got, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ok":true,"command":"stats","request_id":"r","data":{"x":1},"warnings":[]}`
	if string(got) != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}
```

Add this import block to `internal/output/output_test.go`:

```go
import (
	"encoding/json"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output -run 'TestEnvelopeJSON|TestSuccessEnvelopeJSON' -count=1`

Expected failure:

```text
json = {"ok":false,"command":"x","request_id":"req-1","error":{"code":"LOCAL_RATE_LIMIT","message":"slow down"}}, want {"ok":false,"command":"x","request_id":"req-1","error":{"code":"LOCAL_RATE_LIMIT","message":"slow down","retry_after_seconds":2.5}}
```

- [ ] **Step 3: Write minimal implementation**

Replace the `Envelope` and `ErrorBody` definitions and add marshal methods in `internal/output/output.go`:

```go
type Envelope struct {
	OK        bool       `json:"ok"`
	Command   string     `json:"command"`
	RequestID string     `json:"request_id"`
	Data      any        `json:"data,omitempty"`
	Warnings  []string   `json:"warnings"`
	Error     *ErrorBody `json:"error,omitempty"`
}

type ErrorBody struct {
	Code    string
	Message string
	Extra   map[string]any
}

func (e ErrorBody) MarshalJSON() ([]byte, error) {
	fields := map[string]any{
		"code":    e.Code,
		"message": e.Message,
	}
	for k, v := range e.Extra {
		fields[k] = v
	}
	type ordered struct {
		Code              string `json:"code"`
		Message           string `json:"message"`
		RetryAfterSeconds any    `json:"retry_after_seconds,omitempty"`
		TelegramError     any    `json:"telegram_error,omitempty"`
		Candidates        any    `json:"candidates,omitempty"`
	}
	if len(e.Extra) == 0 {
		return json.Marshal(struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: e.Code, Message: e.Message})
	}
	if len(e.Extra) <= 3 {
		return json.Marshal(ordered{
			Code:              e.Code,
			Message:           e.Message,
			RetryAfterSeconds: e.Extra["retry_after_seconds"],
			TelegramError:     e.Extra["telegram_error"],
			Candidates:        e.Extra["candidates"],
		})
	}
	return json.Marshal(fields)
}
```

Add the import to `internal/output/output.go`:

```go
import "encoding/json"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output -run 'TestEnvelopeJSON|TestSuccessEnvelopeJSON|TestFailEnvelopeShape' -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/output	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/output/output.go internal/output/output_test.go
git commit -m "feat: marshal python-compatible envelopes"
```

### Task 5: Add request ID generation

**Files:**
- Modify: `internal/output/output.go`
- Modify: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/output/output_test.go`:

```go
func TestNewRequestIDFormatAndUniqueness(t *testing.T) {
	first := NewRequestID()
	second := NewRequestID()
	matched, err := regexp.MatchString(`^req-[0-9a-f]{8}$`, first)
	if err != nil {
		t.Fatalf("regexp: %v", err)
	}
	if !matched {
		t.Fatalf("request id = %q, want req-<8 hex>", first)
	}
	if first == second {
		t.Fatalf("request ids repeated: %q", first)
	}
}
```

Add `regexp` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output -run TestNewRequestIDFormatAndUniqueness -count=1`

Expected failure:

```text
internal/output/output_test.go:112:11: undefined: NewRequestID
```

- [ ] **Step 3: Write minimal implementation**

Add to `internal/output/output.go`:

```go
func NewRequestID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("req-%x", b)
}
```

Add imports:

```go
import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output -run TestNewRequestIDFormatAndUniqueness -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/output	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/output/output.go internal/output/output_test.go
git commit -m "feat: generate request ids"
```

### Task 6: Add JSON and human emission

**Files:**
- Modify: `internal/output/output.go`
- Modify: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/output/output_test.go`:

```go
func TestEmitJSONSuccessReturnsZero(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Emit(Success("stats", map[string]any{"x": 1}, "r", nil), EmitOptions{
		JSON:   true,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != OK {
		t.Fatalf("code = %d, want OK", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var parsed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal stdout: %v", err)
	}
	if parsed["ok"] != true {
		t.Fatalf("ok = %#v", parsed["ok"])
	}
}

func TestEmitJSONFailureReturnsMappedExitCode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Emit(Fail("x", NotFound, "missing", "r", nil), EmitOptions{
		JSON:   true,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != NotFound {
		t.Fatalf("code = %d, want NOT_FOUND", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"code":"NOT_FOUND"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestEmitHumanFailureWritesStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Emit(Fail("stats", NotFound, "no DB", "r", nil), EmitOptions{
		JSON:   false,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != NotFound {
		t.Fatalf("code = %d, want NOT_FOUND", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("ERROR [NOT_FOUND]: no DB")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
```

Add `bytes` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/output -run 'TestEmit' -count=1`

Expected failure:

```text
internal/output/output_test.go:129:10: undefined: Emit
internal/output/output_test.go:129:65: undefined: EmitOptions
```

- [ ] **Step 3: Write minimal implementation**

Add to `internal/output/output.go`:

```go
type EmitOptions struct {
	JSON           bool
	Stdout         io.Writer
	Stderr         io.Writer
	HumanFormatter func(any)
}

func Emit(envelope Envelope, opts EmitOptions) ExitCode {
	stdout := opts.Stdout
	stderr := opts.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	if opts.JSON {
		encoded, err := json.Marshal(envelope)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "ERROR [GENERIC]: %v\n", err)
			return Generic
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
	} else if envelope.OK {
		if opts.HumanFormatter != nil {
			opts.HumanFormatter(envelope.Data)
		} else {
			encoded, _ := json.MarshalIndent(envelope.Data, "", "  ")
			_, _ = fmt.Fprintln(stdout, string(encoded))
		}
	} else {
		_, _ = fmt.Fprintf(stderr, "ERROR [%s]: %s\n", envelope.Error.Code, envelope.Error.Message)
	}

	if envelope.OK {
		return OK
	}
	return ExitCodeFromString(envelope.Error.Code)
}

func ExitCodeFromString(name string) ExitCode {
	switch name {
	case "OK":
		return OK
	case "BAD_ARGS":
		return BadArgs
	case "NOT_AUTHED":
		return NotAuthed
	case "NOT_FOUND":
		return NotFound
	case "FLOOD_WAIT":
		return FloodWait
	case "WRITE_DISALLOWED":
		return WriteDisallowed
	case "NEEDS_CONFIRM":
		return NeedsConfirm
	case "LOCAL_RATE_LIMIT":
		return LocalRateLimit
	case "PREMIUM_REQUIRED":
		return PremiumRequired
	default:
		return Generic
	}
}
```

Add imports:

```go
import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/output -run 'TestEmit|TestEnvelopeJSON|TestExitCode' -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/output	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/output/output.go internal/output/output_test.go
git commit -m "feat: emit json and human output"
```

### Task 7: Add root global flags matching Python

**Files:**
- Modify: `internal/commands/root.go`
- Modify: `internal/commands/root_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/commands/root_test.go`:

```go
func TestRootCommandGlobalFlags(t *testing.T) {
	root := NewRootCommand()

	flags := []string{"read-only", "lock-wait", "full", "account"}
	for _, name := range flags {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("missing persistent flag --%s", name)
		}
	}
}

func TestRootCommandPropagatesGlobalFlagValues(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--read-only", "--lock-wait", "1.5", "--full", "--account", "work"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	cfg := RootConfigFrom(root)
	if !cfg.ReadOnly {
		t.Fatalf("ReadOnly = false")
	}
	if cfg.LockWaitSeconds != 1.5 {
		t.Fatalf("LockWaitSeconds = %v, want 1.5", cfg.LockWaitSeconds)
	}
	if !cfg.Full {
		t.Fatalf("Full = false")
	}
	if cfg.Account != "work" {
		t.Fatalf("Account = %q, want work", cfg.Account)
	}
}
```

Add `io` to the test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run 'TestRootCommandGlobalFlags|TestRootCommandPropagatesGlobalFlagValues' -count=1`

Expected failure:

```text
internal/commands/root_test.go:38:9: undefined: RootConfigFrom
```

- [ ] **Step 3: Write minimal implementation**

Replace `internal/commands/root.go` with:

```go
package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

type RootConfig struct {
	ReadOnly        bool
	LockWaitSeconds float64
	Full            bool
	Account         string
}

type rootConfigKey struct{}

func NewRootCommand() *cobra.Command {
	cfg := &RootConfig{}
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.PersistentFlags().BoolVar(&cfg.ReadOnly, "read-only", false, "Reject any write to Telegram or local DB. Also via TG_READONLY=1.")
	cmd.PersistentFlags().Float64Var(&cfg.LockWaitSeconds, "lock-wait", 0, "Seconds to wait for the Telegram session lock (default 0 = fail-fast).")
	cmd.PersistentFlags().BoolVar(&cfg.Full, "full", false, "Disable column truncation in human-mode output.")
	cmd.PersistentFlags().StringVar(&cfg.Account, "account", "", "Account name (uses accounts/<NAME>/). Default selected via accounts-use or TG_ACCOUNT env.")
	cmd.SetContext(context.WithValue(cmd.Context(), rootConfigKey{}, cfg))
	cmd.SetOut(io.Discard)
	cmd.SetErr(os.Stderr)
	return cmd
}

func RootConfigFrom(cmd *cobra.Command) RootConfig {
	if cfg, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok && cfg != nil {
		return *cfg
	}
	return RootConfig{}
}

func ExecuteRoot(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return 1
	}
	return 0
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/commands -run 'TestRootCommand' -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/commands	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/commands/root.go internal/commands/root_test.go
git commit -m "feat: add root global flags"
```

### Task 8: Add output flag helper and a `version` command proving registration

**Files:**
- Create: `internal/commands/flags.go`
- Modify: `internal/commands/root.go`
- Modify: `internal/commands/root_test.go`
- Test: `internal/commands/flags_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/commands/flags_test.go`:

```go
package commands

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestAddOutputFlagsAddsJSONAndHumanMutuallyExclusive(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	AddOutputFlags(cmd)
	if cmd.Flags().Lookup("json") == nil {
		t.Fatalf("missing --json")
	}
	if cmd.Flags().Lookup("human") == nil {
		t.Fatalf("missing --human")
	}
	cmd.SetArgs([]string{"--json", "--human"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected mutually exclusive flag error")
	}
}
```

Append to `internal/commands/root_test.go`:

```go
func TestVersionCommandRegistered(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"version", "--json"})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"command":"version"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"warnings":[]`)) {
		t.Fatalf("stdout = %q, want success envelope with warnings", stdout.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/commands -run 'TestAddOutputFlags|TestVersionCommandRegistered' -count=1`

Expected failure:

```text
internal/commands/flags_test.go:12:2: undefined: AddOutputFlags
```

- [ ] **Step 3: Write minimal implementation**

Create `internal/commands/flags.go`:

```go
package commands

import "github.com/spf13/cobra"

func AddOutputFlags(cmd *cobra.Command) {
	group := "output"
	cmd.Flags().Bool("json", false, "Force JSON envelope output (default when stdout is not a TTY)")
	cmd.Flags().Bool("human", false, "Force human-readable output (default on a TTY)")
	cmd.MarkFlagsMutuallyExclusive("json", "human")
	cmd.Flags().Lookup("json").Annotations = map[string][]string{"group": []string{group}}
	cmd.Flags().Lookup("human").Annotations = map[string][]string{"group": []string{group}}
}

func jsonMode(cmd *cobra.Command) bool {
	jsonFlag, _ := cmd.Flags().GetBool("json")
	humanFlag, _ := cmd.Flags().GetBool("human")
	if jsonFlag {
		return true
	}
	if humanFlag {
		return false
	}
	return true
}
```

Modify `internal/commands/root.go` to import `github.com/b1rd33/tgctl-go/internal/output` and call a registration helper:

```go
func NewRootCommand() *cobra.Command {
	cfg := &RootConfig{}
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}
	cmd.PersistentFlags().BoolVar(&cfg.ReadOnly, "read-only", false, "Reject any write to Telegram or local DB. Also via TG_READONLY=1.")
	cmd.PersistentFlags().Float64Var(&cfg.LockWaitSeconds, "lock-wait", 0, "Seconds to wait for the Telegram session lock (default 0 = fail-fast).")
	cmd.PersistentFlags().BoolVar(&cfg.Full, "full", false, "Disable column truncation in human-mode output.")
	cmd.PersistentFlags().StringVar(&cfg.Account, "account", "", "Account name (uses accounts/<NAME>/). Default selected via accounts-use or TG_ACCOUNT env.")
	cmd.SetContext(context.WithValue(cmd.Context(), rootConfigKey{}, cfg))
	cmd.SetOut(io.Discard)
	cmd.SetErr(os.Stderr)
	registerVersion(cmd)
	return cmd
}

func registerVersion(root *cobra.Command) {
	version := &cobra.Command{
		Use:          "version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			env := output.Success("version", map[string]any{"version": "dev"}, output.NewRequestID(), nil)
			code := output.Emit(env, output.EmitOptions{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			})
			if code != output.OK {
				return fmt.Errorf("version failed")
			}
			return nil
		},
	}
	AddOutputFlags(version)
	root.AddCommand(version)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/commands -run 'TestAddOutputFlags|TestVersionCommandRegistered|TestRootCommand' -count=1`

Expected pass:

```text
ok  	github.com/b1rd33/tgctl-go/internal/commands	0.00s
```

- [ ] **Step 5: Commit**

```bash
git add internal/commands/root.go internal/commands/root_test.go internal/commands/flags.go internal/commands/flags_test.go
git commit -m "feat: add output flags and version command"
```

## Phase 2: Output framework + dispatch chokepoint + audit log

Port `tgcli.dispatch.py`, `tgcli.output.py`, and the audit portions of `tgcli.safety.py` into `internal/dispatch`, `internal/output`, and `internal/audit`. The goal is that every command, including future Cobra commands, flows through one function analogous to `run_command(name, args, runner, human_formatter, audit_path)` that generates `req-<8 hex>`, makes that request ID available to runners, emits `{ok:true, command, request_id, data, warnings}` on success, emits `{ok:false, command, request_id, error:{code,message,...}}` on failure, and appends exactly one generic audit line per invocation. Tests must port `tests/tgcli/test_dispatch.py` and `tests/tgcli/test_output.py`: success envelope, async-equivalent runner support via `context.Context`, audit success/failure entries, ambiguous resolver candidates serialized as `[[id,title],...]`, local rate-limit `retry_after_seconds`, flood-wait mapping to exit 5, premium-required mapping to exit 9, unknown errors to `GENERIC`, JSON mode to stdout only, and human failure to stderr. Implement `internal/audit.Write` and `internal/audit.Pre` so `audit_pre` entries include `ts`, `phase:"before"`, `cmd`, `request_id`, `resolved_chat_id`, `resolved_chat_title`, `telethon_method`, `payload_preview`, and `dry_run`, matching `tgcli.safety.audit_pre`.

## Phase 3: SQLite store + schema + read-only/RW connect

Port `tgcli.db.py` into `internal/store` with `modernc.org/sqlite`, preserving the exact tables and column names for `tg_chats`, `tg_messages`, `tg_contacts`, `tg_me`, and `tg_idempotency`; the Go schema must include the current migrated columns `tg_messages.media_path`, `tg_messages.deleted INTEGER DEFAULT 0`, and `tg_chats.left INTEGER DEFAULT 0` even though the Python `SCHEMA` plus `_migrate` reaches that final state in two steps. `Connect` opens read-write, runs `PRAGMA journal_mode=WAL`, applies schema/migrations, commits, and chmods files owner-only where supported; `ConnectReadonly` must fail with a `DatabaseMissing`-equivalent error when the DB does not exist and must use a SQLite URI with `mode=ro` so it never writes or migrates. Tests should cover table existence, index existence, read-only missing DB mapping to `NOT_FOUND`, no migration writes in read-only mode, `decode_raw_json` behavior from `tgcli.commands._common`, and path URI safety from `tests/tgcli/test_phase8_paths.py` via `internal/config.SafeUserPath`.

## Phase 4: Resolver (int / @username / fuzzy)

Port `tgcli.resolve.py` and `tgcli.text.py` into `internal/resolve` and `internal/text`. `ResolveChatDB` must trim input, reject empty selectors as not found, attempt integer chat IDs first with Python-compatible behavior where malformed integers like `"--123"` fall through to fuzzy instead of leaking parse errors, resolve `@username` case-insensitively against `LOWER(username)`, and finally perform accent-insensitive lowercase title substring matching over `tg_chats` ordered by `chat_id`. Tests must directly port `tests/tgcli/test_resolve.py`: exact integer hit, username hit, `"müller"` matching `"Bjørn Müller"` through normalization, ambiguous `"Bj"` yielding candidates `[(1,"Bjørn Müller"), (2,"Bjarne Test Group")]`, no match raising `NotFound`, and malformed int falling through to fuzzy. The write-side fuzzy opt-in is not inside the resolver; it remains a safety gate in Phase 5 so reads auto-allow fuzzy while writes require `--fuzzy`.

## Phase 5: Safety gates + session lock

Port `tgcli.safety.py` and `tgcli.client.acquire_session_lock` into `internal/safety`. Preserve the write pipeline order for every Telegram-side write: read-only/write gate first, destructive typed confirm after resolving the target, fuzzy selector gate before write resolution, dry-run short-circuit before local rate limiting and client creation, local sliding-window limiter of 20 writes per 60 seconds, `audit_pre`, MTProto call, and post/invocation audit through dispatch sharing the same request ID. Tests must port `tests/tgcli/test_safety.py`, `tests/tgcli/test_phase8_readonly.py`, `tests/tgcli/test_phase9_typed_confirm.py`, and `tests/tgcli/test_phase8_lockwait.py`: only `--allow-write` or `TG_ALLOW_WRITE=1` passes, `TG_ALLOW_WRITE=yes` does not pass, `--read-only` and `TG_READONLY=1` reject writes even with allow-write, typed confirm compares string-trimmed values to the resolved ID and rejects raw selector/substrings/hex forms, `RequireExplicitOrFuzzy` allows signed integers and `@username` but rejects titles without `--fuzzy`, local limiter blocks without recording extra events, and session locking uses `<session>.lock` with fail-fast or 100ms retry until `--lock-wait` timeout. Use build-tagged files: Unix `flock`, Windows `LockFileEx`; keep the lock handle process-global so the lock is held until process exit, matching Python.

## Phase 6: Idempotency cache

Port `tgcli.idempotency.py` into `internal/idempotency`, using the exact `tg_idempotency` table columns and storing full success envelopes as JSON in `result_json`. The invariant from Python writes is that `--idempotency-key <name>` is checked only after the write gate and DB connection, a missing or empty key returns no replay, a key reused for the same command returns the cached envelope data with command runners adding `idempotent_replay:true`, and a key reused for a different command raises `BAD_ARGS` with a message containing `already used`. Tests must port `tests/tgcli/test_idempotency.py` exactly: nil/empty lookup returns nil, record plus lookup round-trips an envelope containing `ok`, `command`, `request_id`, `data`, and `warnings`, and command mismatch raises the safety bad-args error. Command tests in later phases must verify that write-gate failure still blocks even if a valid cached idempotency row exists, as covered by Python write tests.

## Phase 7: gotd/td client wiring + auth (login)

Port `tgcli.client.py` and `tgcli.commands.auth.py` into `internal/client` and `internal/commands/auth.go` using gotd/td. `EnsureCredentials` must read `TG_API_ID` and `TG_API_HASH`, reject missing or malformed IDs with `NOT_AUTHED`, and preserve the Python user-facing message that points to `https://my.telegram.org/apps`; the client factory must acquire the single-process session lock before opening gotd state. `login` should perform interactive auth and return `user_id`, `username`, `display_name`, and `session_path`; `me` should support live and `--offline`, with live writing `tg_me` and offline reading it via read-only DB, returning `source`, identity fields, `cached_at`, `session_path`, and decoded `raw_json`. Tests should mirror `tgcli.commands.auth` behavior with a fake client: cached `me` not found maps to `NOT_FOUND`, live `me` updates the cache, display name follows Python `_display_title`, and all command output still flows through dispatch.

## Phase 8: Read commands

Port the read portions of `tgcli.commands.messages.py` into `internal/commands/messages_read.go` and `internal/store/messages.go`: `show`, `search`, `list-msgs`, and `get-msg`. Preserve the JSON data shapes: `show` returns `chat`, `order`, and `messages` with `date`, `is_outgoing`, `text` or null, and `media_type`; `search` rejects an empty query, escapes `%`, `_`, and `\` for SQLite `LIKE`, applies optional case-sensitive `instr`, and orders by `date DESC, message_id DESC`; `list-msgs` validates `--since` and `--until` as `YYYY-MM-DD`, applies inclusive day boundaries, and supports `--reverse`; `get-msg` returns full message fields plus decoded `raw_json` and raises `NOT_FOUND` when absent. Tests must include resolver integration from `test_resolve.py` and deleted-message behavior seen in Python phase 9 tests: by default all reads filter `(deleted = 0 OR deleted IS NULL)`, while `--include-deleted` returns tombstoned messages.

## Phase 9: Write text commands

Port the text write runners from `tgcli.commands.messages.py`: `send`, `edit-msg`, `forward`, `pin-msg`, `unpin-msg`, `react`, and `mark-read`. Every runner must follow the safety pipeline exactly: `RequireWriteAllowed`, text/emoji validation, DB connect, idempotency lookup, `RequireExplicitOrFuzzy`, resolver, payload assembly, dry-run return, rate limiter, `audit.Pre`, gotd/td call, idempotency record, and dispatch audit. Preserve Python data shapes such as `send` returning `chat`, `message_id`, `text`, `reply_to`, `topic_id`, `warnings`, and `idempotent_replay`; `_topic_reply_to` must warn `"--topic ignored because --reply-to was provided"` and use `reply_to` over topic. `forward` must use gotd raw request support when `--topic` is provided because Python uses `ForwardMessagesRequest(top_msg_id=topic)`, and `react` must reject empty emoji and map to gotd reaction requests. Key tests should port Python write tests around dry-run short-circuit, fuzzy title blocking without `--fuzzy`, rate-limit before client calls, idempotent replay, stdin `"-"` text trimming, empty text rejection, and audit payload fields.

## Phase 10: Media uploads

Build media upload/download support around gotd/td while preserving the helper behavior already visible in `tgcli.commands.messages._media_type_of`, `_download_media`, and media command path checks. `internal/media` must detect media kinds equivalent to Python strings (`photo`, `voice`, `audio`, `video_note`, `video`, `sticker`, `image`, `document`, `webpage`) and store downloads under `accounts/<name>/media/<chat_id>/<message_id>` through account-bound paths. Upload commands are Telegram-side writes, so they must use the same gates, fuzzy resolver opt-in, dry-run payloads, local rate limiter, `audit_pre`, idempotency cache, and dispatch audit as text writes. Tests should cover safe path rejection of `?` and `#`, media directory isolation by account, dry-run payloads with resolved chat and file metadata, idempotent replay, and cache updates to `tg_messages.has_media`, `media_type`, and `media_path` where the Python backfill/download behavior would populate them.

## Phase 11: Topics + Folders

Port topic and folder command behavior from the broader Python command modules into `internal/commands/topics.go` and `internal/commands/folders.go`, keeping the same safety and output surfaces used by message writes. Topic commands must resolve chats through the cache, require `--fuzzy` for title-based write selectors, support dry-run payloads, and audit gotd request names; folder commands must preserve Python invariants such as folder id `0` being reserved for deletion, folder writes using idempotency, and JSON fields like `folder_id`, `deleted`, and `idempotent_replay` where Python tests assert them. Tests should be structured like the Python phase 6/62 folder tests: fake gotd client, no network, one test per command data shape, one test for reserved id rejection as `BAD_ARGS`, and explicit verification that dispatch still wraps every success/failure in the standard envelope.

## Phase 12: Admin commands

Port administrative actions into `internal/commands/admin.go`, keeping them narrow adapters from Cobra args to gotd/td requests and shared safety. Admin commands are writes even when they mutate chat permissions or membership rather than messages, so `--read-only` must reject them, `--allow-write` must be required, non-explicit write selectors must need `--fuzzy`, destructive admin variants must require typed `--confirm <resolved-id>`, and dry-run must return before rate limiting or client construction. Tests should reference `tgcli.commands.admin` and Python admin phase tests: fake client request capture, resolver output in payload, audit `telethon_method`/gotd method names, failure mapping for premium-required actions to exit 9, and no local DB mutation when read-only is active.

## Phase 13: Destructive

Port destructive operations into `internal/commands/destructive.go`, starting with Python `delete-msg` from `tgcli.commands.messages.py` and session/block/leave behavior visible in later Python modules. `delete-msg` must resolve the chat, require typed confirm against the resolved `chat_id`, default `for_everyone` to true only when all cached message IDs are outgoing, support `--for-everyone` and `--no-for-everyone`, audit each message deletion before the gotd call, mark `tg_messages.deleted = 1` only for successful revoke-for-everyone deletes, and return `summary` plus per-message `results` without failing the whole command for individual deletion errors. `leave-chat` must reject 1-on-1 user chats, require typed confirm, and set `tg_chats.left = 1` on success. Session termination must compare `--confirm` to the resolved session hash, not a display label. Tests must port `tests/tgcli/test_phase9_typed_confirm.py` and destructive Python tests: mismatch/raw selector confirms reject as `BAD_ARGS`, tombstoned messages disappear from reads unless `--include-deleted`, left chats are marked locally, and all destructive writes still honor idempotency and the safety pipeline.

## Phase 14: Local DB ops (backfill, discover, sync-contacts)

Port local database mutation commands into `internal/commands/localdb.go`, including Python `backfill` from `tgcli.commands.messages.py` and contact/discovery behavior from adjacent command modules. These commands mutate local SQLite and sometimes download media, so they must call the read-only gate even when they do not send Telegram-side writes; `backfill` must check caps before heavy work, rejecting when `current_msg_count >= --max-messages` or DB size is at/above `--max-db-size-mb`, warning at 80% thresholds, iterating dialogs up to `--max-chats`, inserting chats and messages, optionally downloading media, throttling between chats, and returning `chats_processed`, `messages_inserted`, `media_downloaded`, `skipped`, `per_chat`, and `cap_warnings`. Tests should fake dialog/message streams and assert exact schema writes, cap rejection messages, warning strings, JSON quiet behavior, media path placement, and `TG_READONLY=1` rejection for local DB writes.

## Phase 15: Live (listen) + multi-account

Port live/listen ingestion and multi-account behavior into `internal/commands/live.go`, `internal/accounts`, and `internal/config`. Account selection must match Python precedence from `tgcli.__main__` and `tgcli.commands._common`: a `--account` flag is recognized before command execution, then `TG_ACCOUNT`, then `accounts/.current`, then `"default"`; each account resolves to isolated `telegram.sqlite`, `tg.session`, `audit.log`, and `media/` paths under `accounts/<name>/`, with strict account-name validation and a one-time migration of root-level `telegram.sqlite`, `tg.session`, `tg.session.lock`, `audit.log`, and `media/` into `accounts/default/`. Tests should port Python multi-account tests plus live-specific fake update tests: add/use/list/show/remove accounts, invalid names like `"../escape"` and `"with?mark"` rejected, current account reset when removed, live writes only the selected account DB/audit/media paths, and a process never silently writes to a different account than the one selected.

## Phase 16: Doctor + GoReleaser + cross-platform CI

Add `doctor`, `.github/workflows/ci.yml`, and `.goreleaser.yaml`. `doctor` should report credential presence, account paths, DB existence/schema version, session lock state, gotd configuration readiness, OS lock backend, and release build metadata inside the standard envelope; failures such as malformed `TG_API_ID`, missing DB for offline reads, and locked session must use the same error classification as dispatch. CI must run `go test ./...` on Linux, macOS, and Windows so `flock` and `LockFileEx` stay compiled, run `go test -race ./...` where feasible, and build `./cmd/tg`; GoReleaser must produce static-friendly multi-arch binaries using pure-Go SQLite through `modernc.org/sqlite`. Tests should assert doctor data shape, CI workflow includes all three OS families, release config names the binary `tg`, and the module path remains `github.com/b1rd33/tgctl-go`.
