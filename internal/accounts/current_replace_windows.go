//go:build windows

package accounts

import "golang.org/x/sys/windows"

func replaceCurrentFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Windows does not provide a portable directory fsync through os.File. The
// file itself is flushed before publication and MoveFileEx uses
// MOVEFILE_WRITE_THROUGH, so there is no directory handle to sync here.
func syncCurrentDir(string) error { return nil }
