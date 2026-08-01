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

	d.ops.beforeOverwrite = func() {
		if err := os.Remove(final); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(final, 0o600); err != nil {
			t.Fatal(err)
		}
	}

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

func TestDestinationNoReplaceRollsBackFIFOSwapDuringPublish(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	openFile := d.File
	orphan := d.PartPath + ".renamed"
	d.ops.beforeNoReplace = func() {
		if err := os.Rename(d.PartPath, orphan); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(d.PartPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.Commit(); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("Commit error = %v, want ErrUnsafeDestination", err)
	}
	assertFileClosed(t, openFile)
	if _, err := os.Lstat(d.FinalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final was left published after rollback: %v", err)
	}
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
	assertNoQuarantines(t, dir)
}

func TestOpenDestinationFailsFastWhenNoReplaceIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	ops := defaultDestinationOps()
	rename := ops.renameNoReplace
	ops.renameNoReplace = func(*anchoredDir, string, string) error { return unix.ENOSYS }
	d, err := openDestinationWithOps(dir, "result.bin", false, ops)
	if d != nil {
		d.ops.renameNoReplace = rename
		_ = d.Abort()
	}
	if !errors.Is(err, ErrAtomicOverwriteUnsupported) || !errors.Is(err, unix.ENOSYS) {
		t.Fatalf("OpenDestination error = %v, want unsupported and ENOSYS", err)
	}
	assertNoParts(t, dir)
	assertNoAtomicProbes(t, dir)
}

func TestOpenDestinationFailsFastWhenExchangeIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "result.bin")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := defaultDestinationOps()
	exchange := ops.exchange
	ops.exchange = func(*anchoredDir, string, string) error { return unix.EOPNOTSUPP }
	d, err := openDestinationWithOps(dir, "result.bin", true, ops)
	if d != nil {
		d.ops.exchange = exchange
		_ = d.Abort()
	}
	if !errors.Is(err, ErrAtomicOverwriteUnsupported) || !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("OpenDestination error = %v, want unsupported and EOPNOTSUPP", err)
	}
	if got, readErr := os.ReadFile(final); readErr != nil || string(got) != "old" {
		t.Fatalf("existing final changed: content=%q err=%v", got, readErr)
	}
	assertNoParts(t, dir)
	assertNoAtomicProbes(t, dir)
}

func TestAtomicRenameEINVALRequiresValidatedComponents(t *testing.T) {
	if err := normalizeAtomicRenameError(unix.EINVAL, "valid", "names"); !errors.Is(err, ErrAtomicOverwriteUnsupported) {
		t.Fatalf("valid-component EINVAL = %v, want ErrAtomicOverwriteUnsupported", err)
	}
	if err := normalizeAtomicRenameError(unix.EINVAL, "", "names"); errors.Is(err, ErrAtomicOverwriteUnsupported) || !errors.Is(err, unix.EINVAL) {
		t.Fatalf("bad-argument EINVAL misclassified: %v", err)
	}
}

func TestOpenDestinationCapabilityProbeNameExhaustionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, ".occupied-probe-name")
	if err := os.WriteFile(occupied, []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := defaultDestinationOps()
	ops.randomPrivateName = func(string) (string, error) { return filepath.Base(occupied), nil }
	if _, err := openDestinationWithOps(dir, "result.bin", false, ops); !errors.Is(err, ErrAtomicOverwriteUnsupported) {
		t.Fatalf("OpenDestination error = %v, want ErrAtomicOverwriteUnsupported", err)
	}
	if got, err := os.ReadFile(occupied); err != nil || string(got) != "attacker" {
		t.Fatalf("occupied probe candidate changed: content=%q err=%v", got, err)
	}
	assertNoParts(t, dir)
}

func TestOpenDestinationUnsupportedProbeCleanupFailureIsClassified(t *testing.T) {
	dir := t.TempDir()
	ops := defaultDestinationOps()
	injected := errors.New("injected probe unlink failure")
	ops.renameNoReplace = func(*anchoredDir, string, string) error { return unix.ENOSYS }
	ops.remove = func(*anchoredDir, string) error { return injected }
	_, err := openDestinationWithOps(dir, "result.bin", false, ops)
	if !errors.Is(err, ErrAtomicOverwriteUnsupported) || !errors.Is(err, ErrCleanupIncomplete) || !errors.Is(err, injected) {
		t.Fatalf("OpenDestination error = %v, want unsupported and cleanup errors", err)
	}
	assertNoParts(t, dir)
}

func TestOpenDestinationProbeCloseFailureIsCleanupIncomplete(t *testing.T) {
	dir := t.TempDir()
	ops := defaultDestinationOps()
	closeFile := ops.closeFile
	injected := errors.New("injected probe close failure")
	ops.closeFile = func(file *os.File) error {
		return errors.Join(closeFile(file), injected)
	}
	_, err := openDestinationWithOps(dir, "result.bin", false, ops)
	if !errors.Is(err, ErrCleanupIncomplete) || !errors.Is(err, injected) {
		t.Fatalf("OpenDestination error = %v, want cleanup and close errors", err)
	}
	assertNoAtomicProbes(t, dir)
	assertNoParts(t, dir)
}

func TestDestinationCommitClassifiesCapabilityLoss(t *testing.T) {
	for _, mode := range []string{"no-replace", "exchange"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			overwrite := mode == "exchange"
			if overwrite {
				if err := os.WriteFile(filepath.Join(dir, "result.bin"), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			d, err := OpenDestination(dir, "result.bin", overwrite)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.File.Write([]byte("download")); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "no-replace":
				d.ops.renameNoReplace = func(*anchoredDir, string, string) error { return unix.ENOSYS }
			case "exchange":
				d.ops.exchange = func(*anchoredDir, string, string) error { return unix.EOPNOTSUPP }
			}
			commitErr := d.Commit()
			if !errors.Is(commitErr, ErrAtomicOverwriteUnsupported) {
				t.Fatalf("Commit error = %v, want ErrAtomicOverwriteUnsupported", commitErr)
			}
			if mode == "no-replace" && !errors.Is(commitErr, ErrCleanupIncomplete) {
				t.Fatalf("Commit error = %v, want ErrCleanupIncomplete for unremovable part", commitErr)
			}
		})
	}
}

func assertNoAtomicProbes(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".tgctl-probe-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("capability probe entries remain: %v", matches)
	}
}
