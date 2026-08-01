package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpdateMessageMediaPathUpdatesExistingCompositeIdentity(t *testing.T) {
	db := setupMessages(t)
	otherType, otherPath := "document", "/tmp/other.bin"
	if err := InsertMessage(db, Message{ChatID: 2, MessageID: 12, Date: "2026-05-03T12:00:00", HasMedia: true, MediaType: &otherType, MediaPath: &otherPath}); err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := UpdateMessageMediaPath(db, 1, 12, "photo", wantPath); err != nil {
		t.Fatalf("UpdateMessageMediaPath: %v", err)
	}
	got, err := GetOne(db, 1, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasMedia || got.MediaType == nil || *got.MediaType != "photo" || got.MediaPath == nil || *got.MediaPath != wantPath {
		t.Fatalf("updated message = %#v", got)
	}
	other, err := GetOne(db, 2, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	if other.MediaPath == nil || *other.MediaPath != otherPath {
		t.Fatalf("other composite identity changed: %#v", other)
	}
}

func TestMediaPathWritersClearUnknownTelegramIdentity(t *testing.T) {
	writers := []struct {
		name  string
		write func(*sql.DB, int64) error
	}{
		{name: "record upload", write: func(db *sql.DB, id int64) error {
			return RecordUploadedMedia(db, 1, id, "caption", "document", "/new/upload.bin")
		}},
		{name: "update path", write: func(db *sql.DB, id int64) error {
			return UpdateMessageMediaPath(db, 1, id, "document", "/new/download.bin")
		}},
		{name: "store existing", write: func(db *sql.DB, id int64) error {
			return StoreMessageMediaPath(db, 1, id, time.Now(), "document", "/new/store.bin")
		}},
	}
	for i, tt := range writers {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMessages(t)
			identity, oldType, oldPath := "document:A", "document", "/old/a.bin"
			id := int64(500 + i)
			if err := InsertMessage(db, Message{ChatID: 1, MessageID: id, Date: "2026-08-01T00:00:00Z", HasMedia: true, MediaType: &oldType, MediaPath: &oldPath, MediaIdentity: &identity}); err != nil {
				t.Fatal(err)
			}
			if err := tt.write(db, id); err != nil {
				t.Fatal(err)
			}
			got, err := GetOne(db, 1, id, false)
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaIdentity != nil {
				t.Fatalf("media identity=%q, want NULL", *got.MediaIdentity)
			}
		})
	}
}

func TestStoreMessageMediaPathInsertHasNoUnknownIdentity(t *testing.T) {
	db := setupMessages(t)
	if err := StoreMessageMediaPath(db, 1, 777, time.Now(), "document", "/new/insert.bin"); err != nil {
		t.Fatal(err)
	}
	got, err := GetOne(db, 1, 777, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaIdentity != nil {
		t.Fatalf("media identity=%q, want NULL", *got.MediaIdentity)
	}
}

func TestUpdateMessageMediaPathMissingReturnsNoRows(t *testing.T) {
	db := setupMessages(t)
	err := UpdateMessageMediaPath(db, 99, 404, "photo", "/tmp/missing.jpg")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error = %v, want sql.ErrNoRows", err)
	}
}

