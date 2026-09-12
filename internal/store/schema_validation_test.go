package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsupportedSchemaRejectedWithoutConversion(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "writable", true: "readonly"}[readOnly], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("CREATE TABLE tg_messages(chat_id INTEGER,message_id INTEGER,text TEXT); INSERT INTO tg_messages VALUES(7,9,'keep')"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			open := Connect
			if readOnly {
				open = ConnectReadonly
			}
			got, err := open(path)
			if got != nil {
				got.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "unsupported cache schema") {
				t.Fatalf("err=%v", err)
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var text string
			if err = db.QueryRow("SELECT text FROM tg_messages WHERE chat_id=7 AND message_id=9").Scan(&text); err != nil || text != "keep" {
				t.Fatalf("data changed: %q %v", text, err)
			}
			var count int
			if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("tables changed: %d %v", count, err)
			}
		})
	}
}

func TestIncompleteCurrentSchemaRejected(t *testing.T) {
	for _, column := range []string{"media_path", "media_id", "edit_date"} {
		t.Run(column, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.sqlite")
			db, err := Connect(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("ALTER TABLE tg_messages DROP COLUMN " + column); err != nil {
				t.Fatal(err)
			}
			db.Close()
			for _, open := range []func(string) (*sql.DB, error){Connect, ConnectReadonly} {
				db, err = open(path)
				if db != nil {
					db.Close()
				}
				if err == nil {
					t.Fatal("incomplete schema accepted")
				}
			}
		})
	}
}
