package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotIncludesWALAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "live.sqlite")
	dest := filepath.Join(dir, "snapshot.sqlite")
	db, err := Connect(source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id,title) VALUES(7,'synthetic')"); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(context.Background(), source, dest); err != nil {
		t.Fatal(err)
	}
	snap, err := ConnectReadonly(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	var title string
	if err := snap.QueryRow("SELECT title FROM tg_chats WHERE chat_id=7").Scan(&title); err != nil || title != "synthetic" {
		t.Fatal("snapshot lost WAL content")
	}
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(context.Background(), source, dest); err == nil {
		t.Fatal("snapshot overwrote destination")
	}
	after, _ := os.ReadFile(dest)
	if string(before) != string(after) {
		t.Fatal("existing snapshot changed")
	}
	restored := filepath.Join(dir, "restored.sqlite")
	if err := Snapshot(context.Background(), dest, restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
}
