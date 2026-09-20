package commands

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaHashWorkflowOffline(t *testing.T) {
	cfg, _, dir := setupWriteEnv(t)
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		t.Fatal("network factory used")
		return nil, nil
	}
	path := filepath.Join(dir, "picture.jpg")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []int{1, 2} {
		if _, err := db.Exec(`INSERT INTO tg_messages(chat_id,message_id,has_media,media_type,media_path,media_id) VALUES(1,?,1,'photo',?,'photo:1')`, id, path); err != nil {
			t.Fatal(err)
		}
	}
	digest := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	out, code := runRoot(t, cfg, "--account", "default", "--read-only", "media-hash", path, "--json")
	if code != 0 || !strings.Contains(out, digest) {
		t.Fatalf("%d %s", code, out)
	}
	out, code = runRoot(t, cfg, "--account", "default", "media-find", digest, "--json")
	if code != 0 || !strings.Contains(out, `"matches":[]`) {
		t.Fatalf("%d %s", code, out)
	}
	for _, args := range [][]string{{"media-index", "1", "--json"}, {"--read-only", "media-index", "1", "--allow-write", "--json"}} {
		if out, code := runRoot(t, cfg, args...); code == 0 {
			t.Fatal(out)
		}
	}
	out, code = runRoot(t, cfg, "--account", "default", "media-index", "1", "--allow-write", "--limit", "1", "--json")
	if code != 0 || !strings.Contains(out, `"has_more":true`) {
		t.Fatalf("%d %s", code, out)
	}
	out, code = runRoot(t, cfg, "--account", "default", "media-index", "1", "--allow-write", "--after-id", "1", "--json")
	if code != 0 || !strings.Contains(out, `"indexed":1`) {
		t.Fatalf("%d %s", code, out)
	}
	out, code = runRoot(t, cfg, "media-find", digest, "--limit", "1", "--json")
	if code != 0 || !strings.Contains(out, `"truncated":true`) {
		t.Fatalf("%d %s", code, out)
	}
	other, _, _ := setupWriteEnv(t)
	out, code = runRoot(t, other, "media-find", digest, "--json")
	if code != 0 || !strings.Contains(out, `"matches":[]`) {
		t.Fatalf("account leaked: %d %s", code, out)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out, code = runRoot(t, cfg, "--account", "default", "--read-only", "media-find", digest, "--json")
	if code != 0 || !strings.Contains(out, `"message_id":2`) {
		t.Fatalf("lookup after removal: %d %s", code, out)
	}
	out, code = runRoot(t, cfg, "media-index", "1", "--allow-write", "--json")
	if code != 0 || !strings.Contains(out, `"indexed":0`) {
		t.Fatalf("missing file: %d %s", code, out)
	}
	if out, code := runRoot(t, cfg, "media-find", "not-a-hash", "--json"); code == 0 {
		t.Fatal(out)
	}
}
