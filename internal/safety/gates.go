package safety

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Args is a minimal interface implemented by command-arg structs so the gates
// stay decoupled from any specific argparse/cobra layer.
type Args struct {
	ReadOnly   bool
	AllowWrite bool
	Confirm    string // raw value of --confirm
	Fuzzy      bool
}

// RequireWritesNotReadOnly rejects writes when --read-only or TG_READONLY=1.
func RequireWritesNotReadOnly(args Args) error {
	if args.ReadOnly || os.Getenv("TG_READONLY") == "1" {
		return NewWriteDisallowed("Writes blocked: read-only mode active (--read-only / TG_READONLY=1)")
	}
	return nil
}

// RequireWriteAllowed enforces --read-only and then --allow-write / TG_ALLOW_WRITE=1.
// Note: TG_ALLOW_WRITE must equal exactly "1" — "yes" or "true" do not count.
func RequireWriteAllowed(args Args) error {
	if err := RequireWritesNotReadOnly(args); err != nil {
		return err
	}
	if args.AllowWrite {
		return nil
	}
	if os.Getenv("TG_ALLOW_WRITE") == "1" {
		return nil
	}
	return NewWriteDisallowed("Write operations require --allow-write or TG_ALLOW_WRITE=1")
}

// RequireTypedConfirm verifies --confirm exactly matches the resolved id (string-trimmed).
func RequireTypedConfirm(args Args, expected any, slot string) error {
	want := strings.TrimSpace(fmt.Sprintf("%v", expected))
	if strings.TrimSpace(args.Confirm) == "" {
		return &NeedsConfirm{Msg: fmt.Sprintf(
			"destructive op requires --confirm <%s>. Pass --confirm %s to confirm.", slot, want,
		)}
	}
	if got := strings.TrimSpace(args.Confirm); got != want {
		return NewBadArgs(
			"--confirm value %q must equal the resolved %s %s. Pass --confirm %s to confirm.",
			args.Confirm, slot, want, want,
		)
	}
	return nil
}

var explicitIntRE = regexp.MustCompile(`^[+-]?\d+$`)

func isExplicitChatSelector(raw string) bool {
	value := strings.TrimSpace(raw)
	if explicitIntRE.MatchString(value) {
		return true
	}
	if strings.HasPrefix(value, "@") && len(value) > 1 {
		return true
	}
	return false
}

// RequireExplicitOrFuzzy requires --fuzzy before a write may use title resolution.
func RequireExplicitOrFuzzy(args Args, raw string) error {
	if isExplicitChatSelector(raw) {
		return nil
	}
	if args.Fuzzy {
		return nil
	}
	return NewBadArgs(
		"%q looks like a fuzzy title match; pass --fuzzy to allow it for write operations, or use the chat_id directly.",
		raw,
	)
}
