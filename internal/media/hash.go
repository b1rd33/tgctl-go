package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// HashFile hashes a stable regular file without loading it into memory.
func HashFile(ctx context.Context, path string, maxBytes int64) (string, int64, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !before.Mode().IsRegular() {
		return "", 0, fmt.Errorf("hash input must be a regular file without symlinks")
	}
	if maxBytes > 0 && before.Size() > maxBytes {
		return "", 0, fmt.Errorf("file exceeds hash size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(before, opened) {
		return "", 0, fmt.Errorf("hash input changed while opening")
	}
	h := sha256.New()
	buf := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if maxBytes > 0 && total > maxBytes {
			return "", 0, fmt.Errorf("file exceeds hash size limit")
		}
		_, _ = h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	after, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	named, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(opened, named) || total != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return "", 0, fmt.Errorf("hash input changed during read")
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}
