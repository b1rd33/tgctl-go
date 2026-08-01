package media

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeDownloadName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"../secret", "secret"},
		{"/absolute", "absolute"},
		{`C:\\Users\\alice\\photo.jpg`, "photo.jpg"},
		{"a/b", "b"},
		{"bad\x00\nname.txt", "bad__name.txt"},
		{`report<>:"|?*.txt`, "report_______.txt"},
		{"NUL.txt", "_NUL.txt"},
		{"com1.LOG", "_com1.LOG"},
		{"Lpt9", "_Lpt9"},
		{"", "media.bin"},
		{".", "media.bin"},
		{"..", "media.bin"},
		{" . .. ", "media.bin"},
		{"  .résumé 📎.pdf. ", "résumé 📎.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeDownloadName(tt.name); got != tt.want {
				t.Fatalf("SanitizeDownloadName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestSanitizeDownloadNamePortableCollisionsRemainIdentical(t *testing.T) {
	left := SanitizeDownloadName("a:b.txt")
	right := SanitizeDownloadName("a?b.txt")
	if left != "a_b.txt" || right != left {
		t.Fatalf("portable collision mismatch: left=%q right=%q", left, right)
	}
}

func TestSanitizeDownloadNameLongUnicodeReservedStemPreservesExtension(t *testing.T) {
	name := "NUL." + strings.Repeat("界", 100) + ".txt"
	got := SanitizeDownloadName(name)
	if len(got) > maxDownloadNameSize || !utf8.ValidString(got) {
		t.Fatalf("unsafe result: bytes=%d valid=%t name=%q", len(got), utf8.ValidString(got), got)
	}
	if !strings.HasSuffix(got, ".txt") {
		t.Fatalf("useful extension lost: %q", got)
	}
	if strings.EqualFold(strings.TrimSuffix(got, filepath.Ext(got)), "NUL") {
		t.Fatalf("reserved device stem remains: %q", got)
	}
}

func TestSanitizeDownloadNameReservedBoundaryNeverExceedsLimit(t *testing.T) {
	inputs := []string{
		"CON." + strings.Repeat("x", 176),
		"COM1." + strings.Repeat("x", 175),
		"NUL." + strings.Repeat("界", 58) + "ab",
	}
	for _, input := range inputs {
		if len(input) != maxDownloadNameSize {
			t.Fatalf("test input length = %d, want %d", len(input), maxDownloadNameSize)
		}
		got := SanitizeDownloadName(input)
		if len(got) > maxDownloadNameSize {
			t.Fatalf("SanitizeDownloadName produced %d bytes: %q", len(got), got)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("SanitizeDownloadName produced invalid UTF-8: %q", got)
		}
		if reservedDeviceStemForTest(got) {
			t.Fatalf("SanitizeDownloadName left reserved device stem: %q", got)
		}
	}
}

func reservedDeviceStemForTest(name string) bool {
	stem := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" {
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}

func TestOpenDestinationPortableSanitizerCollisionUsesNoReplace(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "a:b.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDestination(dir, "a?b.txt", false); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("colliding OpenDestination error = %v, want ErrDestinationExists", err)
	}
}

func TestSanitizeDownloadNameAlwaysReturnsSafeComponent(t *testing.T) {
	inputs := []string{
		strings.Repeat("界", 100) + ".telegram-download",
		strings.Repeat("a", 250) + ".pdf",
		"\xffbad.txt",
		`..\\..\\secret`,
	}
	for _, input := range inputs {
		got := SanitizeDownloadName(input)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("unsafe empty/dot result %q", got)
		}
		if len(got) > 180 {
			t.Fatalf("result is %d bytes, want <= 180: %q", len(got), got)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("result is invalid UTF-8: %q", got)
		}
		if filepath.Base(got) != got || strings.ContainsAny(got, `/\\`) {
			t.Fatalf("result is not one path component: %q", got)
		}
		if strings.Trim(got, ". ") != got {
			t.Fatalf("result has dangerous edge characters: %q", got)
		}
	}
	if got := SanitizeDownloadName(strings.Repeat("a", 250) + ".pdf"); !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("long name lost useful extension: %q", got)
	}
}

func TestOpenDestinationCreatesPrivateFilesInsideDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "new", "downloads")
	d, err := OpenDestination(dir, "../photo.jpg", false)
	if err != nil {
		t.Fatalf("OpenDestination: %v", err)
	}
	t.Cleanup(func() { _ = d.Abort() })

	if d.FinalPath != filepath.Join(dir, "photo.jpg") {
		t.Fatalf("FinalPath = %q", d.FinalPath)
	}
	partDir := filepath.Dir(d.PartPath)
	if partDir != dir {
		t.Fatalf("part escaped directory: %q", d.PartPath)
	}
	if !strings.HasSuffix(d.PartPath, ".part") || d.PartPath == d.FinalPath {
		t.Fatalf("unexpected part path: %q", d.PartPath)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, d.PartPath, 0o600)
}

func TestOpenDestinationRejectsExistingFinalWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDestination(dir, "report.pdf", false); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("OpenDestination error = %v, want ErrDestinationExists", err)
	}
	assertNoParts(t, dir)
}

func TestOpenDestinationFailurePreservesDirectoryCloseError(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "result.bin")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := defaultDestinationOps()
	closeDir := ops.closeDir
	injected := errors.New("injected open directory close failure")
	ops.closeDir = func(dir *anchoredDir) error {
		return errors.Join(closeDir(dir), injected)
	}
	_, err := openDestinationWithOps(dir, "result.bin", false, ops)
	if !errors.Is(err, ErrDestinationExists) || !errors.Is(err, ErrCleanupIncomplete) || !errors.Is(err, injected) {
		t.Fatalf("OpenDestination error = %v, want exists, cleanup, and close errors", err)
	}
	if got, readErr := os.ReadFile(final); readErr != nil || string(got) != "old" {
		t.Fatalf("existing final changed: content=%q err=%v", got, readErr)
	}
}

func TestOpenDestinationConcurrentCallsUseDistinctParts(t *testing.T) {
	dir := t.TempDir()
	d1, err := OpenDestination(dir, "same.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d1.Abort() })
	d2, err := OpenDestination(dir, "same.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d2.Abort() })
	if d1.PartPath == d2.PartPath {
		t.Fatalf("concurrent destinations shared part %q", d1.PartPath)
	}
}

func TestOpenDestinationDoesNotFollowCollidingPartSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	collision := ".file.bin.collision.part"
	if err := os.Symlink(victim, filepath.Join(dir, collision)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	ops := defaultDestinationOps()
	candidates := []string{collision, ".file.bin.safe.part"}
	ops.generatePartName = func(string) (string, error) {
		candidate := candidates[0]
		candidates = candidates[1:]
		return candidate, nil
	}

	d, err := openDestinationWithOps(dir, "file.bin", false, ops)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(d.PartPath); got != ".file.bin.safe.part" {
		t.Fatalf("part name = %q, want safe second candidate", got)
	}
	if _, err := d.File.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := d.Abort(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "safe" {
		t.Fatalf("part symlink victim changed to %q", got)
	}
	if info, err := os.Lstat(filepath.Join(dir, collision)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("colliding symlink changed: info=%v err=%v", info, err)
	}
}

func TestOpenDestinationPartNameExhaustionPreservesCollisions(t *testing.T) {
	dir := t.TempDir()
	collisionName := ".result.bin.collision.part"
	collisionPath := filepath.Join(dir, collisionName)
	if err := os.WriteFile(collisionPath, []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := defaultDestinationOps()
	ops.generatePartName = func(string) (string, error) { return collisionName, nil }
	if _, err := openDestinationWithOps(dir, "result.bin", false, ops); !errors.Is(err, os.ErrExist) {
		t.Fatalf("OpenDestination error = %v, want os.ErrExist after 100 collisions", err)
	}
	if got, err := os.ReadFile(collisionPath); err != nil || string(got) != "attacker" {
		t.Fatalf("colliding part changed: content=%q err=%v", got, err)
	}
	assertNoQuarantines(t, dir)
}

func TestDestinationCommitPublishesAtomicallyAndSetsPrivateMode(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("complete")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.FinalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final visible before commit: %v", err)
	}
	if err := d.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := os.ReadFile(d.FinalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete" {
		t.Fatalf("final content = %q", got)
	}
	assertMode(t, d.FinalPath, 0o600)
	if _, err := os.Stat(d.PartPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part remains after commit: %v", err)
	}
	if err := d.Commit(); !errors.Is(err, ErrDestinationCommitted) {
		t.Fatalf("second Commit error = %v", err)
	}
	if err := d.Abort(); !errors.Is(err, ErrDestinationCommitted) {
		t.Fatalf("Abort after Commit error = %v", err)
	}
	if got, _ := os.ReadFile(d.FinalPath); string(got) != "complete" {
		t.Fatalf("repeated lifecycle call damaged final: %q", got)
	}
}

func TestDestinationAbsentPublishNeverExposesIncompleteFinal(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("overwrite=%t", overwrite), func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "result.bin", overwrite)
			if err != nil {
				t.Fatal(err)
			}
			payload := strings.Repeat("complete-download-", 4096)
			if _, err := d.File.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}

			entered := make(chan struct{})
			release := make(chan struct{})
			d.ops.beforeAbsentPublish = func() {
				close(entered)
				<-release
			}

			commitResult := make(chan error, 1)
			go func() { commitResult <- d.Commit() }()
			<-entered

			var observationErr error
			for range 100 {
				got, err := os.ReadFile(d.FinalPath)
				switch {
				case errors.Is(err, os.ErrNotExist):
				case err != nil:
					observationErr = fmt.Errorf("observe final: %w", err)
				case string(got) != payload:
					observationErr = fmt.Errorf("observed incomplete final: got %d bytes, want %d", len(got), len(payload))
				}
				if observationErr != nil {
					break
				}
			}
			close(release)
			if err := <-commitResult; err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if observationErr != nil {
				t.Fatal(observationErr)
			}
			if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != payload {
				t.Fatalf("final content incomplete after commit: bytes=%d err=%v", len(got), err)
			}
		})
	}
}

