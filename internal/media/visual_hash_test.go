package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestVisualHashResizeAndRecompression(t *testing.T) {
	hashes := []uint64{}
	for _, scale := range []int{1, 2} {
		img := image.NewGray(image.Rect(0, 0, 90*scale, 80*scale))
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				v := uint8((x/(10*scale)*31 + y/(10*scale)*17) % 256)
				img.SetGray(x, y, color.Gray{Y: v})
			}
		}
		p := filepath.Join(t.TempDir(), fmt.Sprint(scale))
		f, e := os.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		if scale == 1 {
			e = png.Encode(f, img)
		} else {
			e = jpeg.Encode(f, img, &jpeg.Options{Quality: 75})
		}
		if e != nil {
			t.Fatal(e)
		}
		f.Close()
		h, e := HashVisualFile(context.Background(), p, 0)
		if e != nil {
			t.Fatal(e)
		}
		v, e := strconv.ParseUint(h.DHash, 16, 64)
		if e != nil {
			t.Fatal(e)
		}
		hashes = append(hashes, v)
		if len(h.SHA256) != 64 || h.Algorithm != VisualAlgorithm {
			t.Fatal(h)
		}
		if _, e = HashVisualFile(context.Background(), p, 1); e == nil {
			t.Fatal("byte limit ignored")
		}
	}
	if distance := bits.OnesCount64(hashes[0] ^ hashes[1]); distance > 6 {
		t.Fatalf("resized JPEG distance %d", distance)
	}
}
func TestVisualHashRejectsInvalidSmallAndCanceled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "image.png")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, image.NewGray(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = HashVisualFile(context.Background(), p, 0); e == nil {
		t.Fatal("tiny image accepted")
	}
	if e = os.WriteFile(p, []byte("not an image"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = HashVisualFile(context.Background(), p, 0); e == nil {
		t.Fatal("invalid image accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = HashVisualFile(ctx, p, 0); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestVisualHashRejectsOversizedDecodedImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "huge.png")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, image.NewGray(image.Rect(0, 0, 9, 8))); e != nil {
		t.Fatal(e)
	}
	f.Close()
	data, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if e = os.WriteFile(p, data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = HashVisualFile(context.Background(), p, 0); e == nil {
		t.Fatal("pixel limit ignored")
	}
}
