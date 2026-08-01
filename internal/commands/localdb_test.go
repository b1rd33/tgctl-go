package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func sqliteJournalMode(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return strings.ToLower(mode)
}

type hookedBackfillClient struct {
	*client.FakeClient
	backfill func(context.Context, client.BackfillReq) (client.BackfillResult, error)
}

func (c *hookedBackfillClient) BackfillMessages(ctx context.Context, req client.BackfillReq) (client.BackfillResult, error) {
	if c.backfill != nil {
		return c.backfill(ctx, req)
	}
	return c.FakeClient.BackfillMessages(ctx, req)
}

func TestDatabaseSizeBytesUsesSQLiteAllocatedPages(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	dbPath := cfg.Paths.(stubPaths).db
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE wal_size_fixture(payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO wal_size_fixture(payload) VALUES (zeroblob(200000))"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(dbPath + "-wal")
	if err != nil {
		t.Fatalf("stat WAL: %v", err)
	}
	if walInfo.Size() == 0 {
		t.Fatal("WAL did not grow")
	}
	got, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if want := pageCount*pageSize + walInfo.Size(); got != want {
		t.Fatalf("size=%d, want logical main + WAL=%d", got, want)
	}
	if _, err := os.Stat(dbPath + "-shm"); err != nil {
		t.Fatalf("expected SHM coordination file: %v", err)
	}
}

func TestDatabaseSizeBytesReturnsPragmaErrors(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseSizeBytes(db); err == nil {
		t.Fatal("databaseSizeBytes on closed DB returned nil error")
	}
}

func TestBackfillRejectsReadOnlyBeforeClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "--read-only", "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 6 {
		t.Fatalf("code=%d, want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsWhenMaxAlreadyReachedBeforeRPC(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	text := "cached"
	for i := int64(1); i <= 2; i++ {
		if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: i, Date: "2026-05-08T12:00:00", Text: &text}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called despite cap: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsDatabaseAlreadyOverCapBeforeClientOrMutation(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE cap_fixture(payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cap_fixture(payload) VALUES (zeroblob(1200000))"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := captureImmutableFile(t, paths.db)
	factoryCalls := 0
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		factoryCalls++
		return &client.FakeClient{}, nil
	}

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls=%d, want 0", factoryCalls)
	}
	assertImmutableFile(t, paths.db, before)
	if _, err := os.Stat(paths.audit); !os.IsNotExist(err) {
		t.Fatalf("audit path was created: %v", err)
	}
	if _, err := os.Stat(paths.session); !os.IsNotExist(err) {
		t.Fatalf("session path was created: %v", err)
	}
}

func TestBackfillDatabaseCapStopsBeforeNextInsert(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, Date: "2026-05-08T12:00:00", Text: strings.Repeat("x", 1200000)},
		{ChatID: 1, MessageID: 11, Date: "2026-05-08T12:01:00", Text: "must be skipped"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"] != float64(0) || data["messages_skipped"] != float64(2) || data["db_cap_reached"] != true {
		t.Fatalf("data=%#v", data)
	}
	if data["db_size_bytes"].(float64) > 1024*1024 {
		t.Fatalf("db_size_bytes=%v exceeds cap", data["db_size_bytes"])
	}
	if warnings, ok := data["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("warnings=%#v, want non-null empty array", data["warnings"])
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id IN (10, 11)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("inserted fixture rows=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, cfg.Paths.(stubPaths).db); mode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", mode)
	}
}

func TestBackfillDatabaseCapRollsBackOnlyOversizedCandidate(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 20, Date: "2026-05-08T12:00:00", Text: strings.Repeat("a", 400000)},
		{ChatID: 1, MessageID: 21, Date: "2026-05-08T12:01:00", Text: strings.Repeat("b", 900000)},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"] != float64(1) || data["messages_skipped"] != float64(1) || data["db_cap_reached"] != true {
		t.Fatalf("data=%#v", data)
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if size > 1024*1024 {
		t.Fatalf("committed allocated size=%d exceeds cap", size)
	}
	var ids string
	if err := db.QueryRow("SELECT group_concat(message_id, ',') FROM tg_messages WHERE message_id IN (20,21)").Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "20" {
		t.Fatalf("committed message ids=%q, want 20", ids)
	}
}