func TestDestinationCommitWithoutOverwriteDoesNotClobberRacedFinal(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "race.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.FinalPath, []byte("winner"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Commit error = %v, want ErrDestinationExists", err)
	}
	got, err := os.ReadFile(d.FinalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "winner" {
		t.Fatalf("raced final clobbered: %q", got)
	}
	assertNoParts(t, dir)
	if err := d.Abort(); err != nil {
		t.Fatalf("Abort after failed Commit should be idempotent: %v", err)
	}
}

func TestDestinationAbsentPublishDoesNotClobberTargetAppearingAfterCapture(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("overwrite=%t", overwrite), func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "race.bin", overwrite)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.File.Write([]byte("download")); err != nil {
				t.Fatal(err)
			}

			d.ops.beforeAbsentPublish = func() {
				if err := os.WriteFile(d.FinalPath, []byte("winner"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			commitErr := d.Commit()
			wantErr := ErrDestinationExists
			if overwrite {
				wantErr = ErrDestinationChanged
			}
			if !errors.Is(commitErr, wantErr) {
				t.Fatalf("Commit error = %v, want %v", commitErr, wantErr)
			}
			if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "winner" {
				t.Fatalf("raced final changed: content=%q err=%v", got, err)
			}
			assertNoParts(t, dir)
			assertNoQuarantines(t, dir)
		})
	}
}

func TestDestinationCommitStaysInOpenedDirectoryAfterPathReplacement(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "downloads")
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(root, "moved-downloads")
	victim := filepath.Join(root, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := d.Commit(); err != nil {
		t.Fatalf("Commit after directory replacement: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, "result.bin")); err != nil || string(got) != "download" {
		t.Fatalf("opened directory final = %q, err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(victim, "result.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement directory was modified: %v", err)
	}
	assertNoParts(t, moved)
}

func TestDestinationAbortStaysInOpenedDirectoryAfterPathReplacement(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "downloads")
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	originalPartName := filepath.Base(d.PartPath)

	moved := filepath.Join(root, "moved-downloads")
	victim := filepath.Join(root, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, originalPartName), []byte("victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := d.Abort(); err != nil {
		t.Fatalf("Abort after directory replacement: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(victim, originalPartName)); err != nil || string(got) != "victim" {
		t.Fatalf("replacement directory entry changed: content=%q err=%v", got, err)
	}
	assertNoParts(t, moved)
}

func TestDestinationOverwriteReplacesOnlyRegularFiles(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "result.bin")
		if err := os.WriteFile(final, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := OpenDestination(dir, "result.bin", true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.File.Write([]byte("new")); err != nil {
			t.Fatal(err)
		}
		if err := d.Commit(); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(final); string(got) != "new" {
			t.Fatalf("content = %q", got)
		}
		assertMode(t, final, 0o600)
	})

	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			final := filepath.Join(dir, "result.bin")
			switch kind {
			case "directory":
				if err := os.Mkdir(final, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				victim := filepath.Join(dir, "victim")
				if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(victim, final); err != nil {
					t.Skipf("symlink unsupported: %v", err)
				}
			}
			if _, err := OpenDestination(dir, "result.bin", true); !errors.Is(err, ErrUnsafeDestination) {
				t.Fatalf("OpenDestination error = %v, want ErrUnsafeDestination", err)
			}
			assertNoParts(t, dir)
		})
	}
}

func TestDestinationOverwriteRollsBackSymlinkSwapBeforePublish(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "result.bin")
	victim := filepath.Join(dir, "victim.bin")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
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
		if err := os.Symlink(victim, final); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.Commit(); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("Commit error = %v, want ErrUnsafeDestination", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "safe" {
		t.Fatalf("symlink referent changed: content=%q err=%v", got, err)
	}
	if target, err := os.Readlink(final); err != nil || target != victim {
		t.Fatalf("unsafe target was not restored: target=%q err=%v", target, err)
	}
	assertNoParts(t, dir)
}

func TestDestinationAbortRemovesPartAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := d.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if err := d.Abort(); err != nil {
		t.Fatalf("second Abort: %v", err)
	}
	if err := d.Commit(); !errors.Is(err, ErrDestinationAborted) {
		t.Fatalf("Commit after Abort error = %v", err)
	}
	assertNoParts(t, dir)
}

func TestDestinationAbortUnlinkFailureRemainsStable(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	openFile := d.File
	injected := errors.New("injected unlink failure")
	d.ops.remove = func(*anchoredDir, string) error { return injected }

	first := d.Abort()
	if !errors.Is(first, ErrCleanupIncomplete) || !errors.Is(first, injected) {
		t.Fatalf("first Abort error = %v, want cleanup and injected errors", first)
	}
	assertFileClosed(t, openFile)
	second := d.Abort()
	if !errors.Is(second, ErrCleanupIncomplete) || !errors.Is(second, injected) {
		t.Fatalf("second Abort error = %v, want stable cleanup error", second)
	}
	if second.Error() != first.Error() {
		t.Fatalf("cleanup error changed: first=%q second=%q", first, second)
	}
	if commitErr := d.Commit(); !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, injected) {
		t.Fatalf("Commit after failed cleanup = %v, want stable cleanup error", commitErr)
	}
	assertNoParts(t, dir)
}

func TestDestinationAbortQuarantineRenameFailureRemainsStable(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	openFile := d.File
	injected := errors.New("injected quarantine rename failure")
	d.ops.renameNoReplace = func(*anchoredDir, string, string) error { return injected }

	first := d.Abort()
	if !errors.Is(first, ErrCleanupIncomplete) || !errors.Is(first, injected) {
		t.Fatalf("first Abort error = %v, want cleanup and rename errors", first)
	}
	assertFileClosed(t, openFile)
	if _, err := os.Lstat(d.PartPath); err != nil {
		t.Fatalf("uncleaned part state was not preserved: %v", err)
	}
	if second := d.Abort(); second.Error() != first.Error() {
		t.Fatalf("cleanup error changed: first=%q second=%q", first, second)
	}
}

func TestDestinationAbortQuarantineNameExhaustionPreservesEntries(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(dir, ".occupied-private-name")
	if err := os.WriteFile(occupied, []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.ops.randomPrivateName = func(string) (string, error) { return filepath.Base(occupied), nil }
	abortErr := d.Abort()
	if !errors.Is(abortErr, ErrCleanupIncomplete) {
		t.Fatalf("Abort error = %v, want ErrCleanupIncomplete", abortErr)
	}
	if got, err := os.ReadFile(occupied); err != nil || string(got) != "attacker" {
		t.Fatalf("occupied private candidate changed: content=%q err=%v", got, err)
	}
	if _, err := os.Lstat(d.PartPath); err != nil {
		t.Fatalf("uncleaned original part state was not preserved: %v", err)
	}
	if second := d.Abort(); second.Error() != abortErr.Error() {
		t.Fatalf("terminal exhaustion error changed: first=%q second=%q", abortErr, second)
	}
}

func TestDestinationAbortCloseFailuresRemainStableWithoutArtifacts(t *testing.T) {
	for _, target := range []string{"file", "directory"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "cancel.bin", false)
			if err != nil {
				t.Fatal(err)
			}
			openFile := d.File
			injected := errors.New("injected " + target + " close failure")
			switch target {
			case "file":
				closeFile := d.ops.closeFile
				d.ops.closeFile = func(file *os.File) error {
					return errors.Join(closeFile(file), injected)
				}
			case "directory":
				closeDir := d.ops.closeDir
				d.ops.closeDir = func(dir *anchoredDir) error {
					return errors.Join(closeDir(dir), injected)
				}
			}

			first := d.Abort()
			if !errors.Is(first, ErrCleanupIncomplete) || !errors.Is(first, injected) {
				t.Fatalf("Abort error = %v, want cleanup and close errors", first)
			}
			assertFileClosed(t, openFile)
			assertNoParts(t, dir)
			assertNoQuarantines(t, dir)
			if second := d.Abort(); second.Error() != first.Error() {
				t.Fatalf("cleanup error changed: first=%q second=%q", first, second)
			}
		})
	}
}

