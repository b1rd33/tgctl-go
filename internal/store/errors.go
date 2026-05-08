package store

import "fmt"

// DatabaseMissing → exit 4 (matches Python tgcli.db.DatabaseMissing).
type DatabaseMissing struct{ Path string }

func (e *DatabaseMissing) Error() string {
	return fmt.Sprintf("database not found: %s", e.Path)
}
