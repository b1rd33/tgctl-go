package text

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// StripAccents returns lowercase text with combining accent marks removed.
// Mirrors tgcli.text.strip_accents.
func StripAccents(value string) string {
	if value == "" {
		return ""
	}
	decomposed := norm.NFD.String(value)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
