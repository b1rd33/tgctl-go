package commands

import (
	"bytes"
	"io"
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

func TestRootCommandGlobalFlags(t *testing.T) {
	root := NewRootCommand()

	flags := []string{"read-only", "lock-wait", "full", "account"}
	for _, name := range flags {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("missing persistent flag --%s", name)
		}
	}
}

func TestRootCommandPropagatesGlobalFlagValues(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--read-only", "--lock-wait", "1.5", "--full", "--account", "work"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	cfg := RootConfigFrom(root)
	if !cfg.ReadOnly {
		t.Fatalf("ReadOnly = false")
	}
	if cfg.LockWaitSeconds != 1.5 {
		t.Fatalf("LockWaitSeconds = %v, want 1.5", cfg.LockWaitSeconds)
	}
	if !cfg.Full {
		t.Fatalf("Full = false")
	}
	if cfg.Account != "work" {
		t.Fatalf("Account = %q, want work", cfg.Account)
	}
}

func TestVersionCommandRegistered(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"version", "--json"})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"command":"version"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"warnings":[]`)) {
		t.Fatalf("stdout = %q, want success envelope with warnings", stdout.String())
	}
}
