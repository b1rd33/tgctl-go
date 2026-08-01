//go:build darwin || linux

package media

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInspectDownloadedArtifactRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe.bin")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if _, err := InspectDownloadedArtifact(dir, path); err == nil {
		t.Fatal("FIFO inspection succeeded")
	}
}
