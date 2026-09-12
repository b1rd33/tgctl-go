package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestConnectCreatesAllTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()

	want := []string{"tg_chats", "tg_messages", "tg_contacts", "tg_me", "tg_idempotency"}
	for _, name := range want {
		var got string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
		if err != nil {
			t.Fatalf("missing table %s: %v", name, err)
		}
	}
}

func TestConnectAppliesIndexes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()

	indexes := []string{"idx_messages_chat_date", "idx_messages_date"}
	for _, idx := range indexes {
		var got string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&got)
		if err != nil {
			t.Fatalf("missing index %s: %v", idx, err)
		}
	}
}

func TestMessagesHasCurrentColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()

	for _, col := range []string{"media_path", "media_id", "deleted"} {
		if !columnExists(db, "tg_messages", col) {
			t.Fatalf("tg_messages missing column %s", col)
		}
	}
	if !columnExists(db, "tg_chats", "left") {
		t.Fatalf("tg_chats missing column left")
	}
}

func TestConnectReadonlyMissingReturnsDatabaseMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no.sqlite")
	_, err := ConnectReadonly(path)
	var dbm *DatabaseMissing
	if !errors.As(err, &dbm) {
		t.Fatalf("err = %v, want *DatabaseMissing", err)
	}
}

func TestConnectReadonlyDoesNotCreateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	db.Close()

	ro, err := ConnectReadonly(path)
	if err != nil {
		t.Fatalf("ConnectReadonly: %v", err)
	}
	defer ro.Close()
	if _, err := ro.Exec("INSERT INTO tg_chats(chat_id, title) VALUES (1, 'x')"); err == nil {
		t.Fatalf("read-only DB allowed write")
	}
}

func columnExists(db schemaDB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