func TestBackfillCapMediaPreservationWarningCountsOnlySkippedRowsWithPaths(t *testing.T) {
	tests := []struct {
		name          string
		rows          []client.BackfillMessage
		downloaded    int
		skippedMedia  int
		wantWarning   string
		wantPreserved int
	}{
		{
			name: "inserted media followed by skipped text has no preservation warning",
			rows: []client.BackfillMessage{
				{ChatID: 1, MessageID: 30, Date: "2026-08-01T10:00:00Z", Text: strings.Repeat("a", 400000), HasMedia: true, MediaType: "document", MediaPath: "/safe/30.bin"},
				{ChatID: 1, MessageID: 31, Date: "2026-08-01T10:01:00Z", Text: strings.Repeat("b", 900000)},
			},
			downloaded: 1,
		},
		{
			name: "skipped media row reports one",
			rows: []client.BackfillMessage{
				{ChatID: 1, MessageID: 40, Date: "2026-08-01T10:00:00Z", Text: strings.Repeat("a", 400000)},
				{ChatID: 1, MessageID: 41, Date: "2026-08-01T10:01:00Z", Text: strings.Repeat("b", 900000), HasMedia: true, MediaType: "document", MediaPath: "/safe/41.bin"},
			},
			downloaded: 1, wantPreserved: 1,
			wantWarning: "media file for 1 message skipped by a backfill cap was preserved",
		},
		{
			name: "oversized media candidate and later media row report two",
			rows: []client.BackfillMessage{
				{ChatID: 1, MessageID: 50, Date: "2026-08-01T10:00:00Z", Text: strings.Repeat("a", 400000)},
				{ChatID: 1, MessageID: 51, Date: "2026-08-01T10:01:00Z", Text: strings.Repeat("b", 900000), HasMedia: true, MediaType: "document", MediaPath: "/safe/51.bin"},
				{ChatID: 1, MessageID: 52, Date: "2026-08-01T10:02:00Z", Text: "later", HasMedia: true, MediaType: "photo", MediaPath: "/safe/52.jpg"},
			},
			downloaded: 2, wantPreserved: 2,
			wantWarning: "media files for 2 messages skipped by a backfill cap were preserved",
		},
		{
			name: "failed or unsupported media without path is not counted",
			rows: []client.BackfillMessage{
				{ChatID: 1, MessageID: 60, Date: "2026-08-01T10:00:00Z", Text: strings.Repeat("a", 400000)},
				{ChatID: 1, MessageID: 61, Date: "2026-08-01T10:01:00Z", Text: strings.Repeat("b", 900000), HasMedia: true, MediaType: "unsupported"},
			},
			skippedMedia: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			fc.BackfillResult = client.BackfillResult{
				Messages: tt.rows, MediaDownloaded: tt.downloaded, MediaSkipped: tt.skippedMedia,
			}
			out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
			if code != 0 {
				t.Fatalf("code=%d\nout:%s", code, out)
			}
			var envelope struct {
				Data struct {
					MediaDownloaded int      `json:"media_downloaded"`
					MediaSkipped    int      `json:"media_skipped"`
					Warnings        []string `json:"warnings"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Data.MediaDownloaded != tt.downloaded || envelope.Data.MediaSkipped != tt.skippedMedia || envelope.Data.Warnings == nil {
				t.Fatalf("data=%#v", envelope.Data)
			}
			preservationWarnings := 0
			for _, warning := range envelope.Data.Warnings {
				if strings.Contains(warning, "media file") && strings.Contains(warning, "backfill cap") {
					preservationWarnings++
					if warning != tt.wantWarning {
						t.Fatalf("preservation warning=%q, want %q", warning, tt.wantWarning)
					}
					if strings.Contains(warning, "/safe/") {
						t.Fatalf("warning leaked media path: %q", warning)
					}
				}
			}
			if got := preservationWarnings; got != boolInt(tt.wantPreserved > 0) {
				t.Fatalf("preservation warnings=%d, want count=%d", got, boolInt(tt.wantPreserved > 0))
			}
		})
	}
}

func TestAtomicBackfillDuplicateDoesNotConsumeMessageSlotOrCountAsCapSkippedMedia(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := insertBackfillMessage(db, client.BackfillMessage{
		ChatID: 1, MessageID: 70, Date: "2026-08-01T09:00:00Z", Text: "old",
	}); err != nil {
		t.Fatal(err)
	}
	rows := []client.BackfillMessage{
		{ChatID: 1, MessageID: 70, Date: "2026-08-01T10:00:00Z", Text: "updated duplicate"},
		{ChatID: 1, MessageID: 71, Date: "2026-08-01T10:01:00Z", Text: "new within cap", HasMedia: true, MediaType: "document", MediaPath: "/safe/71.bin"},
		{ChatID: 1, MessageID: 72, Date: "2026-08-01T10:02:00Z", Text: "new beyond cap", HasMedia: true, MediaType: "photo", MediaPath: "/safe/72.jpg"},
	}
	inserted, dbCapReached, _, finalCount, capSkippedMedia, err := insertBackfillRowsAtomic(
		context.Background(), db, 1, 2, 0, rows,
	)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 2 || dbCapReached || finalCount != 2 || capSkippedMedia != 1 {
		t.Fatalf("result inserted=%d db_cap=%v final_count=%d cap_skipped_media=%d", inserted, dbCapReached, finalCount, capSkippedMedia)
	}
	var ids, duplicateText string
	if err := db.QueryRow("SELECT group_concat(message_id, ',') FROM tg_messages WHERE chat_id=1 AND message_id IN (70,71,72)").Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT text FROM tg_messages WHERE chat_id=1 AND message_id=70").Scan(&duplicateText); err != nil {
		t.Fatal(err)
	}
	if ids != "70,71" || duplicateText != "updated duplicate" {
		t.Fatalf("ids=%q duplicate_text=%q", ids, duplicateText)
	}
}

func TestBackfillInsertsMessagesAndWarnsNearCap(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, SenderID: 99, Date: "2026-05-08T12:00:00", Text: "hi", IsOutgoing: true},
		{ChatID: 1, MessageID: 11, SenderID: 42, Date: "2026-05-08T12:01:00", Text: "there"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--throttle-seconds", "1.5", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"].(float64) != 2 || data["messages_skipped"].(float64) != 0 || len(data["warnings"].([]any)) == 0 {
		t.Fatalf("data=%#v", data)
	}
	for _, key := range []string{"db_size_bytes", "db_cap_reached", "media_downloaded", "media_skipped", "media_failed", "warnings"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("data missing %q: %#v", key, data)
		}
	}
	if data["db_cap_reached"] != false || data["media_downloaded"] != float64(0) || data["media_skipped"] != float64(0) || data["media_failed"] != float64(0) {
		t.Fatalf("unexpected cap/media counters: %#v", data)
	}
	if len(fc.Backfills) != 1 || fc.Backfills[0].Throttle != 1500*time.Millisecond {
		t.Fatalf("backfills=%#v, want throttle 1.5s", fc.Backfills)
	}
	audit, err := os.ReadFile(cfg.Paths.(stubPaths).audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"max_db_size_mb":0`, `"throttle_seconds":1.5`, `"download_media":false`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %s: %s", want, audit)
		}
	}
}

func TestBackfillDownloadMediaPassesOptionsPersistsPathsAndReturnsCounters(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	mediaPath := filepath.Join(filepath.Dir(paths.db), "media", "1", "9_report.pdf")
	fc.BackfillResult = client.BackfillResult{
		Messages: []client.BackfillMessage{{
			ChatID: 1, MessageID: 9, Date: "2026-08-01T10:00:00Z", Text: "cached despite other failures",
			HasMedia: true, MediaType: "document", MediaPath: mediaPath,
		}},
		MediaDownloaded: 1,
		MediaSkipped:    2,
		MediaFailed:     3,
		Warnings:        []string{"chat_id=1 message_id=8 media=failed code=TRANSFER"},
	}

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--download-media", "--max-media-size-mb", "7", "--overwrite-media", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Backfills) != 1 {
		t.Fatalf("backfills=%#v", fc.Backfills)
	}
	req := fc.Backfills[0]
	wantDir := filepath.Clean(filepath.Join(filepath.Dir(paths.db), "media", "1"))
	if !req.DownloadMedia || req.MediaDir != wantDir || !filepath.IsAbs(req.MediaDir) || req.MaxMediaBytes != 7*1024*1024 || !req.OverwriteMedia {
		t.Fatalf("request=%#v, want media dir %q and 7 MiB overwrite", req, wantDir)
	}
	var envelope struct {
		Data struct {
			MediaDownloaded int      `json:"media_downloaded"`
			MediaSkipped    int      `json:"media_skipped"`
			MediaFailed     int      `json:"media_failed"`
			Warnings        []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.MediaDownloaded != 1 || envelope.Data.MediaSkipped != 2 || envelope.Data.MediaFailed != 3 ||
		!reflect.DeepEqual(envelope.Data.Warnings, fc.BackfillResult.Warnings) {
		t.Fatalf("data=%#v", envelope.Data)
	}
	got, err := loadMessage(paths.db, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType == nil || *got.MediaType != "document" || got.MediaPath == nil || *got.MediaPath != mediaPath {
		t.Fatalf("cached media=%#v", got)
	}
	audit, err := os.ReadFile(paths.audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"download_media":true`, `"max_media_size_mb":7`, `"overwrite_media":true`, `"media_dir_policy":"account-chat"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %s: %s", want, audit)
		}
	}
}

func TestBackfillFatalErrorAfterDownloadedPartialReturnsCommittedRecovery(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	path := filepath.Join(filepath.Dir(cfg.Paths.(stubPaths).db), "media", "1", "42_9_document_700_asset.bin")
	fc.BackfillResult = client.BackfillResult{
		Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 9, Date: "2026-08-01T10:00:00Z"}},
		MediaOutcomes: []client.BackfillMediaOutcome{{
			ChatID: 1, MessageID: 9, MediaIdentity: "document:700", Status: client.BackfillMediaDownloaded,
			MediaType: "document", MediaPath: path, Bytes: 4,
		}},
	}
	fc.BackfillErr = errors.New("page 2 failed session=/secret access_hash=123")
	out, code := runRoot(t, cfg, "backfill", "1", "--download-media", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"artifact_count":1`) || !strings.Contains(out, `"media_path":"`+path+`"`) {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if strings.Contains(out, "access_hash") || strings.Contains(out, "/secret") {
		t.Fatalf("recovery output leaked raw error: %s", out)
	}
	if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partial fatal result was inserted: %v", err)
	}
}

func TestBackfillFatalErrorAfterPreexistingSkipIsNotCommitted(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillResult = client.BackfillResult{MediaOutcomes: []client.BackfillMediaOutcome{{
		ChatID: 1, MessageID: 9, MediaIdentity: "document:700", Status: client.BackfillMediaSkipped,
		MediaType: "document", MediaPath: filepath.Join(t.TempDir(), "existing.bin"), Bytes: 4,
	}}}
	fc.BackfillErr = errors.New("page 2 unavailable")
	out, code := runRoot(t, cfg, "backfill", "1", "--download-media", "--allow-write", "--json")
	if code != 1 || strings.Contains(out, `"committed":true`) {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
}

func TestBackfillClientCloseFailureAfterDownloadIsCommitted(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillResult = client.BackfillResult{MediaOutcomes: []client.BackfillMediaOutcome{{
		ChatID: 1, MessageID: 9, MediaIdentity: "document:700", Status: client.BackfillMediaDownloaded,
		MediaType: "document", MediaPath: filepath.Join(filepath.Dir(cfg.Paths.(stubPaths).db), "media", "1", "asset.bin"), Bytes: 4,
	}}}
	fc.CloseErr = errors.New("close failed")
	out, code := runRoot(t, cfg, "backfill", "1", "--download-media", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"artifact_count":1`) {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
}

func TestBackfillDurableAuditFailureAfterMediaAndDBSuccessReturnsRecovery(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	blocker := filepath.Join(t.TempDir(), "audit-blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths.audit = filepath.Join(blocker, "audit.log")
	cfg.Paths = paths
	fc.BackfillResult = client.BackfillResult{
		Messages:      []client.BackfillMessage{{ChatID: 1, MessageID: 9, Date: "2026-08-01T10:00:00Z", HasMedia: true, MediaType: "document", MediaPath: filepath.Join(t.TempDir(), "asset.bin"), MediaIdentity: "document:700", MediaDisposition: client.BackfillMediaDownloaded}},
		MediaOutcomes: []client.BackfillMediaOutcome{{ChatID: 1, MessageID: 9, MediaIdentity: "document:700", Status: client.BackfillMediaDownloaded, MediaType: "document", MediaPath: filepath.Join(t.TempDir(), "asset.bin"), Bytes: 4}},
		Warnings:      []string{},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--download-media", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"artifact_count":1`) {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
}

func TestBackfillMediaFailurePreservesExistingCachedMediaPath(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	oldText, oldType, oldPath, oldIdentity := "old", "document", "/existing/9_report.pdf", "document:700"
	if err := store.InsertMessage(db, store.Message{
		ChatID: 1, MessageID: 9, Date: "2026-08-01T09:00:00Z", Text: &oldText,
		HasMedia: true, MediaType: &oldType, MediaPath: &oldPath, MediaIdentity: &oldIdentity,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fc.BackfillResult = client.BackfillResult{
		Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 9, Date: "2026-08-01T10:00:00Z", Text: "new text", HasMedia: true,
			MediaIdentity: oldIdentity, MediaDisposition: client.BackfillMediaFailed}},
		MediaFailed: 1,
		Warnings:    []string{"chat_id=1 message_id=9 media=failed code=TRANSFER"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--download-media", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	got, err := loadMessage(paths.db, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text == nil || *got.Text != "new text" || got.MediaType == nil || *got.MediaType != oldType || got.MediaPath == nil || *got.MediaPath != oldPath {
		t.Fatalf("cached row=%#v, want updated text and preserved media", got)
	}
}

func TestBackfillExplicitMediaProvenanceClearsOrPreservesStalePaths(t *testing.T) {
	tests := []struct {
		name     string
		seedID   *string
		row      client.BackfillMessage
		wantPath bool
		wantID   string
	}{
		{name: "same identity failure preserves", seedID: ptrString("document:1"), row: client.BackfillMessage{HasMedia: true, MediaIdentity: "document:1", MediaDisposition: client.BackfillMediaFailed}, wantPath: true, wantID: "document:1"},
		{name: "edited document failure clears", seedID: ptrString("document:1"), row: client.BackfillMessage{HasMedia: true, MediaIdentity: "document:2", MediaDisposition: client.BackfillMediaFailed}},
		{name: "media removed clears", seedID: ptrString("document:1"), row: client.BackfillMessage{HasMedia: false, MediaDisposition: client.BackfillMediaNone}},
		{name: "unsupported replacement clears", seedID: ptrString("document:1"), row: client.BackfillMessage{HasMedia: true, MediaDisposition: client.BackfillMediaUnsupported}},
		{name: "legacy identity failure fails closed", row: client.BackfillMessage{HasMedia: true, MediaIdentity: "document:1", MediaDisposition: client.BackfillMediaFailed}},
		{name: "successful replacement sets new", seedID: ptrString("document:1"), row: client.BackfillMessage{HasMedia: true, MediaType: "document", MediaPath: "/new/b.bin", MediaIdentity: "document:2", MediaDisposition: client.BackfillMediaDownloaded}, wantPath: true, wantID: "document:2"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, _ := setupWriteEnv(t)
			db, err := store.Connect(cfg.Paths.(stubPaths).db)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			oldType, oldPath := "document", "/old/a.bin"
			id := int64(800 + i)
			if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: id, Date: "2026-08-01T09:00:00Z", HasMedia: true, MediaType: &oldType, MediaPath: &oldPath, MediaIdentity: tt.seedID}); err != nil {
				t.Fatal(err)
			}
			row := tt.row
			row.ChatID, row.MessageID, row.Date = 1, id, "2026-08-01T10:00:00Z"
			if err := insertBackfillMessage(db, row); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetOne(db, 1, id, false)
			if err != nil {
				t.Fatal(err)
			}
			if (got.MediaPath != nil) != tt.wantPath {
				t.Fatalf("row=%#v", got)
			}
			if tt.wantID == "" {
				if got.MediaIdentity != nil {
					t.Fatalf("media identity=%v, want nil", *got.MediaIdentity)
				}
			} else if got.MediaIdentity == nil || *got.MediaIdentity != tt.wantID {
				t.Fatalf("row=%#v", got)
			}
		})
	}
}

func ptrString(v string) *string { return &v }

func TestBackfillWarningUsesAuthoritativeCountAfterConcurrentRPCWrite(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 3; id++ {
		row := client.BackfillMessage{ChatID: 1, MessageID: id, Date: "2026-08-01T10:00:00Z", Text: "seed"}
		if err := insertBackfillMessage(db, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	hooked := &hookedBackfillClient{FakeClient: &client.FakeClient{Me: client.User{ID: 99}}}
	hooked.backfill = func(context.Context, client.BackfillReq) (client.BackfillResult, error) {
		concurrentDB, err := store.Connect(path)
		if err != nil {
			return client.BackfillResult{}, err
		}
		defer concurrentDB.Close()
		if err := insertBackfillMessage(concurrentDB, client.BackfillMessage{
			ChatID: 1, MessageID: 4, Date: "2026-08-01T10:01:00Z", Text: "concurrent",
		}); err != nil {
			return client.BackfillResult{}, err
		}
		return client.BackfillResult{Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 5, Date: "2026-08-01T10:02:00Z", Text: "rpc"}}}, nil
	}
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) { return hooked, nil }

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "6", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var envelope struct {
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Warnings) != 1 || !strings.Contains(envelope.Data.Warnings[0], "cached 5 of --max-messages 6") {
		t.Fatalf("warnings=%#v, want authoritative final count 5", envelope.Data.Warnings)
	}
}

func TestBackfillRejectsNegativeLimitsAndThrottle(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "messages", args: []string{"--max-messages", "-1"}},
		{name: "messages too large", args: []string{"--max-messages", "10001"}},
		{name: "database size", args: []string{"--max-db-size-mb", "-1"}},
		{name: "media size", args: []string{"--max-media-size-mb", "-1"}},
		{name: "throttle", args: []string{"--throttle-seconds", "-0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			args := append([]string{"backfill", "1", "--allow-write", "--json"}, tc.args...)
			out, code := runRoot(t, cfg, args...)
			if code != 2 {
				t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
			}
			if len(fc.Backfills) != 0 {
				t.Fatalf("client called: %#v", fc.Backfills)
			}
		})
	}
}

