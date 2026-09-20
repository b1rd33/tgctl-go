package client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"io"
	"os"
	"path/filepath"
)

type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type uploadedSnapshot struct {
	File   tg.InputFileClass
	SHA256 string
	Bytes  int64
}

// Upload immutable private bytes matching the command's preflight fingerprint.
func uploadSnapshot(ctx context.Context, api uploader.Client, path string) (uploadedSnapshot, error) {
	source, err := os.Open(path)
	if err != nil {
		return uploadedSnapshot{}, err
	}
	defer source.Close()
	snapshot, err := os.CreateTemp("", ".tgctl-upload-")
	if err != nil {
		return uploadedSnapshot{}, err
	}
	defer os.Remove(snapshot.Name())
	defer snapshot.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(snapshot, hash), cancelReader{ctx, source})
	if err != nil {
		return uploadedSnapshot{}, err
	}
	if expected := safety.ExpectedFileDigest(ctx, path); expected != "" && expected != fmt.Sprintf("%x", hash.Sum(nil)) {
		return uploadedSnapshot{}, &safety.DefinitiveRejection{Err: safety.NewBadArgs("upload file changed after preflight; no message was sent")}
	}
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		return uploadedSnapshot{}, err
	}
	file, err := uploader.NewUploader(api).Upload(ctx, uploader.NewUpload(filepath.Base(path), snapshot, size))
	if err != nil {
		return uploadedSnapshot{}, err
	}
	return uploadedSnapshot{File: file, SHA256: fmt.Sprintf("%x", hash.Sum(nil)), Bytes: size}, nil
}
