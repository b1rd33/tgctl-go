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

func TestDestinationCommitAndAbortRejectReplacedPartFIFO(t *testing.T) {
	for _, action := range []string{"commit", "abort"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "result.bin", false)
			if err != nil {
				t.Fatal(err)
			}
			openFile := d.File
			orphan := d.PartPath + ".renamed"
			if err := os.Rename(d.PartPath, orphan); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(d.PartPath, 0o600); err != nil {
				t.Fatal(err)
			}

			var lifecycleErr error
			if action == "commit" {
				lifecycleErr = d.Commit()
			} else {
				lifecycleErr = d.Abort()
			}
			if !errors.Is(lifecycleErr, ErrUnsafeDestination) {
				t.Fatalf("%s error = %v, want ErrUnsafeDestination", action, lifecycleErr)
			}
			assertFileClosed(t, openFile)
			info, err := os.Lstat(d.PartPath)
			if err != nil {
				t.Fatalf("replacement FIFO removed: %v", err)
			}
			if info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatalf("replacement changed: mode=%v", info.Mode())
			}
			if _, err := os.Lstat(orphan); err != nil {
				t.Fatalf("renamed original part was removed: %v", err)
			}
			if _, err := os.Lstat(d.FinalPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final was published: %v", err)
			}
		})
	}
}