func TestBackfillMediaSizeValidationRunsBeforeAccountPathsOrClient(t *testing.T) {
	paths := &downloadGatePaths{root: t.TempDir()}
	factoryCalls := 0
	cfg := CommandsConfig{
		Paths: paths,
		ClientFactory: func(context.Context, string, string) (client.Client, error) {
			factoryCalls++
			return &client.FakeClient{}, nil
		},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-media-size-mb", "-1", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if paths.currentCalls != 0 || paths.pathCalls != 0 || factoryCalls != 0 {
		t.Fatalf("preflight side effects current=%d paths=%d factory=%d", paths.currentCalls, paths.pathCalls, factoryCalls)
	}
}

func TestBackfillCommandHelpIncludesMediaFlags(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	root := NewRootCommand()
	registerLocalDBCommands(root, cfg)
	cmd, _, err := root.Find([]string{"backfill"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"download-media": "false", "max-media-size-mb": "100", "overwrite-media": "false",
		"max-db-size-mb": "0", "max-messages": "100", "throttle-seconds": "0",
	}
	for name, def := range want {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || flag.DefValue != def {
			t.Fatalf("--%s = %#v, want default %q", name, flag, def)
		}
	}
}

func TestBackfillThrottleDurationBoundaryDoesNotOverflow(t *testing.T) {
	maxSafeSeconds := math.Nextafter(float64(math.MaxInt64)/float64(time.Second), 0)
	d, err := backfillThrottleDuration(maxSafeSeconds)
	if err != nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	if d <= 0 {
		t.Fatalf("boundary duration=%v, want positive", d)
	}
	if _, err := backfillThrottleDuration(math.Nextafter(maxSafeSeconds, math.Inf(1))); err == nil {
		t.Fatal("overflowing next representable duration was accepted")
	} else {
		var badArgs *safety.BadArgs
		if !errors.As(err, &badArgs) {
			t.Fatalf("overflow error=%T, want *safety.BadArgs", err)
		}
	}
}

func TestBackfillPathLocksAreNormalizedAndPathScoped(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.sqlite")
	equivalentA := filepath.Join(dir, "nested", "..", "a.sqlite")
	pathB := filepath.Join(dir, "b.sqlite")

	unlockA, err := lockBackfillDBPath(pathA)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()

	bAcquired := make(chan func(), 1)
	go func() {
		unlock, lockErr := lockBackfillDBPath(pathB)
		if lockErr != nil {
			bAcquired <- nil
			return
		}
		bAcquired <- unlock
	}()
	select {
	case unlockB := <-bAcquired:
		if unlockB == nil {
			t.Fatal("locking unrelated DB failed")
		}
		unlockB()
	case <-time.After(time.Second):
		t.Fatal("unrelated database path was serialized")
	}

	sameAcquired := make(chan func(), 1)
	go func() {
		unlock, _ := lockBackfillDBPath(equivalentA)
		sameAcquired <- unlock
	}()
	select {
	case unlock := <-sameAcquired:
		if unlock != nil {
			unlock()
		}
		t.Fatal("equivalent database path was not serialized")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	unlockA = func() {}
	select {
	case unlock := <-sameAcquired:
		if unlock == nil {
			t.Fatal("equivalent database path lock failed")
		}
		unlock()
	case <-time.After(time.Second):
		t.Fatal("equivalent database path did not acquire after release")
	}
}

func TestCappedBackfillPrecommitFailureRollsBackAndRestoresWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantErr := errors.New("injected before commit")
	_, _, _, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 300, Date: "2026-08-01T12:00:00Z", Text: "not committed"}},
		backfillAtomicHooks{beforeCommit: func() error { return wantErr }},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want injected error", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=300").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("committed rows=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after precommit failure, want wal", mode)
	}
}

