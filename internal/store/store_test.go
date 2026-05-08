package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/resolve"
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

func TestMessagesHasMigratedColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()

	for _, col := range []string{"media_path", "deleted"} {
		if !columnExists(db, "tg_messages", col) {
			t.Fatalf("tg_messages missing column %s", col)
		}
	}
	if !columnExists(db, "tg_chats", "left") {
		t.Fatalf("tg_chats missing column left")
	}
}

func TestConnectMigratesOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telegram.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Drop migrated columns to simulate older DB. SQLite supports DROP
	// COLUMN since 3.35; modernc.org/sqlite is current.
	for _, alt := range []string{
		"ALTER TABLE tg_messages DROP COLUMN media_path",
		"ALTER TABLE tg_messages DROP COLUMN deleted",
		"ALTER TABLE tg_chats DROP COLUMN left",
	} {
		if _, err := db.Exec(alt); err != nil {
			t.Fatalf("setup drop: %v", err)
		}
	}
	db.Close()

	db2, err := Connect(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	for _, col := range []string{"media_path", "deleted"} {
		if !columnExists(db2, "tg_messages", col) {
			t.Fatalf("migration failed: tg_messages.%s", col)
		}
	}
	if !columnExists(db2, "tg_chats", "left") {
		t.Fatalf("migration failed: tg_chats.left")
	}
}

func TestConnectReadonlyMissingReturnsDatabaseMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no.sqlite")
	_, err := ConnectReadonly(path)
	var dbm *resolve.DatabaseMissing
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
