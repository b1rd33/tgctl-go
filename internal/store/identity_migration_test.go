package store

import (
	"database/sql"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"path/filepath"
	"testing"
)

func TestPeerKindsCannotOverwriteEachOther(t *testing.T) {
	db, err := Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []EntityKind{EntityUser, EntityChat, EntityChannel} {
		if err := UpsertEntity(db, 7, kind, 99); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[int64]EntityKind{7: EntityUser, peerid.Chat(7): EntityChat, peerid.Channel(7): EntityChannel} {
		kind, _, ok := LoadEntity(db, id)
		if !ok || kind != want {
			t.Fatalf("id=%d kind=%s", id, kind)
		}
	}
}
func TestLegacyIdentityPreservesAmbiguousMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE tg_chats(chat_id INTEGER PRIMARY KEY,title TEXT); CREATE TABLE tg_messages(chat_id INTEGER,message_id INTEGER,text TEXT); INSERT INTO tg_chats VALUES(7,'legacy'); INSERT INTO tg_messages VALUES(7,9,'preserve this');`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	db, err := Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal("ambiguous message migrated into active cache")
	}
	db.Close()
	legacy, err := ConnectLegacyReadonly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	var text string
	if err := legacy.QueryRow("SELECT text FROM tg_messages WHERE chat_id=7 AND message_id=9").Scan(&text); err != nil || text != "preserve this" {
		t.Fatal("legacy snapshot lost")
	}
}
