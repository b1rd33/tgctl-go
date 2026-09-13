package store

import (
	"path/filepath"
	"testing"
)

func TestRemoteCursorRejectsMalformedOffsetsAndMismatchedQueries(t *testing.T) {
	expected := RemoteCursor{Account: "default", Operation: "history", Chat: 7, Since: "2026-01-01", Until: "2026-01-02"}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "malformed base64", raw: "not-a-cursor"},
		{name: "zero offset", raw: EncodeRemoteCursor(RemoteCursor{Account: "default", Operation: "history", Chat: 7, Since: "2026-01-01", Until: "2026-01-02"})},
		{name: "cross account", raw: EncodeRemoteCursor(RemoteCursor{Account: "other", Operation: "history", Chat: 7, Since: "2026-01-01", Until: "2026-01-02", OffsetID: 9})},
		{name: "cross filter", raw: EncodeRemoteCursor(RemoteCursor{Account: "default", Operation: "history", Chat: 7, Since: "2026-01-02", Until: "2026-01-02", OffsetID: 9})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRemoteCursor(tc.raw, expected); err == nil {
				t.Fatal("invalid remote cursor was accepted")
			}
		})
	}
	valid := EncodeRemoteCursor(RemoteCursor{Account: "default", Operation: "history", Chat: 7, Since: "2026-01-01", Until: "2026-01-02", OffsetID: 9})
	got, err := DecodeRemoteCursor(valid, expected)
	if err != nil || got.OffsetID != 9 {
		t.Fatalf("valid cursor=%+v err=%v", got, err)
	}
}

func TestCursorPagesEqualTimestampsWithoutDuplicates(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 1; i <= 5; i++ {
		text := "synthetic"
		if err := UpsertLiveMessage(db, LiveMessage{ChatID: 7, MessageID: int64(i), Date: "2026-01-01T00:00:00Z", Text: &text}); err != nil {
			t.Fatal(err)
		}
	}
	for _, reverse := range []bool{false, true} {
		cursor := ""
		seen := map[int64]bool{}
		for page := 0; page < 4; page++ {
			rows, err := Show(db, ShowOptions{ChatID: 7, Limit: 2, Reverse: reverse, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if seen[row.MessageID] {
					t.Fatal("duplicate cursor row")
				}
				seen[row.MessageID] = true
			}
			cursor = MessageCursor(7, reverse, rows, 2)
			if cursor == "" {
				break
			}
		}
		if len(seen) != 5 {
			t.Fatalf("cursor skipped rows: %v", seen)
		}
	}
	if _, err := Show(db, ShowOptions{ChatID: 7, Limit: 2, Cursor: "invalid"}); err == nil {
		t.Fatal("malformed cursor accepted")
	}
}
func TestStaleReplayPreservesEditAndMediaReplacementClearsPath(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	text, old, id, path := "edited", "old", "photo:1", "synthetic.jpg"
	m := LiveMessage{ChatID: 7, MessageID: 1, Date: "2026-01-01T00:00:00Z", Text: &text, EditDate: 20, HasMedia: true, MediaIdentity: &id, MediaPath: &path}
	if err := UpsertLiveMessage(db, m); err != nil {
		t.Fatal(err)
	}
	m.Text = &old
	m.EditDate = 0
	if err := UpsertLiveMessage(db, m); err != nil {
		t.Fatal(err)
	}
	var got string
	db.QueryRow("SELECT text FROM tg_messages WHERE chat_id=7").Scan(&got)
	if got != text {
		t.Fatal("old replay overwrote edit")
	}
	newID := "photo:2"
	m.MediaIdentity = &newID
	m.MediaPath = nil
	m.EditDate = 30
	if err := UpsertLiveMessage(db, m); err != nil {
		t.Fatal(err)
	}
	var retained int
	db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE media_path IS NOT NULL").Scan(&retained)
	if retained != 0 {
		t.Fatal("edited media retained stale file association")
	}
	if err := MarkLiveMessagesDeleted(db, 7, []int64{1}); err != nil {
		t.Fatal(err)
	}
	m.EditDate = 40
	if err := UpsertLiveMessage(db, m); err != nil {
		t.Fatal(err)
	}
	var deleted int
	db.QueryRow("SELECT deleted FROM tg_messages WHERE chat_id=7").Scan(&deleted)
	if deleted != 1 {
		t.Fatal("tombstone resurrected")
	}
}
