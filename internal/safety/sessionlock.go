package safety

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionLock owns one canonical session until Release. Each client owns its
// own instance; another client in the same process must contend normally.
type SessionLock struct {
	mu     sync.Mutex
	handle *os.File
	path   string
}

type lockWaitKey struct{}

func WithLockWait(ctx context.Context, seconds float64) context.Context {
	return context.WithValue(ctx, lockWaitKey{}, seconds)
}
func LockWait(ctx context.Context) float64 { v, _ := ctx.Value(lockWaitKey{}).(float64); return v }
func (s *SessionLock) Acquire(path string, wait float64) error {
	return s.AcquireContext(context.Background(), path, wait, false)
}

func (s *SessionLock) AcquireContext(ctx context.Context, path string, wait float64, readOnly bool) error {
	if math.IsNaN(wait) || math.IsInf(wait, 0) || wait < 0 || wait > 3600 {
		return NewBadArgs("lock wait must be between 0 and 3600 seconds")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Resolve aliases before choosing the stable sidecar; renaming a session
	// must never replace the inode on which ownership is held.
	if real, e := filepath.EvalSymlinks(abs); e == nil {
		abs = real
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else {
		parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
		if e != nil {
			return e
		}
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	if info, err := os.Stat(abs); err == nil {
		if !info.Mode().IsRegular() || sessionHasMultipleLinks(info) {
			return NewBadArgs("session must be a regular file without hard-link aliases")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(abs + ".lock"); err == nil {
		if !info.Mode().IsRegular() || sessionHasMultipleLinks(info) {
			return NewSessionLocked("session ownership file must be a regular file without aliases")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle != nil {
		if s.path == abs {
			return nil
		}
		return NewSessionLocked("lock instance already owns another session")
	}
	f, err := openSessionLockFile(abs+".lock", readOnly)
	if err != nil {
		if readOnly && errors.Is(err, os.ErrNotExist) {
			return NewSessionLocked("session ownership file is missing; initialize it with a writable account operation before using --read-only")
		}
		return err
	}
	opened, statErr := f.Stat()
	named, nameErr := os.Lstat(abs + ".lock")
	if statErr != nil || nameErr != nil || !os.SameFile(opened, named) || !named.Mode().IsRegular() || sessionHasMultipleLinks(opened) {
		f.Close()
		return NewSessionLocked("session ownership file changed while opening")
	}
	acquired := false
	defer func() {
		if !acquired {
			_ = f.Close()
		}
	}()
	deadline := time.Now().Add(time.Duration(wait * float64(time.Second)))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = trySessionLock(f)
		if err == nil {
			s.handle = f
			s.path = abs
			acquired = true
			return nil
		}
		if !sessionLockBusy(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return NewSessionLocked("another client owns this Telegram session; wait for it to close or use --lock-wait")
		}
		timer := time.NewTimer(min(remaining, 50*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (s *SessionLock) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle != nil {
		unlockSession(s.handle)
		_ = s.handle.Close()
		s.handle = nil
		s.path = ""
	}
}
