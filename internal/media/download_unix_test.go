//go:build darwin || linux

package media

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDestinationOverwriteRollsBackFIFOSwapBeforePublish(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "result.bin")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDestination(dir, "result.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}

	originalHook := beforeOverwritePublish
	beforeOverwritePublish = func() {
		if err := os.Remove(final); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(final, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeOverwritePublish = originalHook })

	if err := d.Commit(); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("Commit error = %v, want ErrUnsafeDestination", err)
	}
	info, err := os.Lstat(final)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("unsafe FIFO was not restored: mode=%v", info.Mode())
	}
	assertNoParts(t, dir)
}
