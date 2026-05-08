package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLoadMeMissingReturnsErrNoRows(t *testing.T) {
	dir := t.TempDir()
	db, err := Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	_, err = LoadMe(db)
	if err != sql.ErrNoRows {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestUpsertMeRoundTrips(t *testing.T) {
	dir := t.TempDir()
	db, err := Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	if err := UpsertMe(db, MeRow{
		UserID:      42,
		Username:    sql.NullString{String: "alice", Valid: true},
		Phone:       sql.NullString{String: "+15555550100", Valid: true},
		FirstName:   sql.NullString{String: "Alice", Valid: true},
		DisplayName: sql.NullString{String: "Alice", Valid: true},
		IsBot:       0,
		CachedAt:    "2026-05-08T10:00:00+00:00",
		RawJSON:     sql.NullString{String: `{"id":42}`, Valid: true},
	}); err != nil {
		t.Fatalf("UpsertMe: %v", err)
	}
	row, err := LoadMe(db)
	if err != nil {
		t.Fatalf("LoadMe: %v", err)
	}
	if row.UserID != 42 || row.Username.String != "alice" || row.DisplayName.String != "Alice" {
		t.Fatalf("row = %#v", row)
	}
}

func TestUpsertMeReplacesRow(t *testing.T) {
	dir := t.TempDir()
	db, err := Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
	}
	must(UpsertMe(db, MeRow{UserID: 1, CachedAt: "a"}))
	must(UpsertMe(db, MeRow{UserID: 2, CachedAt: "b"}))
	row, err := LoadMe(db)
	must(err)
	if row.UserID != 2 || row.CachedAt != "b" {
		t.Fatalf("row = %#v", row)
	}
	var n int
	must(db.QueryRow("SELECT COUNT(*) FROM tg_me").Scan(&n))
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}
