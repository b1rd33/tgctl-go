package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func seedAlbumRows(t *testing.T, dbPath string, rows ...store.Message) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	for _, row := range rows {
		if err := store.InsertMessage(db, row); err != nil {
			t.Fatalf("InsertMessage(%d): %v", row.MessageID, err)
		}
	}
}

func albumArtifact(t *testing.T, root string, messageID int64, mediaType string) client.DownloadMediaResp {
	t.Helper()
	name := filepath.Join(root, string(rune('a'+messageID%26))+".jpg")
	if err := os.WriteFile(name, []byte("media"), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	identity, artifact, err := media.CaptureArtifactIdentity(root, name)
	if err != nil {
		t.Fatalf("CaptureArtifactIdentity: %v", err)
	}
	return client.DownloadMediaResp{ChatID: 1, MessageID: messageID, MediaType: mediaType, MIMEType: "image/jpeg", Filename: artifact.Filename, Path: artifact.Path, Bytes: artifact.Size, ArtifactIdentity: identity}
}

func TestDownloadAlbumDryRunGroupsRowsWithoutClientOrMutation(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	caption := "private caption"
	mediaType := "photo"
	seedAlbumRows(t, filepath.Join(dir, "telegram.sqlite"),
		store.Message{ChatID: 1, MessageID: 103, GroupedID: 700, Date: "2026-08-01T00:00:03Z", HasMedia: true, MediaType: &mediaType, Text: &caption},
		store.Message{ChatID: 1, MessageID: 101, GroupedID: 700, Date: "2026-08-01T00:00:01Z", HasMedia: true, MediaType: &mediaType},
	)
	out, code := runRoot(t, cfg, "download-album", "1", "--grouped-id", "700", "--allow-write", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout=%s", code, out)
	}
	if len(fake.Downloads) != 0 || len(fake.Calls) != 0 {
		t.Fatalf("client called during dry-run: calls=%v downloads=%v", fake.Calls, fake.Downloads)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run audit state err=%v", err)
	}
	var envelope struct {
		Data downloadAlbumResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode: %v\nout=%s", err, out)
	}
	if got := []int64{envelope.Data.Items[0].MessageID, envelope.Data.Items[1].MessageID}; !reflect.DeepEqual(got, []int64{101, 103}) {
		t.Fatalf("stable order = %v", got)
	}
	if envelope.Data.Items[0].Status != "planned" || envelope.Data.Items[0].GroupedID != 700 {
		t.Fatalf("items = %#v", envelope.Data.Items)
	}
	if containsAny(out, caption, filepath.Join(dir, "telegram.sqlite")) {
		t.Fatalf("dry-run leaked caption/path: %s", out)
	}
}

func TestDownloadAlbumPersistsEachPathAndContinuesPartialFailures(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	mediaType := "photo"
	seedAlbumRows(t, filepath.Join(dir, "telegram.sqlite"),
		store.Message{ChatID: 1, MessageID: 101, GroupedID: 800, Date: "2026-08-01T00:00:01Z", HasMedia: true, MediaType: &mediaType},
		store.Message{ChatID: 1, MessageID: 102, GroupedID: 800, Date: "2026-08-01T00:00:02Z", HasMedia: true, MediaType: &mediaType},
		store.Message{ChatID: 1, MessageID: 103, GroupedID: 800, Date: "2026-08-01T00:00:03Z", HasMedia: false},
	)
	output := filepath.Join(dir, "album-out")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	first := albumArtifact(t, output, 101, "photo")
	second := albumArtifact(t, output, 102, "photo")
	// The fake returns a pre-existing artifact as if the downloader committed
	// it; this still exercises response validation and cache persistence.
	fake.DownloadResponses = []client.DownloadMediaResp{first, second}
	fake.DownloadErrors = []error{errors.New("transfer unavailable"), nil}
	out, code := runRoot(t, cfg, "download-album", "1", "--grouped-id", "800", "--output", output, "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout=%s", code, out)
	}
	if got := []int64{fake.Downloads[0].MessageID, fake.Downloads[1].MessageID}; !reflect.DeepEqual(got, []int64{101, 102}) {
		t.Fatalf("download order = %v", got)
	}
	var envelope struct {
		Data downloadAlbumResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Failed != 1 || !envelope.Data.Partial || envelope.Data.Items[0].Status != "failed" || envelope.Data.Items[1].Status != "downloaded" || envelope.Data.Items[2].Status != "missing_media" {
		t.Fatalf("result = %#v", envelope.Data)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := store.GetOne(db, 1, 102, false)
	if err != nil {
		t.Fatal(err)
	}
	if row.MediaPath == nil || *row.MediaPath != second.Path {
		t.Fatalf("cached path = %#v", row.MediaPath)
	}
}

func TestDownloadAlbumOverwriteBypassesCachedArtifactSkip(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "album-overwrite")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := albumArtifact(t, output, 101, "photo")
	artifact2 := albumArtifact(t, output, 102, "photo")
	mediaType := "photo"
	identity := "photo:101"
	seedAlbumRows(t, filepath.Join(dir, "telegram.sqlite"),
		store.Message{ChatID: 1, MessageID: 101, GroupedID: 801, Date: "2026-08-01T00:00:01Z", HasMedia: true, MediaType: &mediaType, MediaPath: &artifact.Path, MediaIdentity: &identity},
		store.Message{ChatID: 1, MessageID: 102, GroupedID: 801, Date: "2026-08-01T00:00:02Z", HasMedia: true, MediaType: &mediaType},
	)
	fake.DownloadResponses = []client.DownloadMediaResp{artifact, artifact2}
	out, code := runRoot(t, cfg, "download-album", "1", "--grouped-id", "801", "--output", output, "--overwrite", "--allow-write", "--json")
	if code != 0 || len(fake.Downloads) != 2 {
		t.Fatalf("code=%d downloads=%d out=%s", code, len(fake.Downloads), out)
	}
}

func TestDownloadAlbumRejectsAmbiguousAnchorBeforePathsOrClient(t *testing.T) {
	paths := &downloadGatePaths{root: t.TempDir()}
	calls := 0
	cfg := CommandsConfig{Paths: paths, ClientFactory: func(context.Context, string, string) (client.Client, error) {
		calls++
		return &client.FakeClient{}, nil
	}}
	out, code := runRoot(t, cfg, "download-album", "1", "9", "--grouped-id", "11", "--allow-write", "--json")
	if code != 2 || calls != 0 || paths.currentCalls != 0 || paths.pathCalls != 0 {
		t.Fatalf("code=%d calls=%d paths=%d/%d out=%s", code, calls, paths.currentCalls, paths.pathCalls, out)
	}
}

func TestDownloadAlbumRejectsUngroupedAnchorWithStableError(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	mediaType := "photo"
	seedAlbumRows(t, filepath.Join(dir, "telegram.sqlite"), store.Message{
		ChatID: 1, MessageID: 9, Date: "2026-08-01T00:00:00Z", HasMedia: true, MediaType: &mediaType,
	})
	out, code := runRoot(t, cfg, "download-album", "1", "9", "--allow-write", "--json")
	if code != 2 || !strings.Contains(out, "not-an-album") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(fake.Downloads) != 0 {
		t.Fatalf("client called for ungrouped anchor: %#v", fake.Downloads)
	}
}

func TestBackfillInsertPersistsGroupedID(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "messages.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := insertBackfillMessage(db, client.BackfillMessage{ChatID: 1, MessageID: 44, Date: "2026-08-01T00:00:00Z", HasMedia: true, GroupedID: 744}); err != nil {
		t.Fatalf("insertBackfillMessage: %v", err)
	}
	row, err := store.GetOne(db, 1, 44, false)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if row.GroupedID != 744 {
		t.Fatalf("grouped id = %d, want 744", row.GroupedID)
	}
}
