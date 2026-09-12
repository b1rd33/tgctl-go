//go:build !windows

package safety

import (
	"errors"
	"os"
	"syscall"
)

func trySessionLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func sessionLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
func unlockSession(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

func sessionHasMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func openSessionLockFile(path string, readOnly bool) (*os.File, error) {
	flags := os.O_RDWR | os.O_CREATE
	if readOnly {
		flags = os.O_RDONLY
	}
	return os.OpenFile(path, flags, 0600)
}
