package safety

import "fmt"

// BadArgs corresponds to Python BadArgs / argparse misuse → exit 2.
type BadArgs struct{ Msg string }

func (e *BadArgs) Error() string { return e.Msg }
func NewBadArgs(format string, a ...any) *BadArgs {
	return &BadArgs{Msg: fmt.Sprintf(format, a...)}
}

// WriteDisallowed → exit 6.
type WriteDisallowed struct{ Msg string }

func (e *WriteDisallowed) Error() string { return e.Msg }
func NewWriteDisallowed(msg string) *WriteDisallowed {
	return &WriteDisallowed{Msg: msg}
}

// NeedsConfirm → exit 7.
type NeedsConfirm struct{ Msg string }

func (e *NeedsConfirm) Error() string { return e.Msg }
func NewNeedsConfirm(action string) *NeedsConfirm {
	return &NeedsConfirm{Msg: fmt.Sprintf("Destructive op %q requires --confirm", action)}
}

// LocalRateLimited → exit 8.
type LocalRateLimited struct {
	Msg               string
	RetryAfterSeconds float64
}

func (e *LocalRateLimited) Error() string { return e.Msg }

// MissingCredentials → exit 3.
type MissingCredentials struct{ Msg string }

func (e *MissingCredentials) Error() string { return e.Msg }
func NewMissingCredentials(msg string) *MissingCredentials {
	return &MissingCredentials{Msg: msg}
}

// SessionLocked → exit 1 (matches Python).
type SessionLocked struct{ Msg string }

func (e *SessionLocked) Error() string { return e.Msg }
func NewSessionLocked(msg string) *SessionLocked {
	return &SessionLocked{Msg: msg}
}

// FloodWait → exit 5.
type FloodWait struct {
	Seconds int
}

func (e *FloodWait) Error() string {
	return fmt.Sprintf("Telegram FloodWait: wait %ds", e.Seconds)
}

// PremiumRequired → exit 9.
type PremiumRequired struct{}

func (e *PremiumRequired) Error() string {
	return "Telegram Premium account required for this action"
}

// PermissionDenied represents a Telegram-side chat/member permission refusal.
type PermissionDenied struct {
	RPCType string
}

func (e *PermissionDenied) Error() string {
	if e == nil || e.RPCType == "" {
		return "Telegram denied this operation due to chat permissions"
	}
	return "Telegram denied this operation due to chat permissions (" + e.RPCType + ")"
}

// ArchiveVerification is returned by a local manifest verification when the
// filesystem does not match the recorded archive. Kind is one of missing,
// changed, or extra and Results contains bounded machine-readable details.
type ArchiveVerification struct {
	Kind    string
	Message string
	Results map[string]any
}

func (e *ArchiveVerification) Error() string {
	if e == nil || e.Message == "" {
		return "archive verification failed"
	}
	return e.Message
}

// CommittedWrite marks an error discovered after a write was durably
// committed. Callers must not retry it as if the operation were rolled back.
type CommittedWrite struct {
	Msg    string
	Err    error
	extras map[string]any
}

func (e *CommittedWrite) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return fmt.Sprintf("%s: %v", e.Msg, e.Err)
}

func (e *CommittedWrite) Unwrap() error { return e.Err }

func NewCommittedWrite(msg string, err error) *CommittedWrite {
	return &CommittedWrite{Msg: msg, Err: err}
}

// NewCommittedWriteWithExtras attaches recovery metadata for dispatch
// classification. The map is copied; dispatch also filters reserved keys.
func NewCommittedWriteWithExtras(msg string, err error, extras map[string]any) *CommittedWrite {
	copyExtras := make(map[string]any, len(extras))
	for key, value := range extras {
		copyExtras[key] = value
	}
	return &CommittedWrite{Msg: msg, Err: err, extras: copyExtras}
}

func (e *CommittedWrite) ClassificationExtras() map[string]any {
	if e == nil {
		return nil
	}
	out := make(map[string]any, len(e.extras))
	for key, value := range e.extras {
		out[key] = value
	}
	return out
}
