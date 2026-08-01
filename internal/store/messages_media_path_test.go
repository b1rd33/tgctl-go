package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
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
	if err := StoreMessageMediaPath(db, 7, 33, "voice", wantPath); err != nil {
		t.Fatalf("StoreMessageMediaPath: %v", err)
	}
	got, err := GetOne(db, 7, 33, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasMedia || got.MediaType == nil || *got.MediaType != "voice" || got.MediaPath == nil || *got.MediaPath != wantPath {
		t.Fatalf("minimal message = %#v", got)
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
	if err := StoreMessageMediaPath(db, 8, 44, "video", wantPath); err != nil {
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
	db.SetMaxOpenConns(1)
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
		errCh <- StoreMessageMediaPath(db, 12, 55, "document", "/tmp/concurrent.pdf")
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
	err := StoreMessageMediaPath(db, 1, 12, "video", "/tmp/rejected.mp4")
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
