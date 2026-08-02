package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func setupMessages(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	hello := "hello world"
	media := "photo"
	caption := "look 50% off __sale__"
	must(InsertMessage(db, Message{ChatID: 1, MessageID: 10, Date: "2026-05-01T10:00:00", Text: &hello, IsOutgoing: false}))
	must(InsertMessage(db, Message{ChatID: 1, MessageID: 11, Date: "2026-05-02T11:00:00", Text: &caption, IsOutgoing: true}))
	must(InsertMessage(db, Message{ChatID: 1, MessageID: 12, Date: "2026-05-03T12:00:00", IsOutgoing: false, HasMedia: true, MediaType: &media}))
	must(InsertMessage(db, Message{ChatID: 1, MessageID: 13, Date: "2026-05-04T13:00:00", Text: &hello, IsOutgoing: true}))
	// Tombstoned message:
	deletedText := "removed"
	must(InsertMessage(db, Message{ChatID: 1, MessageID: 14, Date: "2026-05-05T14:00:00", Text: &deletedText}))
	must(MarkDeleted(db, 1, 14))
	return db
}

func TestShowDefaultExcludesDeleted(t *testing.T) {
	db := setupMessages(t)
	rows, err := Show(db, ShowOptions{ChatID: 1, Limit: 100})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("len=%d, want 4 (deleted excluded)", len(rows))
	}
	// Default order is DESC by date — newest first.
	if rows[0].MessageID != 13 {
		t.Fatalf("order: first MessageID = %d", rows[0].MessageID)
	}
}

func TestUpsertLiveMessageIsIdempotentAndRejectsOlderMutation(t *testing.T) {
	db := setupMessages(t)
	first := "first"
	second := "second"
	if err := UpsertLiveMessage(db, LiveMessage{ChatID: 1, MessageID: 90, Date: "2026-05-10T10:00:00Z", Text: &first, GroupedID: 700}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertLiveMessage(db, LiveMessage{ChatID: 1, MessageID: 90, Date: "2026-05-10T09:00:00Z", Text: &second, GroupedID: 701}); err != nil {
		t.Fatal(err)
	}
	row, err := GetOne(db, 1, 90, true)
	if err != nil {
		t.Fatal(err)
	}
	if row.Text == nil || *row.Text != first || row.GroupedID != 700 {
		t.Fatalf("older mutation overwrote row: %+v", row)
	}
	if err := MarkLiveMessagesDeleted(db, 1, []int64{90, 999}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetOne(db, 1, 90, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted live row visible: %v", err)
	}
}

func TestShowReverseAscending(t *testing.T) {
	db := setupMessages(t)
	rows, err := Show(db, ShowOptions{ChatID: 1, Limit: 100, Reverse: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rows[0].MessageID != 10 {
		t.Fatalf("first = %d, want 10", rows[0].MessageID)
	}
}

func TestShowIncludeDeleted(t *testing.T) {
	db := setupMessages(t)
	rows, err := Show(db, ShowOptions{ChatID: 1, Limit: 100, IncludeDeleted: true})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("len=%d, want 5 (with deleted)", len(rows))
	}
}

func TestSearchEscapesLikeMetacharacters(t *testing.T) {
	db := setupMessages(t)
	// "50%" should match the text containing literal "50%" not act as wildcard.
	rows, err := Search(db, SearchOptions{ChatID: 1, Query: "50%", Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d, want 1", len(rows))
	}
	// "_sale_" with literal underscores should match the same row.
	rows2, err := Search(db, SearchOptions{ChatID: 1, Query: "_sale_", Limit: 10})
	if err != nil {
		t.Fatalf("err2: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("rows2 len=%d", len(rows2))
	}
}

func TestSearchCaseSensitiveUsesInstr(t *testing.T) {
	db := setupMessages(t)
	rows, err := Search(db, SearchOptions{ChatID: 1, Query: "Hello", CaseSensitive: true, Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("case-sensitive Hello should not match lowercase hello, got %d", len(rows))
	}
	rows2, err := Search(db, SearchOptions{ChatID: 1, Query: "hello", CaseSensitive: true, Limit: 10})
	if err != nil {
		t.Fatalf("err2: %v", err)
	}
	if len(rows2) != 2 {
		t.Fatalf("len=%d, want 2", len(rows2))
	}
}

func TestSearchOrderByDateThenIDDesc(t *testing.T) {
	db := setupMessages(t)
	rows, err := Search(db, SearchOptions{ChatID: 1, Query: "hello", Limit: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rows[0].MessageID != 13 || rows[1].MessageID != 10 {
		t.Fatalf("order = %v", rows)
	}
}

func TestListAppliesDateRange(t *testing.T) {
	db := setupMessages(t)
	rows, err := List(db, ListOptions{
		ChatID: 1, Limit: 100,
		Since: "2026-05-02T00:00:00",
		Until: "2026-05-03T23:59:59",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len=%d, want 2", len(rows))
	}
}

func TestGetOneReturnsFullMessage(t *testing.T) {
	db := setupMessages(t)
	msg, err := GetOne(db, 1, 12, false)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if msg.MessageID != 12 || !msg.HasMedia || msg.MediaType == nil || *msg.MediaType != "photo" {
		t.Fatalf("msg = %#v", msg)
	}
}

func TestGetOneDeletedExcludedByDefault(t *testing.T) {
	db := setupMessages(t)
	_, err := GetOne(db, 1, 14, false)
	if err == nil {
		t.Fatalf("expected ErrNoRows, got nil")
	}
}

func TestGetOneDeletedIncludable(t *testing.T) {
	db := setupMessages(t)
	msg, err := GetOne(db, 1, 14, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if msg.MessageID != 14 {
		t.Fatalf("msg = %#v", msg)
	}
}
