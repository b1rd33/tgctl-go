package commands

import (
	"bytes"
	"testing"
)

func TestNewRootCommandHasNameAndNoCommandReturnsOK(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root.Use != "tg" {
		t.Fatalf("Use = %q, want tg", root.Use)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("Telegram agent CLI")) {
		t.Fatalf("stderr help = %q, want description", stderr.String())
	}
}
