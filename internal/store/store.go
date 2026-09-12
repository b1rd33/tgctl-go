package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Connect opens a current-schema database or initializes an empty database.
func Connect(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("database must be a regular file without symlinks")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(4)
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = validateSchema(tx, true); err != nil {
		_ = tx.Rollback()
		db.Close()
		return nil, err
	}
	_, err = tx.Exec(Schema)
	if err != nil {
		_ = tx.Rollback()
		db.Close()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}

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
	uri := "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	if err := validateSchema(db, false); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type schemaDB interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// DBTX permits applying a cache batch and checkpoint in the same transaction.
type DBTX = schemaDB
