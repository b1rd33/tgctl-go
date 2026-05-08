package resolve

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func setupChats(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rows := []struct {
		id       int64
		title    string
		username string
	}{
		{1, "Bjørn Müller", ""},
		{2, "Bjarne Test Group", ""},
		{3, "Cats", "catsfeed"},
		{4, "", ""}, // null-title fallback
	}
	for _, r := range rows {
		if _, err := db.Exec(
			"INSERT INTO tg_chats(chat_id, title, username) VALUES (?, ?, ?)",
			r.id, r.title, nullIfEmpty(r.username),
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestResolveIntChatID(t *testing.T) {
	db := setupChats(t)
	id, title, err := ResolveChatDB(db, "1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != 1 || title != "Bjørn Müller" {
		t.Fatalf("got (%d,%q)", id, title)
	}
}

func TestResolveUsernameCaseInsensitive(t *testing.T) {
	db := setupChats(t)
	id, _, err := ResolveChatDB(db, "@CATSFEED")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != 3 {
		t.Fatalf("id = %d", id)
	}
}

func TestResolveFuzzyAccentInsensitive(t *testing.T) {
	db := setupChats(t)
	id, _, err := ResolveChatDB(db, "müller")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != 1 {
		t.Fatalf("id = %d", id)
	}
}

func TestResolveAmbiguousReturnsCandidates(t *testing.T) {
	db := setupChats(t)
	_, _, err := ResolveChatDB(db, "Bj")
	var amb *Ambiguous
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *Ambiguous", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %v", amb.Candidates)
	}
	if amb.Candidates[0][0] != int64(1) || amb.Candidates[1][0] != int64(2) {
		t.Fatalf("candidates ordering = %v", amb.Candidates)
	}
}

func TestResolveNoMatchReturnsNotFound(t *testing.T) {
	db := setupChats(t)
	_, _, err := ResolveChatDB(db, "zzz nothing")
	var nf *NotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *NotFound", err)
	}
}

func TestResolveEmptySelector(t *testing.T) {
	db := setupChats(t)
	_, _, err := ResolveChatDB(db, "   ")
	var nf *NotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveNullTitleFallsBackToChatID(t *testing.T) {
	db := setupChats(t)
	id, title, err := ResolveChatDB(db, "4")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != 4 || title != "chat_4" {
		t.Fatalf("got (%d,%q)", id, title)
	}
}

func TestResolveMissingIntReturnsNotFound(t *testing.T) {
	db := setupChats(t)
	_, _, err := ResolveChatDB(db, "999")
	var nf *NotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
}
