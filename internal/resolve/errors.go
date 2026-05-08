package resolve

import "fmt"

// NotFound matches Python tgcli.resolve.NotFound → exit 4.
type NotFound struct{ Msg string }

func (e *NotFound) Error() string { return e.Msg }
func NewNotFound(format string, a ...any) *NotFound {
	return &NotFound{Msg: fmt.Sprintf(format, a...)}
}

// Ambiguous matches Python tgcli.resolve.Ambiguous → exit 2 with candidates.
type Ambiguous struct {
	Raw        string
	Candidates [][2]any // [[id, title], ...]
}

func (e *Ambiguous) Error() string {
	return fmt.Sprintf("%q is ambiguous: %d matches", e.Raw, len(e.Candidates))
}

// DatabaseMissing → exit 4 (matches Python tgcli.db.DatabaseMissing).
type DatabaseMissing struct{ Path string }

func (e *DatabaseMissing) Error() string {
	return fmt.Sprintf("database not found: %s", e.Path)
}
