package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type changingAccountPaths struct {
	firstName, secondName string
	first, second         stubPaths
	calls                 int
}

func (p *changingAccountPaths) Current() string {
	if p.calls == 0 {
		return p.firstName
	}
	return p.secondName
}

func (p *changingAccountPaths) AccountPaths(account string) (string, string, string, error) {
	p.calls++
	if account == p.firstName {
		return p.first.db, p.first.session, p.first.audit, nil
	}
	return p.second.db, p.second.session, p.second.audit, nil
}

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
	// Paths land in audit.log JSON-escaped, so on Windows backslashes are
	// doubled. Marshal the expected path and strip the surrounding quotes
	// to get the same byte form.
	needle, _ := json.Marshal(filepath.Clean(path))
	if !strings.Contains(string(auditBytes), string(needle[1:len(needle)-1])) {
		t.Fatalf("audit missing source path:\n%s", auditBytes)
	}
}

func TestUploadDocumentUsesOneResolvedAccountForAllSideEffects(t *testing.T) {
	rootDir := t.TempDir()
	first := stubPaths{
		db:      filepath.Join(rootDir, "first", "telegram.sqlite"),
		session: filepath.Join(rootDir, "first", "tg.session"),
		audit:   filepath.Join(rootDir, "first", "audit.log"),
	}
	second := stubPaths{
		db:      filepath.Join(rootDir, "second", "telegram.sqlite"),
		session: filepath.Join(rootDir, "second", "tg.session"),
		audit:   filepath.Join(rootDir, "second", "audit.log"),
	}
	seedChat(t, first.db, 1, "First")
	seedChat(t, second.db, 1, "Second")
	paths := &changingAccountPaths{firstName: "first", secondName: "second", first: first, second: second}
	fake := &client.FakeClient{NextMessageID: 777}
	var clientSession, clientDB string
	cfg := CommandsConfig{
		Paths: paths,
		ClientFactory: func(_ context.Context, sessionPath, dbPath string) (client.Client, error) {
			clientSession, clientDB = sessionPath, dbPath
			return fake, nil
		},
	}
	path := writeMediaFixture(t, "doc.bin", []byte("document"))

	out, code := runRoot(t, cfg, "upload-document", "1", path, "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if paths.calls != 1 {
		t.Errorf("account paths resolved %d times, want once", paths.calls)
	}
	if clientSession != first.session || clientDB != first.db {
		t.Errorf("client paths = (%q, %q), want (%q, %q)", clientSession, clientDB, first.session, first.db)
	}
	assertAuditContains(t, first.audit, `"cmd":"upload-document"`)
	assertPathMissing(t, second.audit)
	assertUploadedMessage(t, first.db, 1, 777, true)
	assertUploadedMessage(t, second.db, 1, 777, false)
}

func assertUploadedMessage(t *testing.T, dbPath string, chatID, messageID int64, want bool) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND message_id=? AND has_media=1", chatID, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Errorf("uploaded message exists in %s = %v, want %v", dbPath, got, want)
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
