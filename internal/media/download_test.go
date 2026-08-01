package media

import (
	"errors"
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
	originalGenerator := generatePartName
	candidates := []string{collision, ".file.bin.safe.part"}
	generatePartName = func(string) (string, error) {
		candidate := candidates[0]
		candidates = candidates[1:]
		return candidate, nil
	}
	t.Cleanup(func() { generatePartName = originalGenerator })

	d, err := OpenDestination(dir, "file.bin", false)
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

	originalHook := beforeOverwritePublish
	beforeOverwritePublish = func() {
		if err := os.Remove(final); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, final); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeOverwritePublish = originalHook })

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
