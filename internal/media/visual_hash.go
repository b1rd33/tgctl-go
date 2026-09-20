package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

const VisualAlgorithm = "dhash64-v1"
const MaxVisualBytes int64 = 20 * 1024 * 1024
const maxVisualPixels = 24_000_000

type VisualHash struct {
	SHA256    string `json:"sha256"`
	DHash     string `json:"dhash"`
	Algorithm string `json:"algorithm"`
	Bytes     int64  `json:"bytes"`
}

// HashVisualFile decodes a bounded immutable byte buffer, then averages each
// of 9x8 grayscale cells. Each bit compares adjacent horizontal cells.
// It is a candidate-finding heuristic, never a content identity check.
func HashVisualFile(ctx context.Context, path string, maxBytes int64) (VisualHash, error) {
	if err := ctx.Err(); err != nil {
		return VisualHash{}, err
	}
	if maxBytes <= 0 || maxBytes > MaxVisualBytes {
		maxBytes = MaxVisualBytes
	}
	info, err := os.Lstat(path)
	if err != nil {
		return VisualHash{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return VisualHash{}, fmt.Errorf("visual input must be a regular file within the byte limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return VisualHash{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return VisualHash{}, err
	}
	if !os.SameFile(info, opened) {
		return VisualHash{}, fmt.Errorf("visual input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, maxBytes+1))
	if err != nil {
		return VisualHash{}, err
	}
	if int64(len(data)) > maxBytes {
		return VisualHash{}, fmt.Errorf("visual input exceeds byte limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return VisualHash{}, fmt.Errorf("visual input must be JPEG or PNG")
	}
	if format != "jpeg" && format != "png" {
		return VisualHash{}, fmt.Errorf("visual input must be JPEG or PNG")
	}
	if config.Width < 9 || config.Height < 8 || config.Width > maxVisualPixels/config.Height {
		return VisualHash{}, fmt.Errorf("visual image dimensions must be at least 9x8 and at most 24 million pixels")
	}
	img, _, err := image.Decode(contextReader{ctx, bytes.NewReader(data)})
	if err != nil {
		return VisualHash{}, err
	}
	var sums [8][9]uint64
	var counts [8][9]uint64
	bounds := img.Bounds()
	for y := 0; y < config.Height; y++ {
		if err := ctx.Err(); err != nil {
			return VisualHash{}, err
		}
		gy := y * 8 / config.Height
		for x := 0; x < config.Width; x++ {
			gx := x * 9 / config.Width
			r, g, b, a := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Composite premultiplied color over white for deterministic transparency.
			white := uint64(65535 - a)
			sums[gy][gx] += 299*(uint64(r)+white) + 587*(uint64(g)+white) + 114*(uint64(b)+white)
			counts[gy][gx]++
		}
	}
	var hash uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			hash <<= 1
			if sums[y][x]/counts[y][x] > sums[y][x+1]/counts[y][x+1] {
				hash |= 1
			}
		}
	}
	return VisualHash{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), DHash: fmt.Sprintf("%016x", hash), Algorithm: VisualAlgorithm, Bytes: int64(len(data))}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
