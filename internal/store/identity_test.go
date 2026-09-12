package store

import (
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