func TestDestinationCommitFailureAndCleanupFailureRemainStableOnAbort(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "broken.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected file sync failure")
	removeErr := errors.New("injected cleanup unlink failure")
	d.ops.syncFile = func(*os.File) error { return syncErr }
	d.ops.remove = func(*anchoredDir, string) error { return removeErr }

	commitErr := d.Commit()
	if !errors.Is(commitErr, syncErr) || !errors.Is(commitErr, removeErr) || !errors.Is(commitErr, ErrCleanupIncomplete) {
		t.Fatalf("Commit error = %v, want sync, cleanup, and unlink errors", commitErr)
	}
	abortErr := d.Abort()
	if !errors.Is(abortErr, syncErr) || !errors.Is(abortErr, removeErr) || !errors.Is(abortErr, ErrCleanupIncomplete) {
		t.Fatalf("Abort error = %v, want stable joined Commit error", abortErr)
	}
	if abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationConcurrentCommitAndAbortAreSerialized(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	syncFile := d.ops.syncFile
	d.ops.syncFile = func(file *os.File) error {
		close(entered)
		<-release
		return syncFile(file)
	}
	commitResult := make(chan error, 1)
	abortResult := make(chan error, 1)
	go func() { commitResult <- d.Commit() }()
	<-entered
	go func() { abortResult <- d.Abort() }()
	close(release)
	if err := <-commitResult; err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := <-abortResult; !errors.Is(err, ErrDestinationCommitted) {
		t.Fatalf("concurrent Abort error = %v, want ErrDestinationCommitted", err)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "download" {
		t.Fatalf("final content=%q err=%v", got, err)
	}
}

func TestDestinationDirectorySyncFailureReportsCommittedOutcome(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected directory sync failure")
	d.ops.syncDir = func(*anchoredDir) error { return injected }
	if err := d.Commit(); !errors.Is(err, injected) {
		t.Fatalf("Commit error = %v, want directory sync error", err)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "download" {
		t.Fatalf("published final content=%q err=%v", got, err)
	}
	if err := d.Abort(); !errors.Is(err, ErrDestinationCommitted) {
		t.Fatalf("Abort after published sync failure = %v, want ErrDestinationCommitted", err)
	}
}

func TestDestinationCommitFileCloseFailureIsStableCleanupIncomplete(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	closeFile := d.ops.closeFile
	injected := errors.New("injected commit file close failure")
	d.ops.closeFile = func(file *os.File) error {
		return errors.Join(closeFile(file), injected)
	}
	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, injected) {
		t.Fatalf("Commit error = %v, want cleanup and close errors", commitErr)
	}
	assertNoParts(t, dir)
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal close error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationCommittedDirectoryCloseFailureIsStableCleanupIncomplete(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	closeDir := d.ops.closeDir
	injected := errors.New("injected committed directory close failure")
	d.ops.closeDir = func(dir *anchoredDir) error {
		return errors.Join(closeDir(dir), injected)
	}
	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, injected) {
		t.Fatalf("Commit error = %v, want cleanup and close errors", commitErr)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "download" {
		t.Fatalf("published final content=%q err=%v", got, err)
	}
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal directory close error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationAbortCleansPartAfterFileIsAlreadyClosed(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "closed.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.File.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Abort(); err != nil {
		t.Fatalf("Abort after closed file: %v", err)
	}
	assertNoParts(t, dir)
}

func TestDestinationCommitFailureRemovesPart(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "broken.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.File.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Commit(); err == nil {
		t.Fatal("Commit returned nil error for closed file")
	}
	assertNoParts(t, dir)
}

func TestDestinationMutatedFieldsCannotDamageUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "expected.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	originalPart := d.PartPath
	victim := filepath.Join(dir, "victim.bin")
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.PartPath = victim
	d.FinalPath = filepath.Join(dir, "other.bin")
	if err := d.Commit(); !errors.Is(err, ErrInvalidDestination) {
		t.Fatalf("Commit error = %v, want ErrInvalidDestination", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "safe" {
		t.Fatalf("unrelated file changed: content=%q err=%v", got, err)
	}
	if _, err := os.Stat(originalPart); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original part remains: %v", err)
	}
}

func TestDestinationCommitRejectsReplacedOrMissingPartEntry(t *testing.T) {
	for _, kind := range []string{"symlink", "regular", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "result.bin", false)
			if err != nil {
				t.Fatal(err)
			}
			openFile := d.File
			if _, err := openFile.Write([]byte("download")); err != nil {
				t.Fatal(err)
			}
			orphan := replacePartEntry(t, d, kind)

			commitErr := d.Commit()
			wantErr := ErrDestinationChanged
			if kind == "symlink" {
				wantErr = ErrUnsafeDestination
			}
			if !errors.Is(commitErr, wantErr) {
				t.Fatalf("Commit error = %v, want %v", commitErr, wantErr)
			}
			assertFileClosed(t, openFile)
			if _, err := os.Lstat(d.FinalPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final was published from changed part: %v", err)
			}
			assertReplacementUnchanged(t, d.PartPath, kind)
			if got, err := os.ReadFile(orphan); err != nil || string(got) != "download" {
				t.Fatalf("renamed original part changed: content=%q err=%v", got, err)
			}
		})
	}
}

