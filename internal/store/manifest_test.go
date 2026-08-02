package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveManifestBuildAndVerifyHashes(t *testing.T) {
	root := t.TempDir()
	mediaDir := filepath.Join(root, "media")
	if err := os.MkdirAll(filepath.Join(mediaDir, "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mediaDir, "1", "photo.jpg")
	if err := os.WriteFile(path, []byte("same-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	relPath := path
	manifest, warnings, err := BuildArchiveManifest([]Message{{MessageID: 11, GroupedID: 77, MediaPath: &relPath}}, "test", 1, "jsonl", "chat.jsonl", mediaDir, true)
	if err != nil || len(warnings) != 0 || len(manifest.Items) != 1 || !manifest.Items[0].Present || manifest.Items[0].SHA256 == "" {
		t.Fatalf("manifest=%+v warnings=%v err=%v", manifest, warnings, err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	f, err := os.Create(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteArchiveManifest(f, manifest); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, result, err := VerifyArchiveManifest(manifestPath, mediaDir)
	if err != nil || result.Checked != 1 || len(result.Missing) != 0 || len(result.Changed) != 0 || len(result.Extra) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := os.WriteFile(path, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, result, err = VerifyArchiveManifest(manifestPath, mediaDir)
	if err != nil || len(result.Changed) != 1 || result.Changed[0] != "1/photo.jpg" {
		t.Fatalf("same-size change result=%+v err=%v", result, err)
	}
}

func TestArchiveManifestVerificationReportsMissingAndExtra(t *testing.T) {
	root := t.TempDir()
	mediaDir := filepath.Join(root, "media")
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := ArchiveManifest{Version: 1, ChatID: 1, Items: []ArchiveManifestItem{{MessageID: 1, MediaPath: "missing.jpg", Present: true, Size: 3}, {MessageID: 2, MediaPath: "gone.jpg", Present: true, Size: 3}}}
	manifestPath := filepath.Join(root, "manifest.json")
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "unrecorded.jpg"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, result, err := VerifyArchiveManifest(manifestPath, mediaDir)
	if err != nil || len(result.Missing) != 2 || len(result.Extra) != 1 || result.Extra[0] != "unrecorded.jpg" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestArchiveManifestRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := ArchiveManifest{Version: 1, Items: []ArchiveManifestItem{{MediaPath: "../secret"}}}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := VerifyArchiveManifest(manifestPath, filepath.Join(root, "media"))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err=%v, want traversal rejection", err)
	}
}
