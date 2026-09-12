//go:build windows

package safety

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func trySessionLock(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
}
func sessionLockBusy(err error) bool { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }
func unlockSession(f *os.File) {
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}

func sessionHasMultipleLinks(info os.FileInfo) bool { return false }

// Share delete so account removal can delete the ownership sidecar last, after
// credentials are gone, without releasing ownership prematurely.
func openSessionLockFile(path string, readOnly bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
	creation := uint32(windows.OPEN_ALWAYS)
	if readOnly {
		access = windows.GENERIC_READ
		creation = windows.OPEN_EXISTING
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, creation, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
