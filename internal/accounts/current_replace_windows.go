//go:build windows

package accounts

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func replaceCurrentFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 1000; attempt++ {
		err = windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return err
}

// Windows does not provide a portable directory fsync through os.File. The
// file itself is flushed before publication and MoveFileEx uses
// MOVEFILE_WRITE_THROUGH, so there is no directory handle to sync here.
func syncCurrentDir(string) error { return nil }
