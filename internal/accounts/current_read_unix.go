//go:build !windows

package accounts

import "os"

func readCurrentFile(path string) ([]byte, error) { return os.ReadFile(path) }