func TestCappedBackfillActiveReaderFailsBeforeWriteAndKeepsWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	readerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer readerDB.Close()
	reader, err := readerDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ExecContext(context.Background(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer reader.ExecContext(context.Background(), "ROLLBACK")
	var seeded int
	if err := reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM tg_chats").Scan(&seeded); err != nil {
		t.Fatal(err)
	}

	writerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writerDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, _, _, _, err = insertBackfillRowsAtomic(ctx, writerDB, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 301, Date: "2026-08-01T12:00:00Z", Text: "blocked"}},
	)
	if err == nil {
		t.Fatal("capped write succeeded with an active reader")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("contention failure was not time-bounded: %v", time.Since(started))
	}
	var count int
	if err := reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM tg_messages WHERE message_id=301").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("blocked row count=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after contention, want wal", mode)
	}
}

func TestCappedBackfillRacingReaderCannotInterruptWALRestore(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	committed := make(chan struct{})
	attempting := make(chan struct{})
	readerDone := make(chan error, 1)
	readerClosed := make(chan struct{})
	go func() {
		defer close(readerClosed)
		<-committed
		readerDB, openErr := sql.Open("sqlite", path)
		if openErr != nil {
			readerDone <- openErr
			return
		}
		readerDB.SetMaxOpenConns(1)
		if _, busyErr := readerDB.Exec("PRAGMA busy_timeout=1000"); busyErr != nil {
			readerDone <- errors.Join(busyErr, readerDB.Close())
			return
		}
		close(attempting)
		var count int
		queryErr := readerDB.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=302").Scan(&count)
		readerDone <- errors.Join(queryErr, readerDB.Close())
	}()
	_, _, _, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 302, Date: "2026-08-01T12:00:00Z", Text: "committed"}},
		backfillAtomicHooks{afterCommit: func() error {
			close(committed)
			<-attempting
			select {
			case err := <-readerDone:
				return fmt.Errorf("reader entered before WAL restoration: %w", err)
			case <-time.After(50 * time.Millisecond):
				return nil
			}
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readerDone:
		if err != nil {
			t.Fatalf("reader failed after restoration: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("racing reader remained blocked after restoration")
	}
	<-readerClosed
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after success, want wal", mode)
	}
}

func TestCappedBackfillPostcommitFailureIsClassifiedAndRestoresWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantErr := errors.New("injected after commit")
	_, _, _, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 303, Date: "2026-08-01T12:00:00Z", Text: "durable"}},
		backfillAtomicHooks{afterCommit: func() error { return wantErr }},
	)
	var committed *safety.CommittedWrite
	if !errors.As(err, &committed) || !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want classified committed error wrapping injected failure", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=303").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable row count=%d, want 1", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after postcommit error, want wal", mode)
	}
}

