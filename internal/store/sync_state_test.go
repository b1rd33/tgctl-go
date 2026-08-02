package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Connect(filepath.Join(t.TempDir(), "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSyncStateFreshUpsertAndList(t *testing.T) {
	db := testDB(t)
	state := SyncState{Account: "work", ChatID: 42, LastMessageID: 7, LastSyncAt: "2026-08-02T12:00:00Z", UpdatedAt: "2026-08-02T12:00:01Z"}
	if err := SaveSyncState(db, state); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSyncState(db, "work", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got != state {
		t.Fatalf("state=%+v want %+v", got, state)
	}
	state.LastMessageID = 9
	state.LastSyncAt = "2026-08-02T12:01:00Z"
	state.UpdatedAt = "2026-08-02T12:01:01Z"
	if err := SaveSyncState(db, state); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSyncState(db, "work", 42); err != nil || got.LastMessageID != 9 {
		t.Fatalf("updated state=%+v err=%v", got, err)
	}
	if err := SaveSyncState(db, SyncState{Account: "work", ChatID: 7, LastMessageID: 3, UpdatedAt: "2026-08-02T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	states, err := ListSyncStates(db, "work")
	if err != nil || len(states) != 2 || states[0].ChatID != 7 || states[1].ChatID != 42 {
		t.Fatalf("states=%+v err=%v", states, err)
	}
}

func TestSyncStateMissingAndValidation(t *testing.T) {
	db := testDB(t)
	if _, err := LoadSyncState(db, "default", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing state err=%v", err)
	}
	for _, state := range []SyncState{{ChatID: 0, UpdatedAt: "now"}, {ChatID: 1, LastMessageID: -1, UpdatedAt: "now"}} {
		if err := SaveSyncState(db, state); err == nil {
			t.Fatalf("invalid state accepted: %+v", state)
		}
	}
}

func TestSyncStateMigrationOnLegacySchema(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec("DROP TABLE tg_sync_state"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_sync_state_updated"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := SaveSyncState(db, SyncState{ChatID: 5, LastMessageID: 10, UpdatedAt: "now"}); err != nil {
		t.Fatal(err)
	}
}