func TestDestinationAbortRejectsReplacedOrMissingPartEntry(t *testing.T) {
	for _, kind := range []string{"symlink", "regular", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "cancel.bin", false)
			if err != nil {
				t.Fatal(err)
			}
			openFile := d.File
			orphan := replacePartEntry(t, d, kind)

			abortErr := d.Abort()
			wantErr := ErrDestinationChanged
			if kind == "symlink" {
				wantErr = ErrUnsafeDestination
			}
			if !errors.Is(abortErr, wantErr) {
				t.Fatalf("Abort error = %v, want %v", abortErr, wantErr)
			}
			assertFileClosed(t, openFile)
			assertReplacementUnchanged(t, d.PartPath, kind)
			if _, err := os.Lstat(orphan); err != nil {
				t.Fatalf("renamed original part was removed: %v", err)
			}
		})
	}
}

func TestDestinationOverwriteRejectsReplacedPartAndRestoresFinal(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "result.bin")
	if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := OpenDestination(dir, "result.bin", true)
	if err != nil {
		t.Fatal(err)
	}
	openFile := d.File
	if _, err := openFile.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	orphan := replacePartEntry(t, d, "symlink")

	if err := d.Commit(); !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("Commit error = %v, want ErrUnsafeDestination", err)
	}
	assertFileClosed(t, openFile)
	if got, err := os.ReadFile(final); err != nil || string(got) != "old" {
		t.Fatalf("original final changed: content=%q err=%v", got, err)
	}
	assertReplacementUnchanged(t, d.PartPath, "symlink")
	if got, err := os.ReadFile(orphan); err != nil || string(got) != "download" {
		t.Fatalf("renamed original part changed: content=%q err=%v", got, err)
	}
}

func TestDestinationNoReplaceRollsBackPartSwapDuringPublish(t *testing.T) {
	for _, kind := range []string{"symlink", "regular"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			d, err := OpenDestination(dir, "result.bin", false)
			if err != nil {
				t.Fatal(err)
			}
			openFile := d.File
			if _, err := openFile.Write([]byte("download")); err != nil {
				t.Fatal(err)
			}

			var orphan string
			d.ops.beforeNoReplace = func() {
				orphan = replacePartEntry(t, d, kind)
			}

			commitErr := d.Commit()
			wantErr := ErrDestinationChanged
			if kind == "symlink" {
				wantErr = ErrUnsafeDestination
			}
			if !errors.Is(commitErr, wantErr) {
				t.Fatalf("Commit error = %v, want %v", commitErr, wantErr)
			}
			assertFileClosed(t, openFile)
			if _, err := os.Lstat(d.FinalPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final was left published after rollback: %v", err)
			}
			assertReplacementUnchanged(t, d.PartPath, kind)
			if got, err := os.ReadFile(orphan); err != nil || string(got) != "download" {
				t.Fatalf("renamed original part changed: content=%q err=%v", got, err)
			}
			assertNoQuarantines(t, dir)
		})
	}
}