func TestBackfillFullCommandProcessHelper(t *testing.T) {
	if os.Getenv("TGCTL_BACKFILL_COMMAND_HELPER") != "1" {
		return
	}
	path := os.Getenv("TGCTL_BACKFILL_DB")
	id, err := strconv.ParseInt(os.Getenv("TGCTL_BACKFILL_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ready := os.Getenv("TGCTL_BACKFILL_READY")
	gate := os.Getenv("TGCTL_BACKFILL_GATE")
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for full-command subprocess gate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fc := &client.FakeClient{
		Me: client.User{ID: 99},
		BackfillRows: []client.BackfillMessage{{
			ChatID: 1, MessageID: id, Date: "2026-08-01T12:00:00Z", Text: strings.Repeat("x", 650000),
		}},
	}
	cfg := CommandsConfig{
		Paths: stubPaths{
			db: path, session: os.Getenv("TGCTL_BACKFILL_SESSION"), audit: os.Getenv("TGCTL_BACKFILL_AUDIT"),
		},
		ClientFactory: func(context.Context, string, string) (client.Client, error) { return fc, nil },
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("non-JSON command failure code=%d: %v\nout:%s", code, err, out)
		}
		message := strings.ToLower(envelope.Error.Message)
		if code != 1 || envelope.OK || envelope.Error.Code != "GENERIC" ||
			(!strings.Contains(message, "locked") && !strings.Contains(message, "busy")) {
			t.Fatalf("command failed unclearly code=%d envelope=%#v\nout:%s", code, envelope, out)
		}
	}
	if err := os.WriteFile(os.Getenv("TGCTL_BACKFILL_RESULT"), []byte(strconv.Itoa(code)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("command code=%d output=%s", code, strings.TrimSpace(out))
}

func TestCappedBackfillFullCommandCrossProcessContention(t *testing.T) {
	cfg, _, dir := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	gate := filepath.Join(dir, "full-command-process-gate")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type child struct {
		cmd        *exec.Cmd
		out        bytes.Buffer
		resultPath string
	}
	children := make([]*child, 2)
	for i := range children {
		ready := filepath.Join(dir, fmt.Sprintf("full-command-ready-%d", i))
		child := &child{resultPath: filepath.Join(dir, fmt.Sprintf("child-%d.result", i))}
		child.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackfillFullCommandProcessHelper$", "-test.v")
		child.cmd.Env = append(os.Environ(),
			"TGCTL_BACKFILL_COMMAND_HELPER=1", "TGCTL_BACKFILL_DB="+paths.db,
			"TGCTL_BACKFILL_ID="+strconv.Itoa(500+i), "TGCTL_BACKFILL_READY="+ready,
			"TGCTL_BACKFILL_GATE="+gate, "TGCTL_BACKFILL_SESSION="+filepath.Join(dir, fmt.Sprintf("child-%d.session", i)),
			"TGCTL_BACKFILL_AUDIT="+filepath.Join(dir, fmt.Sprintf("child-%d.audit", i)),
			"TGCTL_BACKFILL_RESULT="+child.resultPath,
		)
		child.cmd.Stdout = &child.out
		child.cmd.Stderr = &child.out
		if err := child.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children[i] = child
	}
	deadline := time.Now().Add(5 * time.Second)
	for i := range children {
		ready := filepath.Join(dir, fmt.Sprintf("full-command-ready-%d", i))
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for child %d readiness", i)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	successes := 0
	for i, child := range children {
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("full-command child %d failed: %v\n%s", i, err, child.out.String())
		}
		result, err := os.ReadFile(child.resultPath)
		if err != nil {
			t.Fatalf("read child %d result: %v\n%s", i, err, child.out.String())
		}
		code, err := strconv.Atoi(string(result))
		if err != nil {
			t.Fatalf("parse child %d result %q: %v", i, result, err)
		}
		if code == 0 {
			successes++
		}
		t.Logf("child %d:\n%s", i, child.out.String())
	}
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id IN (500,501)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if count < 0 || count > 1 || size > 1024*1024 {
		t.Fatalf("full-command cross-process count=%d size=%d, want at most one row within cap", count, size)
	}
	if count == 0 && successes != 0 {
		t.Fatalf("count=0 with %d successful children, want both clean busy/locked failures", successes)
	}
	if count == 1 && successes == 0 {
		t.Fatal("count=1 without a successful child outcome")
	}
	if mode := sqliteJournalMode(t, paths.db); mode != "wal" {
		t.Fatalf("journal_mode=%q after full-command subprocess contention, want wal", mode)
	}
}

func setupLegacyBackfillEnv(t *testing.T) (CommandsConfig, *client.FakeClient, stubPaths) {
	t.Helper()
	dir := t.TempDir()
	paths := stubPaths{db: filepath.Join(dir, "telegram.sqlite"), session: filepath.Join(dir, "tg.session"), audit: filepath.Join(dir, "audit.log")}
	db, err := sql.Open("sqlite", paths.db)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `
		CREATE TABLE tg_chats (chat_id INTEGER PRIMARY KEY, type TEXT, title TEXT, username TEXT);
		CREATE TABLE tg_messages (
			chat_id INTEGER, message_id INTEGER, sender_id INTEGER, date TEXT, text TEXT,
			is_outgoing INTEGER, reply_to_msg_id INTEGER, has_media INTEGER, media_type TEXT,
			raw_json TEXT, PRIMARY KEY(chat_id, message_id));
		CREATE INDEX idx_messages_chat_date ON tg_messages(chat_id, date DESC);
		CREATE INDEX idx_messages_date ON tg_messages(date DESC);
		INSERT INTO tg_chats(chat_id, type, title) VALUES (1, 'group', 'Legacy');`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fc := &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 1, MessageID: 7, Date: "2026-08-01T10:00:00Z", Text: "migrated"}}}
	cfg := CommandsConfig{
		Paths:                 paths,
		ClientFactory:         func(context.Context, string, string) (client.Client, error) { return fc, nil },
		ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) { return fc, nil },
	}
	return cfg, fc, paths
}

