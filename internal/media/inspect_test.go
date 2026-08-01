package media

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDownloadedArtifactAcceptsAnchoredRegularDirectChild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := InspectDownloadedArtifact(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != path || got.Filename != "asset.bin" || got.Size != 7 {
		t.Fatalf("inspection=%#v", got)
	}
}

func TestInspectDownloadedArtifactRejectsMissingSymlinkDirectoryAndNested(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "link.bin")
	if err := os.Symlink(victim, symlink); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(dir, "directory.bin")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(dir, "nested")
	if err := os.Mkdir(nestedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(nestedDir, "asset.bin")
	if err := os.WriteFile(nested, []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.bin"), symlink, directory, nested} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := InspectDownloadedArtifact(dir, path); err == nil {
				t.Fatalf("InspectDownloadedArtifact(%q) succeeded", path)
			}
		})
	}
}

func TestInspectDownloadedArtifactRejectsFinalSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := inspectDownloadedArtifactWithHook(dir, path, func() {
		if removeErr := os.Remove(path); removeErr != nil {
			t.Fatal(removeErr)
		}
		if linkErr := os.Symlink(victim, path); linkErr != nil {
			t.Fatal(linkErr)
		}
	})
	if !errors.Is(err, ErrDestinationChanged) && !errors.Is(err, ErrUnsafeDestination) {
		t.Fatalf("error=%v want changed/unsafe", err)
	}
}

func TestInspectDownloadedArtifactRejectsParentSwap(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "asset.bin")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(base, "old-downloads")
	_, err := inspectDownloadedArtifactWithHook(dir, path, func() {
		if renameErr := os.Rename(dir, oldDir); renameErr != nil {
			t.Fatal(renameErr)
		}
		if mkdirErr := os.Mkdir(dir, 0o700); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		if writeErr := os.WriteFile(path, []byte("attacker"), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	})
	if !errors.Is(err, ErrDestinationChanged) {
		t.Fatalf("error=%v want ErrDestinationChanged", err)
	}
}
