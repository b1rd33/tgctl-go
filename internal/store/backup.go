package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot uses SQLite's own snapshot operation, including committed WAL pages.
// The destination must not exist. It is published only after integrity checking.
func Snapshot(ctx context.Context, source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("snapshot destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	db, err := ConnectReadonly(source)
	if err != nil {
		return err
	}
	defer db.Close()
	parent := filepath.Dir(destination)
	stage, err := os.MkdirTemp(parent, ".tgctl-snapshot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	tmp := filepath.Join(stage, "database.sqlite")
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	if err := ValidateSnapshot(ctx, tmp); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Hard-link publication is no-clobber, unlike Rename on Unix.
	if err := os.Link(tmp, destination); err != nil {
		return err
	}
	return nil
}
func ValidateSnapshot(ctx context.Context, path string) error {
	db, err := ConnectReadonly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("snapshot integrity check failed")
	}
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('tg_chats','tg_messages')").Scan(&tables); err != nil {
		return err
	}
	if tables != 2 {
		return fmt.Errorf("snapshot is not a Telegram cache")
	}
	return nil
}

// LedgerOperations returns safe metadata only: serialized requests may contain
// messages and access hashes and are deliberately never part of this view.
func LedgerOperations(ctx context.Context, db *sql.DB, limit int) ([]map[string]any, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("limit must be between 1 and 1000")
	}
	rows, err := db.QueryContext(ctx, "SELECT call_id,request_id,method,state,created_at FROM tg_write_calls ORDER BY created_at DESC,call_id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, request, method, state, at string
		if err := rows.Scan(&id, &request, &method, &state, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"operation_id": id, "request_id": request, "method": strings.TrimSuffix(method, "Request"), "state": state, "created_at": at, "retry_safe": state == "rejected"})
	}
	return out, rows.Err()
}