func legacyHasColumn(t *testing.T, path, column string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA table_info(tg_messages)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	return false
}

func TestBackfillMigratesLegacySchemaBeforeCountAndInsert(t *testing.T) {
	cfg, fc, paths := setupLegacyBackfillEnv(t)
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Backfills) != 1 || !legacyHasColumn(t, paths.db, "deleted") || !legacyHasColumn(t, paths.db, "media_path") {
		t.Fatalf("migration/backfill missing: calls=%#v deleted=%v media_path=%v", fc.Backfills, legacyHasColumn(t, paths.db, "deleted"), legacyHasColumn(t, paths.db, "media_path"))
	}
	db, err := store.ConnectReadonly(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=7 AND deleted=0").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("inserted rows=%d, want 1", count)
	}
}

func TestBackfillSafetyFailureDoesNotMigrateLegacySchema(t *testing.T) {
	cfg, fc, paths := setupLegacyBackfillEnv(t)
	before := captureImmutableFile(t, paths.db)
	out, code := runRoot(t, cfg, "--read-only", "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 6 {
		t.Fatalf("code=%d, want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 || legacyHasColumn(t, paths.db, "deleted") {
		t.Fatalf("safety failure mutated/called client: calls=%#v", fc.Backfills)
	}
	assertImmutableFile(t, paths.db, before)
}

type concurrentBackfillResult struct {
	code int
	out  string
}

func runConcurrentBackfills(t *testing.T, cfg CommandsConfig, rowText string, maxMessages, maxDBSizeMB int) []concurrentBackfillResult {
	t.Helper()
	var calls int32
	ready := make(chan struct{})
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			return nil, errors.New("timed out waiting for concurrent backfill client")
		}
		return &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 1, MessageID: int64(100 + n), Date: "2026-08-01T10:00:00Z", Text: rowText}}}, nil
	}
	results := make(chan concurrentBackfillResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", fmt.Sprint(maxMessages), "--max-db-size-mb", fmt.Sprint(maxDBSizeMB), "--allow-write", "--json")
			results <- concurrentBackfillResult{code: code, out: out}
		}()
	}
	return []concurrentBackfillResult{<-results, <-results}
}

