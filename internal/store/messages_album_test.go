package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAlbumSchemaMigratesGroupedIDAndIndex(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "messages.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	if !columnExists(db, "tg_messages", "grouped_id") {
		t.Fatal("tg_messages missing grouped_id")
	}
	var indexName string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_messages_chat_grouped'").Scan(&indexName); err != nil {
		t.Fatalf("missing grouped index: %v", err)
	}
}

func TestAlbumRowsRoundTripAndStableMessageIDOrder(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "messages.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	for _, id := range []int64{103, 101, 102} {
		mediaType := "photo"
		ext := ".jpg"
		if id == 102 {
			mediaType = "video"
			ext = ".mp4"
		}
		path := filepath.Join("/safe", string(rune('0'+id-100))+ext)
		if err := InsertMessage(db, Message{ChatID: 4, MessageID: id, GroupedID: 88, Date: "2026-08-01T00:00:00Z", HasMedia: true, MediaType: &mediaType, MediaPath: &path}); err != nil {
			t.Fatalf("InsertMessage(%d): %v", id, err)
		}
	}
	summaries, err := Show(db, ShowOptions{ChatID: 4, Limit: 10})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	var summaryGrouped bool
	for _, summary := range summaries {
		if summary.MessageID == 101 {
			summaryGrouped = summary.GroupedID == 88
		}
	}
	if !summaryGrouped {
		t.Fatal("summary did not retain grouped_id")
	}
	rows, err := ListAlbum(db, 4, 88, false)
	if err != nil {
		t.Fatalf("ListAlbum: %v", err)
	}
	if got := []int64{rows[0].MessageID, rows[1].MessageID, rows[2].MessageID}; !reflect.DeepEqual(got, []int64{101, 102, 103}) {
		t.Fatalf("message order = %v", got)
	}
	if rows[0].GroupedID != 88 {
		t.Fatalf("grouped id = %d, want 88", rows[0].GroupedID)
	}
	if rows[1].MediaType == nil || *rows[1].MediaType != "video" {
		t.Fatalf("mixed album media type = %#v", rows[1].MediaType)
	}
	anchor, err := GetAlbum(db, 4, 102, false)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if len(anchor) != 3 || anchor[1].MessageID != 102 {
		t.Fatalf("anchor album = %#v", anchor)
	}
}

func TestGetAlbumUngroupedAnchorReturnsOneRow(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "messages.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	if err := InsertMessage(db, Message{ChatID: 4, MessageID: 7, Date: "2026-08-01T00:00:00Z"}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	rows, err := GetAlbum(db, 4, 7, false)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if len(rows) != 1 || rows[0].MessageID != 7 {
		t.Fatalf("ungrouped rows = %#v", rows)
	}
}

func TestRecordUploadedAlbumPersistsGroupedID(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "messages.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	if err := RecordUploadedAlbum(db, 3, []UploadedMedia{{MessageID: 11, GroupedID: 999, MediaType: "photo", MediaPath: "/safe/a.jpg"}}); err != nil {
		t.Fatalf("RecordUploadedAlbum: %v", err)
	}
	row, err := GetOne(db, 3, 11, false)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if row.GroupedID != 999 {
		t.Fatalf("grouped id = %d, want 999", row.GroupedID)
	}
}

func TestOldSchemaMigratesGroupedID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_messages_chat_grouped"); err != nil {
		t.Fatalf("drop grouped index: %v", err)
	}
	if _, err := db.Exec("ALTER TABLE tg_messages DROP COLUMN grouped_id"); err != nil {
		t.Fatalf("drop grouped_id: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db, err = Connect(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	if !columnExists(db, "tg_messages", "grouped_id") {
		t.Fatal("migration failed: grouped_id")
	}
	var indexName string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_messages_chat_grouped'").Scan(&indexName); err != nil {
		t.Fatalf("migration failed: grouped index: %v", err)
	}
}
