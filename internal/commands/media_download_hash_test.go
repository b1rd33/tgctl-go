package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestDownloadMediaIndexesSuccessfulDownloadedBytes(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "media", "1")
	path := configureDownload(t, cfg, fake, 1, 9, output, false)
	fake.DownloadResp.MediaIdentity = "document:70"
	seed, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec("INSERT INTO tg_messages(chat_id,message_id,date,has_media,media_type,media_id) VALUES(1,9,'2026-08-01T00:00:09Z',1,'document','document:70')"); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 0 || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	digest, size, err := media.HashFile(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	matches, err := store.FindMediaHash(context.Background(), db, digest, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Representation != "downloaded" || matches[0].MessageID != 9 || matches[0].Bytes != size {
		t.Fatalf("matches=%#v", matches)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if matches, err = store.FindMediaHash(context.Background(), db, digest, 10); err != nil || len(matches) != 1 {
		t.Fatal("hash lost after deleting file")
	}
}

func TestDownloadAlbumIndexesEachSuccessfulDownloadedBytes(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	mediaType := "photo"
	seedAlbumRows(t, filepath.Join(dir, "telegram.sqlite"),
		store.Message{ChatID: 1, MessageID: 101, GroupedID: 901, Date: "2026-08-01T00:00:01Z", HasMedia: true, MediaType: &mediaType},
		store.Message{ChatID: 1, MessageID: 102, GroupedID: 901, Date: "2026-08-01T00:00:02Z", HasMedia: true, MediaType: &mediaType},
	)
	output := filepath.Join(dir, "album-hash")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	first := albumArtifact(t, output, 101, "photo")
	second := albumArtifact(t, output, 102, "photo")
	fake.DownloadResponses = []client.DownloadMediaResp{first, second}
	out, code := runRoot(t, cfg, "download-album", "1", "--grouped-id", "901", "--output", output, "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	var envelope struct {
		Data downloadAlbumResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Warnings) != 0 || envelope.Data.Downloaded != 2 {
		t.Fatalf("result=%#v", envelope.Data)
	}
	digest, _, err := media.HashFile(context.Background(), first.Path, 0)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	matches, err := store.FindMediaHash(context.Background(), db, digest, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Representation != "downloaded" || matches[1].Representation != "downloaded" {
		t.Fatalf("matches=%#v", matches)
	}
}

func TestDownloadMediaHashFailurePreservesSuccessfulDownload(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "media", "1")
	path := configureDownload(t, cfg, fake, 1, 9, output, false)
	identity := "cached-media-identity"
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	mediaType := "document"
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 9, Date: "2026-08-01T00:00:09Z", HasMedia: true, MediaType: &mediaType, MediaPath: &path, MediaIdentity: &identity}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fake.DownloadResp.MediaIdentity = "remote-media-identity"
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 0 || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, downloadedMediaHashWarning) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	readDB, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()
	digest, _, err := media.HashFile(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := store.FindMediaHash(context.Background(), readDB, digest, 10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("unexpected hash matches after identity failure: %#v", matches)
	}
}

func TestBackfillIndexesOnlyFreshDownloads(t *testing.T) {
	_, _, dir := setupWriteEnv(t)
	dbPath := filepath.Join(dir, "telegram.sqlite")
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(dir, "image")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if _, err := db.Exec(`INSERT INTO tg_messages(chat_id,message_id,date,has_media,media_path,media_id) VALUES(1,?,'2026-09-20T00:00:00Z',1,?,'photo:1')`, id, path); err != nil {
			t.Fatal(err)
		}
	}
	rows := []client.BackfillMessage{
		{MessageID: 1, MediaPath: path, MediaIdentity: "photo:1", MediaDisposition: client.BackfillMediaDownloaded},
		{MessageID: 2, MediaPath: path, MediaIdentity: "photo:1", MediaDisposition: client.BackfillMediaSkipped},
		{MessageID: 3, MediaPath: path, MediaIdentity: "photo:1", MediaDisposition: client.BackfillMediaDownloaded},
	}
	if warnings := indexBackfillDownloadedHashes(context.Background(), dbPath, 1, rows); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	hash, _, err := media.HashFile(context.Background(), path, 0)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := store.FindMediaHash(context.Background(), db, hash, 10)
	if err != nil || len(matches) != 1 || matches[0].MessageID != 1 {
		t.Fatalf("%+v %v", matches, err)
	}
	missing := filepath.Join(dir, "must-not-exist", "cache.sqlite")
	if warnings := indexBackfillDownloadedHashes(context.Background(), missing, 1, rows[1:2]); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("skip-only backfill created database")
	}
}

func TestDownloadedHashRejectsReplacedArtifact(t *testing.T) {
	_, _, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	path := filepath.Join(dir, "image")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, artifact, err := media.CaptureArtifactIdentity(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("xyz"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := indexDownloadedMediaHash(context.Background(), db, 1, 1, artifact, "photo:1", identity); err == nil {
		t.Fatal("replaced artifact accepted")
	}
}
