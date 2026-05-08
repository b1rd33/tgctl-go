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
