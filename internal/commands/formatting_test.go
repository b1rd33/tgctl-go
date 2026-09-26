package commands

import (
	textutil "github.com/b1rd33/tgctl-go/internal/text"
	"strings"
	"testing"
)

func TestFormattingChangesIdempotencyAndRejectsInvalidBeforeSend(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	args := []string{"send", "1", "😀 hi", "--allow-write", "--idempotency-key", "format", "--json", "--entities", `[{"type":"bold","offset":3,"length":2}]`}
	if out, code := runRoot(t, cfg, args...); code != 0 || len(fc.Sent) != 1 || len(fc.Sent[0].Entities) != 1 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	args[len(args)-1] = `[{"type":"italic","offset":3,"length":2}]`
	if out, code := runRoot(t, cfg, args...); code == 0 || len(fc.Sent) != 1 {
		t.Fatalf("changed formatting replayed: %d %s", code, out)
	}
	args[len(args)-1] = `[{"type":"bold","offset":1,"length":1}]`
	if out, code := runRoot(t, cfg, args...); code == 0 || len(fc.Sent) != 1 {
		t.Fatalf("invalid sent: %d %s", code, out)
	}
	if _, err := readTextArg("-", strings.NewReader(strings.Repeat("a", textutil.MaxInputBytes+1))); err == nil {
		t.Fatal("unbounded stdin")
	}
	if _, err := readTextArg("-", strings.NewReader("")); err == nil {
		t.Fatal("empty stdin")
	}
}

func TestAlbumFingerprintIncludesFormatting(t *testing.T) {
	a, err := albumFingerprint("default", 1, nil, "photo", "hi", 0, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := albumFingerprint("default", 1, nil, "photo", "hi", 0, false, false, []textutil.Entity{{Type: "bold", Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("formatting missing from fingerprint")
	}
}

func TestAlbumCommandPassesCaptionEntities(t *testing.T) {
	cfg, fc, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.mp4", []byte("video"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--caption", "hi", "--entities", `[{"type":"italic","offset":0,"length":2}]`, "--allow-write", "--json")
	if code != 0 || len(fc.Albums) != 1 || len(fc.Albums[0].Entities) != 1 {
		t.Fatalf("code=%d out=%s", code, out)
	}
}
