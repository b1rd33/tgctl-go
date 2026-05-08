package output

import (
	"encoding/json"
	"testing"
)

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