func TestConcurrentBackfillsAtomicallyEnforceMessageLimit(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	for _, result := range runConcurrentBackfills(t, cfg, "small", 1, 0) {
		if result.code != 0 {
			t.Fatalf("code=%d, want 0\nout:%s", result.code, result.out)
		}
	}
	db, err := store.ConnectReadonly(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message count=%d exceeds max 1", count)
	}
}

func TestConcurrentBackfillMessageCapWarnsOnlyForTheMediaRowActuallySkipped(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	var calls int32
	ready := make(chan struct{})
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			return nil, errors.New("timed out waiting for concurrent media backfill client")
		}
		id := int64(200 + n)
		return &client.FakeClient{BackfillResult: client.BackfillResult{
			Messages: []client.BackfillMessage{{
				ChatID: 1, MessageID: id, Date: "2026-08-01T10:00:00Z", Text: "concurrent",
				HasMedia: true, MediaType: "document", MediaPath: fmt.Sprintf("/safe/%d.bin", id),
			}},
			MediaDownloaded: 1,
		}}, nil
	}
	type concurrentEnvelope struct {
		code int
		out  string
	}
	results := make(chan concurrentEnvelope, 2)
	for i := 0; i < 2; i++ {
		go func() {
			out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "1", "--download-media", "--allow-write", "--json")
			results <- concurrentEnvelope{code: code, out: out}
		}()
	}
	totalInserted, totalSkipped, preservationWarnings := 0, 0, 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.code != 0 {
			t.Fatalf("code=%d\nout:%s", result.code, result.out)
		}
		var envelope struct {
			Data struct {
				MessagesInserted int      `json:"messages_inserted"`
				MessagesSkipped  int      `json:"messages_skipped"`
				MediaDownloaded  int      `json:"media_downloaded"`
				Warnings         []string `json:"warnings"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(result.out), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.MediaDownloaded != 1 || envelope.Data.Warnings == nil {
			t.Fatalf("data=%#v", envelope.Data)
		}
		totalInserted += envelope.Data.MessagesInserted
		totalSkipped += envelope.Data.MessagesSkipped
		for _, warning := range envelope.Data.Warnings {
			if warning == "media file for 1 message skipped by a backfill cap was preserved" {
				preservationWarnings++
			}
		}
	}
	if totalInserted != 1 || totalSkipped != 1 || preservationWarnings != 1 {
		t.Fatalf("totals inserted=%d skipped=%d preservation_warnings=%d", totalInserted, totalSkipped, preservationWarnings)
	}
}

func TestConcurrentBackfillsAtomicallyEnforceDatabaseCap(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	for _, result := range runConcurrentBackfills(t, cfg, strings.Repeat("x", 650000), 10, 1) {
		if result.code != 0 {
			t.Fatalf("code=%d, want 0\nout:%s", result.code, result.out)
		}
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if size > 1024*1024 {
		t.Fatalf("allocated size=%d exceeds cap", size)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message count=%d, want exactly one fitting row", count)
	}
}

func TestDiscoverUpsertsChats(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Dialogs = []client.ChatInfo{{ID: 2, Type: "channel", Title: "Ops", Username: "ops"}}
	out, code := runRoot(t, cfg, "discover", "--limit", "10", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Discoveries) != 1 {
		t.Fatalf("discoveries=%#v", fc.Discoveries)
	}
	db, _ := store.Connect(cfg.Paths.(stubPaths).db)
	defer db.Close()
	var title string
	if err := db.QueryRow("SELECT title FROM tg_chats WHERE chat_id=2").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Ops" {
		t.Fatalf("title=%q", title)
	}
}

func TestSyncContactsUpsertsContacts(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Contacts = []client.ContactInfo{{UserID: 42, Phone: "123", FirstName: "Ada", Username: "ada", IsMutual: true}}
	out, code := runRoot(t, cfg, "sync-contacts", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.ContactSyncs) != 1 {
		t.Fatalf("sync calls=%#v", fc.ContactSyncs)
	}
	if !strings.Contains(out, `"synced":1`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
