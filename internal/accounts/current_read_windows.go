//go:build windows

package accounts

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func readCurrentFile(path string) ([]byte, error) {
	b, err := readCurrentFileShared(path)
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
		b, err = readCurrentFileShared(path)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, os.ErrNotExist)) {
			return b, err
		}
	}
	// MoveFileEx can leave the destination briefly absent while the adjacent
	// private selector is being published. Read that private file before
	// falling back to the default account; this keeps concurrent readers from
	// observing a false default during an otherwise valid account switch.
	if entries, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".current.tmp-*")); globErr == nil {
		for _, candidate := range entries {
			if candidateBytes, candidateErr := readCurrentFileShared(candidate); candidateErr == nil && len(candidateBytes) > 0 {
				return candidateBytes, nil
			}
		}
	}
	return b, err
}

func readCurrentFileShared(path string) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, errors.New("open selector handle")
	}
	defer f.Close()
	return io.ReadAll(f)
}
