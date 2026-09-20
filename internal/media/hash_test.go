package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHashFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(p, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	h, n, e := HashFile(context.Background(), p, 3)
	if e != nil || n != 3 || h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("%s %d %v", h, n, e)
	}
	if _, _, e = HashFile(context.Background(), p, 2); e == nil {
		t.Fatal("size cap ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e = HashFile(ctx, p, 0); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, _, e = HashFile(context.Background(), filepath.Dir(p), 0); e == nil {
		t.Fatal("directory accepted")
	}
	link := p + ".link"
	if os.Symlink(p, link) == nil {
		if _, _, e = HashFile(context.Background(), link, 0); e == nil {
			t.Fatal("symlink accepted")
		}
	}
}
