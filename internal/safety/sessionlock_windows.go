//go:build windows

package safety

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

type SessionLock struct {
	mu     sync.Mutex
	handle *os.File
}

var defaultLock = &SessionLock{}

func AcquireSessionLock(sessionPath string, waitSeconds float64) error {
	return defaultLock.Acquire(sessionPath, waitSeconds)
}

func (s *SessionLock) Acquire(sessionPath string, waitSeconds float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle != nil {
		return nil
	}
	lockPath := sessionPath + ".lock"
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	deadline := time.Now().Add(time.Duration(waitSeconds * float64(time.Second)))
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		var ol windows.Overlapped
		if err := windows.LockFileEx(
			windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, &ol,
		); err != nil {
			f.Close()
			if !time.Now().Before(deadline) {
				return NewSessionLocked(fmt.Sprintf(
					"Another tg process holds the Telegram session at %s.", lockPath,
				))
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_, _ = fmt.Fprintf(f, "%d", os.Getpid())
		_ = f.Sync()
		s.handle = f
		return nil
	}
}

func (s *SessionLock) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == nil {
		return
	}
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(s.handle.Fd()), 0, 1, 0, &ol)
	_ = s.handle.Close()
	s.handle = nil
}
