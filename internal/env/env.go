// Package env loads a minimal .env file. Shell-set variables always win over
// file values, matching tgcli.env in the Python reference.
package env

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// LoadFile reads a KEY=value file and writes any unset keys into the process
// environment. Quotes around values are stripped. Comments (#) and blank
// lines are ignored. Missing files are not an error.
func LoadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"`)
		val = strings.Trim(val, `'`)
		if key == "" {
			continue
		}
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
