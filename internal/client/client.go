// Package client defines the narrow Telegram interface used by all command
// runners. Production code wires this to gotd/td (added in a follow-up phase);
// tests use an in-memory fake.
//
// Keeping commands behind this interface decouples them from any specific
// MTProto library and lets the entire test suite run without network access.
package client

import (
	"context"
)

// User mirrors the subset of fields tgcli.commands.auth uses.
type User struct {
	ID          int64
	Username    string
	Phone       string
	FirstName   string
	LastName    string
	IsBot       bool
	DisplayName string
	RawJSON     string
}

// Client is the narrow API command runners use. Concrete implementations:
// - gotdClient (production, wired in a follow-up phase)
// - FakeClient (in tests)
type Client interface {
	GetMe(ctx context.Context) (User, error)
	Close() error
}

// DisplayName mirrors tgcli.commands.messages._display_title for User-shaped
// values, with the same fallback ordering: first+last → first → last → username
// → "user_<id>".
func DisplayName(firstName, lastName, username string, id int64) string {
	first := trim(firstName)
	last := trim(lastName)
	if first != "" && last != "" {
		return first + " " + last
	}
	if first != "" {
		return first
	}
	if last != "" {
		return last
	}
	if u := trim(username); u != "" {
		return "@" + u
	}
	return userIDFallback(id)
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func userIDFallback(id int64) string {
	return "user_" + itoa(id)
}

func itoa(i int64) string {
	// avoid strconv import to keep this file dependency-free
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
