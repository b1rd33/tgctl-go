package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMediaFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUploadDocumentInvokesClientAndAuditsPath(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	path := writeMediaFixture(t, "doc.bin", []byte("document"))

	out, code := runRoot(t, cfg, "upload-document", "1", path, "--caption", "caption", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Uploads) != 1 {
		t.Fatalf("fc.Uploads = %#v", fc.Uploads)
	}
	if fc.Uploads[0].Kind != "document" || fc.Uploads[0].Caption != "caption" {
		t.Fatalf("upload = %#v", fc.Uploads[0])
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\nout: %s", err, out)
	}
	data := env["data"].(map[string]any)
	if data["media_type"] != "document" || data["message_id"].(float64) == 0 {
		t.Fatalf("data = %#v", data)
	}

	auditBytes, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(auditBytes), filepath.Clean(path)) {
		t.Fatalf("audit missing source path:\n%s", auditBytes)
	}
}

func TestUploadPhotoDryRunSkipsClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	path := writeMediaFixture(t, "photo.webp", []byte("RIFFxxxxWEBPdata"))

	out, code := runRoot(t, cfg, "upload-photo", "1", path, "--allow-write", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Uploads) != 0 {
		t.Fatalf("client called in dry-run: %#v", fc.Uploads)
	}
	if !strings.Contains(out, `"dry_run":true`) || !strings.Contains(out, `"media_type":"photo"`) {
		t.Fatalf("unexpected dry-run output: %s", out)
	}
}

func TestUploadDocumentIdempotentReplay(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	path := writeMediaFixture(t, "doc.bin", []byte("document"))

	first, code := runRoot(t, cfg, "upload-document", "1", path, "--allow-write", "--idempotency-key", "media-k", "--json")
	if code != 0 {
		t.Fatalf("first code=%d\nout:%s", code, first)
	}
	second, code := runRoot(t, cfg, "upload-document", "1", path, "--allow-write", "--idempotency-key", "media-k", "--json")
	if code != 0 {
		t.Fatalf("second code=%d\nout:%s", code, second)
	}
	if len(fc.Uploads) != 1 {
		t.Fatalf("client called again on replay: %#v", fc.Uploads)
	}
	if !strings.Contains(second, `"idempotent_replay":true`) {
		t.Fatalf("replay flag missing: %s", second)
	}
}

func TestUploadRejectsUnsafePathBeforeClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "upload-document", "1", "bad?name.txt", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.Uploads) != 0 {
		t.Fatalf("client called: %#v", fc.Uploads)
	}
}
