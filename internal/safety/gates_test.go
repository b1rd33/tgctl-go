package safety

import (
	"errors"
	"testing"
	"time"
)

func TestRequireWriteAllowedFlagPasses(t *testing.T) {
	if err := RequireWriteAllowed(Args{AllowWrite: true}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireWriteAllowedEnvOnePasses(t *testing.T) {
	t.Setenv("TG_ALLOW_WRITE", "1")
	if err := RequireWriteAllowed(Args{}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireWriteAllowedEnvYesIsRejected(t *testing.T) {
	t.Setenv("TG_ALLOW_WRITE", "yes")
	err := RequireWriteAllowed(Args{})
	var wd *WriteDisallowed
	if !errors.As(err, &wd) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadOnlyBlocksEvenWithAllowWrite(t *testing.T) {
	err := RequireWriteAllowed(Args{ReadOnly: true, AllowWrite: true})
	var wd *WriteDisallowed
	if !errors.As(err, &wd) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvReadOnlyBlocksEvenWithAllowWrite(t *testing.T) {
	t.Setenv("TG_READONLY", "1")
	t.Setenv("TG_ALLOW_WRITE", "1")
	err := RequireWriteAllowed(Args{})
	var wd *WriteDisallowed
	if !errors.As(err, &wd) {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireTypedConfirmMissing(t *testing.T) {
	err := RequireTypedConfirm(Args{}, int64(-100123), "chat_id")
	var nc *NeedsConfirm
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v", err)
	}
	if got, want := err.Error(), "destructive op requires --confirm <chat_id>. Pass --confirm -100123 to confirm."; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	var ba *BadArgs
	if errors.As(err, &ba) {
		t.Fatalf("missing confirmation classified as BadArgs: %v", err)
	}
}

func TestRequireTypedConfirmBlankIsMissing(t *testing.T) {
	err := RequireTypedConfirm(Args{Confirm: " \t\n "}, "abc123", "session_hash")
	var nc *NeedsConfirm
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want *NeedsConfirm", err)
	}
}

func TestRequireTypedConfirmMismatchRejects(t *testing.T) {
	err := RequireTypedConfirm(Args{Confirm: "Bjørn"}, int64(-100123), "chat_id")
	var ba *BadArgs
	if !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
	if got, want := err.Error(), `--confirm value "Bjørn" must equal the resolved chat_id -100123. Pass --confirm -100123 to confirm.`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	var nc *NeedsConfirm
	if errors.As(err, &nc) {
		t.Fatalf("mismatched confirmation classified as NeedsConfirm: %v", err)
	}
}

func TestRequireTypedConfirmExactMatchPasses(t *testing.T) {
	if err := RequireTypedConfirm(Args{Confirm: "  -100123 "}, int64(-100123), "chat_id"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireTypedConfirmStringSlot(t *testing.T) {
	if err := RequireTypedConfirm(Args{Confirm: "abc123"}, "abc123", "session_hash"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireExplicitOrFuzzyAcceptsInt(t *testing.T) {
	if err := RequireExplicitOrFuzzy(Args{}, "-100123"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireExplicitOrFuzzyAcceptsUsername(t *testing.T) {
	if err := RequireExplicitOrFuzzy(Args{}, "@cats"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireExplicitOrFuzzyRejectsTitleWithoutFlag(t *testing.T) {
	err := RequireExplicitOrFuzzy(Args{}, "Bjørn")
	var ba *BadArgs
	if !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireExplicitOrFuzzyAcceptsTitleWithFlag(t *testing.T) {
	if err := RequireExplicitOrFuzzy(Args{Fuzzy: true}, "Bjørn"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestRateLimiterBlocksAfterMax(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if w := rl.Check(); w > 0 {
			t.Fatalf("call %d blocked: %v", i, w)
		}
	}
	if w := rl.Check(); w <= 0 {
		t.Fatalf("4th call must block, wait = %v", w)
	}
}

func TestRateLimiterBlockedCallNotRecorded(t *testing.T) {
	rl := NewRateLimiter(1, 60*time.Second)
	if w := rl.Check(); w != 0 {
		t.Fatalf("first call blocked: %v", w)
	}
	if w := rl.Check(); w <= 0 {
		t.Fatalf("second call should block")
	}
	if w := rl.Check(); w <= 0 {
		t.Fatalf("third call should still block (blocked calls not recorded)")
	}
}

func TestSessionLockFailsFastWhenHeld(t *testing.T) {
	dir := t.TempDir()
	session := dir + "/tg.session"
	a := &SessionLock{}
	if err := a.Acquire(session, 0); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer a.Release()

	b := &SessionLock{}
	err := b.Acquire(session, 0)
	var sl *SessionLocked
	if !errors.As(err, &sl) {
		t.Fatalf("expected SessionLocked, got %v", err)
	}
}

func TestSessionLockWaitsThenFails(t *testing.T) {
	dir := t.TempDir()
	session := dir + "/tg.session"
	a := &SessionLock{}
	if err := a.Acquire(session, 0); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer a.Release()

	start := time.Now()
	b := &SessionLock{}
	err := b.Acquire(session, 0.3)
	elapsed := time.Since(start)
	var sl *SessionLocked
	if !errors.As(err, &sl) {
		t.Fatalf("err = %v", err)
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("did not wait, elapsed = %v", elapsed)
	}
}

func TestSessionLockSecondCallSameInstanceIsNoop(t *testing.T) {
	dir := t.TempDir()
	session := dir + "/tg.session"
	a := &SessionLock{}
	if err := a.Acquire(session, 0); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := a.Acquire(session, 0); err != nil {
		t.Fatalf("second: %v", err)
	}
	a.Release()
}
