//go:build !windows

package safety

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SessionLock holds a process-wide flock on <session>.lock until process exit.
type SessionLock struct {
	mu     sync.Mutex
	handle *os.File
}

var defaultLock = &SessionLock{}

// AcquireSessionLock takes an exclusive flock on <session>.lock. Idempotent
// within a process. waitSeconds=0 fails fast; positive value retries until
// acquired or timeout. Mirrors tgcli.client.acquire_session_lock.
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
	deadline := time.Now().Add(time.Duration(maxF(waitSeconds, 0) * float64(time.Second)))
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			if !time.Now().Before(deadline) {
				existing := readPID(lockPath)
				return NewSessionLocked(fmt.Sprintf(
					"Another tg process holds the Telegram session (PID %s). "+
						"Wait for it to finish, or kill it with: kill %s",
					existing, existing,
				))
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_, _ = fmt.Fprintf(f, "%d", os.Getpid())
		_ = f.Sync()
		_ = os.Chmod(lockPath, 0o600)
		s.handle = f
		return nil
	}
}

// Release is mainly for tests; production drops the lock at process exit.
func (s *SessionLock) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == nil {
		return
	}
	_ = syscall.Flock(int(s.handle.Fd()), syscall.LOCK_UN)
	_ = s.handle.Close()
	s.handle = nil
}

func readPID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "?"
	}
	pid := strings.TrimSpace(string(b))
	if pid == "" {
		return "?"
	}
	return pid
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
