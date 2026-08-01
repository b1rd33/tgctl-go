//go:build unix

package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/gotd/td/tg"
)

func TestGotdDownloadMediaExistingFIFOIsNotSkipped(t *testing.T) {
	message := documentMessageWithSize(29, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "pipe.bin"})
	g, outputDir, downloader := downloadTestClient(t, message, []byte("new"))
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(outputDir, "pipe.bin"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 29, OutputDir: outputDir})
	if !errors.Is(err, media.ErrUnsafeDestination) || got.Skipped {
		t.Fatalf("response=%#v error=%v, want unsafe destination", got, err)
	}
	if downloader.Calls() != 0 {
		t.Fatalf("downloader calls = %d, want 0", downloader.Calls())
	}
}
