package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type stubPaths struct {
	db, session, audit string
}

func (s stubPaths) AccountPaths(string) (string, string, string) {
	return s.db, s.session, s.audit
}

func setupCacheDB(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telegram.sqlite")
	sessionPath := filepath.Join(dir, "tg.session")
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := store.UpsertMe(db, store.MeRow{
		UserID:      99,
		Username:    sql.NullString{String: "nikolov", Valid: true},
		FirstName:   sql.NullString{String: "Toni", Valid: true},
		DisplayName: sql.NullString{String: "Toni", Valid: true},
		IsBot:       0,
		CachedAt:    "2026-05-08T10:00:00+00:00",
		RawJSON:     sql.NullString{String: `{"id":99}`, Valid: true},
	}); err != nil {
		t.Fatalf("UpsertMe: %v", err)
	}
	db.Close()
	return dbPath, sessionPath
}

func TestMeOfflineReturnsCachedRow(t *testing.T) {
	dbPath, sessionPath := setupCacheDB(t)
	data, err := MeOfflineRunner(context.Background(), dbPath, sessionPath)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	m := data.(map[string]any)
	if m["user_id"] != int64(99) {
		t.Fatalf("user_id = %#v", m["user_id"])
	}
	if m["source"] != "cache" {
		t.Fatalf("source = %v", m["source"])
	}
	if m["session_path"] != sessionPath {
		t.Fatalf("session_path = %v", m["session_path"])
	}
	if m["raw_json"].(map[string]any)["id"].(float64) != 99 {
		t.Fatalf("raw_json = %#v", m["raw_json"])
	}
}

func TestMeOfflineMissingCacheReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telegram.sqlite")
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	db.Close()
	_, err = MeOfflineRunner(context.Background(), dbPath, dir+"/tg.session")
	if err == nil {
		t.Fatalf("expected NotFound, got nil")
	}
}

func TestMeOfflineMissingDBReturnsDatabaseMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := MeOfflineRunner(context.Background(), filepath.Join(dir, "no.sqlite"), dir+"/x")
	var dbm *store.DatabaseMissing
	if err == nil || !errorsAs(err, &dbm) {
		t.Fatalf("err = %v, want *store.DatabaseMissing", err)
	}
}

// errorsAs avoids importing errors here — small test helper.
func errorsAs(err error, target any) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		switch t := target.(type) {
		case **store.DatabaseMissing:
			if dbm, ok := err.(*store.DatabaseMissing); ok {
				*t = dbm
				return true
			}
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestMeCommandRegisteredAndOfflineEmitsEnvelope(t *testing.T) {
	dbPath, sessionPath := setupCacheDB(t)
	auditPath := filepath.Join(filepath.Dir(dbPath), "audit.log")

	root := NewRootCommand()
	registerAuth(root, stubPaths{db: dbPath, session: sessionPath, audit: auditPath})

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"me", "--offline", "--json"})

	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\nstdout: %s", err, stdout.String())
	}
	if env["ok"] != true || env["command"] != "me" {
		t.Fatalf("envelope = %#v", env)
	}
	data := env["data"].(map[string]any)
	if data["source"] != "cache" {
		t.Fatalf("source = %v", data["source"])
	}
	// Sanity: dispatch generated a request id.
	if rid, _ := env["request_id"].(string); rid == "" {
		t.Fatalf("request_id missing")
	}
	_ = output.OK
}