func TestDestinationAbortQuarantineDoesNotDeleteNewPublicReplacement(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "cancel.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	openFile := d.File
	d.ops.beforeQuarantineDelete = func() {
		if err := os.WriteFile(d.PartPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	assertFileClosed(t, openFile)
	if got, err := os.ReadFile(d.PartPath); err != nil || string(got) != "replacement" {
		t.Fatalf("new public replacement changed: content=%q err=%v", got, err)
	}
	assertNoQuarantines(t, dir)
}

func TestDestinationDisplacedCleanupDoesNotDeleteNewPartReplacement(t *testing.T) {
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
	d.ops.beforeQuarantineDelete = func() {
		if err := os.WriteFile(d.PartPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "download" {
		t.Fatalf("final content=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(d.PartPath); err != nil || string(got) != "replacement" {
		t.Fatalf("new part replacement changed: content=%q err=%v", got, err)
	}
	assertNoQuarantines(t, dir)
}

func TestDestinationRollbackCleanupDoesNotDeleteNewFinalReplacement(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	d.ops.beforeAbsentPublish = func() {
		if err := os.WriteFile(d.FinalPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := d.Commit(); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Commit error = %v, want ErrDestinationExists", err)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "replacement" {
		t.Fatalf("new final replacement changed: content=%q err=%v", got, err)
	}
	assertNoParts(t, dir)
	assertNoQuarantines(t, dir)
}

func TestDestinationOverwriteRollbackFailureIsStableCleanupIncomplete(t *testing.T) {
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
	exchange := d.ops.exchange
	rollbackErr := errors.New("injected rollback exchange failure")
	calls := 0
	d.ops.exchange = func(dirHandle *anchoredDir, oldName, newName string) error {
		calls++
		if calls == 2 {
			return rollbackErr
		}
		if err := exchange(dirHandle, oldName, newName); err != nil {
			return err
		}
		if err := os.Remove(final); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(final, []byte("attacker"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	}

	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrDestinationChanged) || !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, rollbackErr) {
		t.Fatalf("Commit error = %v, want changed, cleanup, and rollback errors", commitErr)
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "attacker" {
		t.Fatalf("partial public state changed: content=%q err=%v", got, err)
	}
	quarantines, err := filepath.Glob(filepath.Join(dir, ".tgctl-quarantine-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 1 {
		t.Fatalf("private displaced state = %v, want one retained entry", quarantines)
	}
	if got, err := os.ReadFile(quarantines[0]); err != nil || string(got) != "old" {
		t.Fatalf("retained displaced target content=%q err=%v", got, err)
	}
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal partial-state error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationAbsentRestoreFailurePreservesWinnerAndPrivatePart(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	d.ops.beforeAbsentPublish = func() {
		if err := os.WriteFile(d.FinalPath, []byte("winner"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rename := d.ops.renameNoReplace
	restoreErr := errors.New("injected private restore failure")
	d.ops.renameNoReplace = func(dirHandle *anchoredDir, oldName, newName string) error {
		if strings.HasPrefix(oldName, ".tgctl-quarantine-") && newName == d.partName {
			return restoreErr
		}
		return rename(dirHandle, oldName, newName)
	}

	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrDestinationExists) || !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, restoreErr) {
		t.Fatalf("Commit error = %v, want exists, cleanup, and restore errors", commitErr)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "winner" {
		t.Fatalf("winner changed: content=%q err=%v", got, err)
	}
	quarantines, err := filepath.Glob(filepath.Join(dir, ".tgctl-quarantine-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 1 {
		t.Fatalf("private staged state = %v, want one retained entry", quarantines)
	}
	if got, err := os.ReadFile(quarantines[0]); err != nil || string(got) != "download" {
		t.Fatalf("retained staged content=%q err=%v", got, err)
	}
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal restore error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationPostPublishIdentityLossIsStableCleanupIncomplete(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "result.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.File.Write([]byte("download")); err != nil {
		t.Fatal(err)
	}
	rename := d.ops.renameNoReplace
	d.ops.renameNoReplace = func(dirHandle *anchoredDir, oldName, newName string) error {
		if err := rename(dirHandle, oldName, newName); err != nil {
			return err
		}
		if newName == d.finalName {
			if err := os.Remove(d.FinalPath); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(d.FinalPath, []byte("attacker"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrDestinationChanged) || !errors.Is(commitErr, ErrCleanupIncomplete) {
		t.Fatalf("Commit error = %v, want changed and cleanup errors", commitErr)
	}
	if got, err := os.ReadFile(d.FinalPath); err != nil || string(got) != "attacker" {
		t.Fatalf("attacker final changed: content=%q err=%v", got, err)
	}
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal publication error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func TestDestinationDisplacedUnlinkFailureIsStableCleanupIncomplete(t *testing.T) {
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
	injected := errors.New("injected displaced unlink failure")
	d.ops.remove = func(*anchoredDir, string) error { return injected }
	commitErr := d.Commit()
	if !errors.Is(commitErr, ErrCleanupIncomplete) || !errors.Is(commitErr, injected) {
		t.Fatalf("Commit error = %v, want cleanup and unlink errors", commitErr)
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "download" {
		t.Fatalf("published final content=%q err=%v", got, err)
	}
	quarantines, err := filepath.Glob(filepath.Join(dir, ".tgctl-quarantine-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantines) != 1 {
		t.Fatalf("retained displaced state = %v, want one entry", quarantines)
	}
	if got, err := os.ReadFile(quarantines[0]); err != nil || string(got) != "old" {
		t.Fatalf("retained displaced content=%q err=%v", got, err)
	}
	if abortErr := d.Abort(); abortErr.Error() != commitErr.Error() {
		t.Fatalf("terminal displaced cleanup error changed: Commit=%q Abort=%q", commitErr, abortErr)
	}
}

func replacePartEntry(t *testing.T, d *Destination, kind string) string {
	t.Helper()
	orphan := d.PartPath + ".renamed"
	if err := os.Rename(d.PartPath, orphan); err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "symlink":
		victim := d.PartPath + ".victim"
		if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, d.PartPath); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	case "regular":
		if err := os.WriteFile(d.PartPath, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "missing":
	default:
		t.Fatalf("unknown replacement kind %q", kind)
	}
	return orphan
}

func assertReplacementUnchanged(t *testing.T, partPath, kind string) {
	t.Helper()
	switch kind {
	case "symlink":
		target, err := os.Readlink(partPath)
		if err != nil {
			t.Fatalf("replacement symlink removed or changed: %v", err)
		}
		if got, err := os.ReadFile(target); err != nil || string(got) != "safe" {
			t.Fatalf("replacement referent changed: content=%q err=%v", got, err)
		}
	case "regular":
		if got, err := os.ReadFile(partPath); err != nil || string(got) != "replacement" {
			t.Fatalf("replacement regular file changed: content=%q err=%v", got, err)
		}
	case "missing":
		if _, err := os.Lstat(partPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing part entry was recreated: %v", err)
		}
	}
}

func assertFileClosed(t *testing.T, file *os.File) {
	t.Helper()
	if _, err := file.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("original part FD is not closed: %v", err)
	}
}

func TestLimitWriterExactLimitAndUnlimited(t *testing.T) {
	var exact strings.Builder
	w := &LimitWriter{W: &exact, Max: 4}
	if n, err := w.Write([]byte("data")); n != 4 || err != nil {
		t.Fatalf("exact Write = (%d, %v)", n, err)
	}
	if w.N != 4 || exact.String() != "data" {
		t.Fatalf("exact state = N %d, data %q", w.N, exact.String())
	}
	if n, err := w.Write([]byte("x")); n != 0 || !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("past-limit Write = (%d, %v)", n, err)
	}

	var unlimited strings.Builder
	u := &LimitWriter{W: &unlimited, Max: 0}
	if n, err := u.Write([]byte("unlimited")); n != 9 || err != nil {
		t.Fatalf("unlimited Write = (%d, %v)", n, err)
	}
}

func TestLimitWriterNeverWritesBeyondLimit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   string
	}{
		{"single call", []string{"abcdef"}, "abcd"},
		{"multiple calls", []string{"abc", "def"}, "abcd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst strings.Builder
			w := &LimitWriter{W: &dst, Max: 4}
			var limitErr error
			for _, value := range tc.writes {
				_, limitErr = w.Write([]byte(value))
				if limitErr != nil {
					break
				}
			}
			if !errors.Is(limitErr, ErrLimitExceeded) {
				t.Fatalf("error = %v, want ErrLimitExceeded", limitErr)
			}
			if dst.String() != tc.want || w.N != 4 {
				t.Fatalf("wrote %q with N=%d, want %q and N=4", dst.String(), w.N, tc.want)
			}
		})
	}
}

func TestLimitWriterTracksPartialUnderlyingWrites(t *testing.T) {
	underlyingErr := errors.New("disk full")
	sw := &shortErrorWriter{max: 2, err: underlyingErr}
	w := &LimitWriter{W: sw, Max: 5}
	n, err := w.Write([]byte("abcd"))
	if n != 2 || !errors.Is(err, underlyingErr) {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if w.N != 2 || sw.got != "ab" {
		t.Fatalf("state N=%d data=%q", w.N, sw.got)
	}

	short := &shortErrorWriter{max: 2}
	w = &LimitWriter{W: short, Max: 5}
	if n, err := w.Write([]byte("abcd")); n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short Write = (%d, %v), want io.ErrShortWrite", n, err)
	}
}

func TestLimitWriterRejectsNegativeLimitWithoutWriting(t *testing.T) {
	var dst strings.Builder
	w := &LimitWriter{W: &dst, Max: -1}
	if n, err := w.Write([]byte("data")); n != 0 || !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("Write = (%d, %v), want ErrInvalidLimit", n, err)
	}
	if dst.Len() != 0 || w.N != 0 {
		t.Fatalf("negative limit wrote data: %q N=%d", dst.String(), w.N)
	}
}

func TestLimitWriterErrorCanBeAbortedWithoutPartLeftover(t *testing.T) {
	dir := t.TempDir()
	d, err := OpenDestination(dir, "large.bin", false)
	if err != nil {
		t.Fatal(err)
	}
	w := &LimitWriter{W: d.File, Max: 3}
	if _, err := w.Write([]byte("too large")); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Write error = %v, want ErrLimitExceeded", err)
	}
	if err := d.Abort(); err != nil {
		t.Fatal(err)
	}
	assertNoParts(t, dir)
}

type shortErrorWriter struct {
	max int
	err error
	got string
}

func (w *shortErrorWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	w.got += string(p)
	return len(p), w.err
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %#o, want %#o", path, got, want)
	}
}

func assertNoParts(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.part"))
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := filepath.Glob(filepath.Join(dir, ".*.part"))
	if err != nil {
		t.Fatal(err)
	}
	matches = append(matches, hidden...)
	if len(matches) != 0 {
		t.Fatalf("part files remain: %v", matches)
	}
}

func assertNoQuarantines(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".tgctl-quarantine-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("quarantine entries remain: %v", matches)
	}
}
