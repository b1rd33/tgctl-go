package idempotency

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLookupEmptyKeyReturnsNil(t *testing.T) {
	db := newDB(t)
	got, err := Lookup(db, "", "send")
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestLookupMissingKeyReturnsNil(t *testing.T) {
	db := newDB(t)
	got, err := Lookup(db, "k1", "send")
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestRecordThenLookupRoundTrip(t *testing.T) {
	db := newDB(t)
	envelope := map[string]any{
		"ok":         true,
		"command":    "send",
		"request_id": "req-1",
		"data":       map[string]any{"message_id": float64(42)},
		"warnings":   []any{},
	}
	if err := Record(db, "k1", "send", "req-1", envelope); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := Lookup(db, "k1", "send")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v", got["ok"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", got["data"])
	}
	if data["message_id"] != float64(42) {
		t.Fatalf("message_id = %#v", data["message_id"])
	}
}

func TestLookupCommandMismatchReturnsBadArgs(t *testing.T) {
	db := newDB(t)
	if err := Record(db, "k1", "send", "req-1", map[string]any{"ok": true}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	_, err := Lookup(db, "k1", "edit-msg")
	var ba *safety.BadArgs
	if !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordEmptyKeyIsNoop(t *testing.T) {
	db := newDB(t)
	if err := Record(db, "", "send", "r", map[string]any{"ok": true}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_idempotency").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows, got %d", n)
	}
}

func TestReserveBlocksFreshPendingAndReclaimsStale(t *testing.T) {
	db := newDB(t)
	if _, reserved, err := Reserve(db, "fresh", "upload-album", "req-1", "fp"); err != nil || !reserved {
		t.Fatalf("initial reserve=%v err=%v", reserved, err)
	}
	if _, reserved, err := Reserve(db, "fresh", "upload-album", "req-2", "fp"); err != nil || reserved {
		t.Fatalf("fresh pending reserve=%v err=%v", reserved, err)
	}
	stale, _ := json.Marshal(map[string]any{
		"pending": true, "command": "upload-album", "request_id": "old", "idempotency_fingerprint": "fp",
		"reserved_at": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano),
	})
	if _, err := db.Exec(`INSERT INTO tg_idempotency(key, command, request_id, result_json, created_at) VALUES (?, ?, ?, ?, ?)`, "stale", "upload-album", "old", string(stale), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, reserved, err := Reserve(db, "stale", "upload-album", "req-3", "fp"); err != nil || !reserved {
		t.Fatalf("stale reserve=%v err=%v", reserved, err)
	}
}

func TestReserveDoesNotReclaimStaleDifferentFingerprint(t *testing.T) {
	db := newDB(t)
	stale, _ := json.Marshal(map[string]any{
		"pending": true, "command": "upload-album", "request_id": "old", "idempotency_fingerprint": "old-fp",
		"reserved_at": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano),
	})
	if _, err := db.Exec(`INSERT INTO tg_idempotency(key, command, request_id, result_json, created_at) VALUES (?, ?, ?, ?, ?)`, "stale-different", "upload-album", "old", string(stale), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	_, reserved, err := Reserve(db, "stale-different", "upload-album", "req", "new-fp")
	if err == nil || reserved {
		t.Fatalf("reserve=%v err=%v", reserved, err)
	}
	var badArgs *safety.BadArgs
	if !errors.As(err, &badArgs) {
		t.Fatalf("err=%v is not BadArgs", err)
	}
}