func TestStoreMessageMediaPathInsertsMinimalMissingRow(t *testing.T) {
	db := setupMessages(t)
	wantPath := filepath.Join(t.TempDir(), "voice.ogg")
	messageDate := time.Date(2025, 3, 4, 5, 6, 7, 0, time.FixedZone("source", 2*60*60))
	if err := StoreMessageMediaPath(db, 7, 33, messageDate, "voice", wantPath); err != nil {
		t.Fatalf("StoreMessageMediaPath: %v", err)
	}
	got, err := GetOne(db, 7, 33, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasMedia || got.MediaType == nil || *got.MediaType != "voice" || got.MediaPath == nil || *got.MediaPath != wantPath {
		t.Fatalf("minimal message = %#v", got)
	}
	if got.Date != "2025-03-04T03:06:07Z" {
		t.Fatalf("minimal message date=%q want authoritative UTC date", got.Date)
	}
	laterText := "later"
	if err := InsertMessage(db, Message{ChatID: 7, MessageID: 35, Date: "2025-04-01T00:00:00Z", Text: &laterText}); err != nil {
		t.Fatal(err)
	}
	rows, err := List(db, ListOptions{ChatID: 7, Since: "2025-03-04T03:06:08Z", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MessageID != 35 {
		t.Fatalf("date filter/order used fabricated download time: %#v", rows)
	}
}

func TestStoreMessageMediaPathMissingRejectsUnknownMessageDate(t *testing.T) {
	db := setupMessages(t)
	err := StoreMessageMediaPath(db, 7, 34, time.Time{}, "voice", "/tmp/voice.ogg")
	if err == nil || !strings.Contains(err.Error(), "authoritative message date") {
		t.Fatalf("error=%v want authoritative-date failure", err)
	}
	if _, err := GetOne(db, 7, 34, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing row was inserted: %v", err)
	}
}

func TestStoreMessageMediaPathConflictPreservesRicherFields(t *testing.T) {
	db := setupMessages(t)
	text, raw := "rich text", `{"rich":true}`
	sender := int64(91)
	if err := InsertMessage(db, Message{
		ChatID: 8, MessageID: 44, SenderID: &sender, Date: "2026-07-31T09:30:00Z",
		Text: &text, IsOutgoing: true, RawJSON: &raw,
	}); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(t.TempDir(), "video.mp4")
	if err := StoreMessageMediaPath(db, 8, 44, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), "video", wantPath); err != nil {
		t.Fatalf("StoreMessageMediaPath: %v", err)
	}
	got, err := GetOne(db, 8, 44, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text == nil || *got.Text != text || got.RawJSON == nil || *got.RawJSON != raw || got.SenderID == nil || *got.SenderID != sender || !got.IsOutgoing || got.Date != "2026-07-31T09:30:00Z" {
		t.Fatalf("rich fields were overwritten: %#v", got)
	}
	if got.MediaType == nil || *got.MediaType != "video" || got.MediaPath == nil || *got.MediaPath != wantPath {
		t.Fatalf("media fields not updated: %#v", got)
	}
}

func TestStoreMessageMediaPathConcurrentInsertConflictMergesFields(t *testing.T) {
	db := setupMessages(t)
	// A single underlying SQLite connection makes statement ordering
	// deterministic while the callers still race at the store API boundary.
	// Either order must merge to one rich row with downloaded media.
	var seq int
	var schemaName, dbPath string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &schemaName, &dbPath); err != nil {
		t.Fatal(err)
	}
	db2, err := Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := db.Exec(`
			INSERT INTO tg_messages(chat_id, message_id, date, text, sender_id, deleted)
			VALUES (12, 55, '2026-07-31T12:00:00Z', 'concurrent rich text', 99, 0)
			ON CONFLICT(chat_id, message_id) DO UPDATE SET
				date=excluded.date, text=excluded.text, sender_id=excluded.sender_id`)
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		errCh <- StoreMessageMediaPath(db2, 12, 55, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), "document", "/tmp/concurrent.pdf")
	}()
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent statement: %v", err)
		}
	}
	got, err := GetOne(db, 12, 55, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text == nil || *got.Text != "concurrent rich text" || got.SenderID == nil || *got.SenderID != 99 {
		t.Fatalf("rich fields missing: %#v", got)
	}
	if got.MediaType == nil || *got.MediaType != "document" || got.MediaPath == nil || *got.MediaPath != "/tmp/concurrent.pdf" {
		t.Fatalf("media fields missing: %#v", got)
	}
}

func TestStoreMessageMediaPathStatementErrorLeavesRowUntouched(t *testing.T) {
	db := setupMessages(t)
	if _, err := db.Exec(`CREATE TRIGGER reject_media_path BEFORE UPDATE OF media_path ON tg_messages BEGIN SELECT RAISE(ABORT, 'reject media'); END`); err != nil {
		t.Fatal(err)
	}
	err := StoreMessageMediaPath(db, 1, 12, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), "video", "/tmp/rejected.mp4")
	if err == nil {
		t.Fatal("expected persistence error")
	}
	got, err := GetOne(db, 1, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediaType == nil || *got.MediaType != "photo" || got.MediaPath != nil {
		t.Fatalf("row changed after failed statement: %#v", got)
	}
}
