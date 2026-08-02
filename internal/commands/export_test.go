package commands

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func seedExportRows(t *testing.T, cfg CommandsConfig, mediaPath string) {
	t.Helper()
	paths := cfg.Paths.(stubPaths)
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	text := "<hello & goodbye>"
	mediaType := "photo"
	if err := store.InsertMessage(db, store.Message{
		ChatID: 1, MessageID: 1, Date: "2026-08-01T10:00:00Z", Text: &text,
		GroupedID: 700, HasMedia: true, MediaType: &mediaType, MediaPath: &mediaPath,
	}); err != nil {
		t.Fatal(err)
	}
	other := "outside range"
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 2, Date: "2026-08-03T10:00:00Z", Text: &other}); err != nil {
		t.Fatal(err)
	}
}

func TestExportCommandWritesJSONLCSVAndHTMLLocally(t *testing.T) {
	cases := []struct {
		name   string
		format string
		check  func(t *testing.T, data []byte)
	}{
		{
			name:   "jsonl",
			format: "jsonl",
			check: func(t *testing.T, data []byte) {
				var row exportRecord
				if err := json.Unmarshal(bytesFirstLine(data), &row); err != nil {
					t.Fatal(err)
				}
				if row.MessageID != 1 || row.GroupedID != 700 || row.MediaPath != "1/photo.jpg" {
					t.Fatalf("row=%+v", row)
				}
			},
		},
		{
			name:   "csv",
			format: "csv",
			check: func(t *testing.T, data []byte) {
				rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 2 || rows[1][1] != "1" || rows[1][7] != "700" || rows[1][9] != "1/photo.jpg" {
					t.Fatalf("rows=%#v", rows)
				}
			},
		},
		{
			name:   "html",
			format: "html",
			check: func(t *testing.T, data []byte) {
				got := string(data)
				if !strings.Contains(got, "&lt;hello &amp; goodbye&gt;") || !strings.Contains(got, "1/photo.jpg") {
					t.Fatalf("html=%s", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fake, dir := setupWriteEnv(t)
			mediaPath := filepath.Join(dir, "media", "1", "photo.jpg")
			seedExportRows(t, cfg, mediaPath)
			output := filepath.Join(dir, "exports", tc.name+".out")
			out, code := runRoot(t, cfg, "export", "1", "--format", tc.format, "--output", output, "--include-media", "--since", "2026-08-01", "--until", "2026-08-02T23:59:59Z", "--json")
			if code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, data)
			if len(fake.Calls) != 0 {
				t.Fatalf("Telegram client was called by local export: %#v", fake.Calls)
			}
		})
	}
}

func TestExportCommandRefusesToOverwriteOutput(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	seedExportRows(t, cfg, filepath.Join(dir, "media", "1", "photo.jpg"))
	output := filepath.Join(dir, "existing.jsonl")
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runRoot(t, cfg, "export", "1", "--output", output, "--json")
	if code == 0 || !strings.Contains(out, "already exists") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep me" {
		t.Fatalf("existing output changed: %q", data)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("Telegram client was called by local export: %#v", fake.Calls)
	}
}

func TestExportCommandWritesAndVerifiesManifest(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	mediaPath := filepath.Join(dir, "media", "1", "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("same-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedExportRows(t, cfg, mediaPath)
	output := filepath.Join(dir, "exports", "chat.jsonl")
	manifest := filepath.Join(dir, "exports", "chat.manifest.json")
	out, code := runRoot(t, cfg, "export", "1", "--output", output, "--manifest", manifest, "--manifest-hash", "--include-media", "--json")
	if code != 0 {
		t.Fatalf("export code=%d out=%s", code, out)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal(err)
	}
	out, code = runRoot(t, cfg, "export", "--verify", manifest, "--json")
	if code != 0 || !strings.Contains(out, `"checked":1`) {
		t.Fatalf("verify code=%d out=%s", code, out)
	}
	if err := os.WriteFile(mediaPath, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code = runRoot(t, cfg, "export", "--verify", manifest, "--json")
	if code != 12 || !strings.Contains(out, "ARCHIVE_CHANGED") {
		t.Fatalf("changed verify code=%d out=%s", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "media", "extra.jpg"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code = runRoot(t, cfg, "export", "--verify", manifest, "--json")
	if code != 12 || !strings.Contains(out, "ARCHIVE_CHANGED") {
		t.Fatalf("changed+extra priority code=%d out=%s", code, out)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("Telegram client was called by local manifest operations: %#v", fake.Calls)
	}
}

func TestExportCommandStdoutKeepsJSONEnvelopeValid(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	seedExportRows(t, cfg, filepath.Join(dir, "media", "1", "photo.jpg"))
	out, code := runRoot(t, cfg, "export", "1", "--format", "jsonl", "--output", "-", "--include-media", "--since", "2026-08-01", "--until", "2026-08-02T23:59:59Z", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Rows    int    `json:"rows"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("invalid JSON envelope: %v\n%s", err, out)
	}
	if !envelope.OK || envelope.Data.Rows != 1 || !strings.Contains(envelope.Data.Content, `"message_id":1`) {
		t.Fatalf("envelope=%+v", envelope)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("Telegram client was called by local export: %#v", fake.Calls)
	}
}

func bytesFirstLine(data []byte) []byte {
	if i := strings.IndexByte(string(data), '\n'); i >= 0 {
		return data[:i]
	}
	return data
}
