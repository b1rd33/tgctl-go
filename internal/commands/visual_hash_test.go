package commands

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVisualWorkflowOfflineAfterFileRemoval(t *testing.T) {
	cfg, _, dir := setupWriteEnv(t)
	p := filepath.Join(dir, "image.png")
	img := image.NewGray(image.Rect(0, 0, 90, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 90; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x * 2)})
		}
	}
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	f.Close()
	db, e := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO tg_messages(chat_id,message_id,has_media,media_path) VALUES(1,1,1,?)`, p); e != nil {
		t.Fatal(e)
	}
	db.Close()
	hash, e := media.HashVisualFile(context.Background(), p, 0)
	if e != nil {
		t.Fatal(e)
	}
	out, code := runRoot(t, cfg, "media-hash", p, "--visual", "--read-only", "--json")
	if code != 0 || !strings.Contains(out, hash.DHash) {
		t.Fatalf("%d %s", code, out)
	}
	out, code = runRoot(t, cfg, "media-index", "1", "--visual", "--allow-write", "--json")
	if code != 0 || !strings.Contains(out, `"visual_indexed":1`) {
		t.Fatalf("%d %s", code, out)
	}
	if e = os.Remove(p); e != nil {
		t.Fatal(e)
	}
	out, code = runRoot(t, cfg, "media-similar", hash.DHash, "--read-only", "--json")
	if code != 0 || !strings.Contains(out, `"message_id":1`) || !strings.Contains(out, `"approximate":true`) {
		t.Fatalf("%d %s", code, out)
	}
	for _, args := range [][]string{{"media-similar", "invalid"}, {"media-similar", hash.DHash, "--distance", "65"}} {
		if out, code := runRoot(t, cfg, args...); code == 0 {
			t.Fatal(out)
		}
	}
}
