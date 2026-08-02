//go:build windows

package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func readCurrentFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, os.ErrNotExist)) {
		return b, err
	}
	// A concurrent MoveFileEx replacement can briefly deny or hide the target.
	// Retry only when the selector directory itself exists, so a normal first
	// run still falls back to the default immediately.
	if _, statErr := os.Stat(filepath.Dir(path)); statErr != nil {
		return b, err
	}
	for range 250 {
		time.Sleep(time.Millisecond)
		b, err = os.ReadFile(path)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, os.ErrNotExist)) {
			return b, err
		}
	}
	return b, err
}
