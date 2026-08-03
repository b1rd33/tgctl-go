package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Connect opens the DB read-write, applies schema, and runs idempotent
// migrations. Mirrors tgcli.db.connect.
func Connect(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(Schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return db, nil
}

// ConnectReadonly opens the DB read-only and never writes or migrates.
// Returns *resolve.DatabaseMissing when the file does not exist.
func ConnectReadonly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, &DatabaseMissing{Path: path}
		}
		return nil, err
	}
	uri := "file:" + url.PathEscape(path) + "?mode=ro"
	return sql.Open("sqlite", uri)
}

// migrate runs idempotent post-schema upgrades. The Schema constant already
// reflects the final migrated state, so this exists to handle DBs created by
// older versions in the field.
func migrate(db *sql.DB) error {
	if !columnExists(db, "tg_messages", "media_path") {
		if _, err := db.Exec("ALTER TABLE tg_messages ADD COLUMN media_path TEXT"); err != nil {
			return err
		}
	}
	if !columnExists(db, "tg_messages", "media_id") {
		if _, err := db.Exec("ALTER TABLE tg_messages ADD COLUMN media_id TEXT"); err != nil {
			return fmt.Errorf("migrate tg_messages.media_id: %w", err)
		}
	}
	if !columnExists(db, "tg_messages", "grouped_id") {
		if _, err := db.Exec("ALTER TABLE tg_messages ADD COLUMN grouped_id INTEGER"); err != nil {
			return fmt.Errorf("migrate tg_messages.grouped_id: %w", err)
		}
	}
	if !columnExists(db, "tg_messages", "deleted") {
		if _, err := db.Exec("ALTER TABLE tg_messages ADD COLUMN deleted INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	if !columnExists(db, "tg_chats", "left") {
		if _, err := db.Exec("ALTER TABLE tg_chats ADD COLUMN left INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	if !tableExists(db, "tg_entities") {
		if _, err := db.Exec(`
			CREATE TABLE tg_entities (
				id          INTEGER PRIMARY KEY,
				kind        TEXT NOT NULL,
				access_hash INTEGER,
				updated_at  TEXT NOT NULL
			)`); err != nil {
			return err
		}
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_chat_grouped ON tg_messages(chat_id, grouped_id, message_id)"); err != nil {
		return fmt.Errorf("migrate tg_messages grouped index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tg_sync_state (
			account TEXT NOT NULL DEFAULT 'default',
			chat_id INTEGER NOT NULL,
			last_message_id INTEGER NOT NULL DEFAULT 0,
			last_sync_at TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (account, chat_id)
		)`); err != nil {
		return fmt.Errorf("migrate tg_sync_state: %w", err)
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_sync_state_updated ON tg_sync_state(updated_at)"); err != nil {
		return fmt.Errorf("migrate tg_sync_state index: %w", err)
	}
	return nil
}

func tableExists(db *sql.DB, name string) bool {
	var got string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&got)
	return err == nil
}

func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
