//go:build !windows

package accounts

import (
	"errors"
	"os"
)

func replaceCurrentFile(from, to string) error { return os.Rename(from, to) }

func syncCurrentDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}
