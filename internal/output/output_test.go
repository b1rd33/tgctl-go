package output

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
)

func TestExitCodeValuesAreStable(t *testing.T) {
	tests := map[ExitCode]int{
		OK:               0,
		Generic:          1,
		BadArgs:          2,
		NotAuthed:        3,
		NotFound:         4,
		FloodWait:        5,
		WriteDisallowed:  6,
		NeedsConfirm:     7,
		LocalRateLimit:   8,
		PremiumRequired:  9,
		PermissionDenied: 10,
		ArchiveMissing:   11,
		ArchiveChanged:   12,
		ArchiveExtra:     13,
	}
	for code, want := range tests {
		if int(code) != want {
			t.Fatalf("%s = %d, want %d", code.String(), code, want)
		}
	}
}

func TestExitCodeStringNamesMatchPythonEnum(t *testing.T) {
	tests := map[ExitCode]string{
		OK:               "OK",
		Generic:          "GENERIC",
		BadArgs:          "BAD_ARGS",
		NotAuthed:        "NOT_AUTHED",
		NotFound:         "NOT_FOUND",
		FloodWait:        "FLOOD_WAIT",
		WriteDisallowed:  "WRITE_DISALLOWED",
		NeedsConfirm:     "NEEDS_CONFIRM",
		LocalRateLimit:   "LOCAL_RATE_LIMIT",
		PremiumRequired:  "PREMIUM_REQUIRED",
		PermissionDenied: "PERMISSION_DENIED",
		ArchiveMissing:   "ARCHIVE_MISSING",
		ArchiveChanged:   "ARCHIVE_CHANGED",
		ArchiveExtra:     "ARCHIVE_EXTRA",
	}
	for code, want := range tests {
		if got := code.String(); got != want {
			t.Fatalf("String(%d) = %q, want %q", code, got, want)
		}
	}
}

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
