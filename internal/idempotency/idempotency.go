package idempotency

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

// Lookup returns a cached result envelope for key+command, or nil if absent.
// Mismatched command for the same key returns *safety.BadArgs.
// Mirrors tgcli.idempotency.lookup.
func Lookup(db *sql.DB, key, command string) (map[string]any, error) {
	if key == "" {
		return nil, nil
	}
	var recordedCmd, resultJSON string
	err := db.QueryRow(
		"SELECT command, result_json FROM tg_idempotency WHERE key = ?",
		key,
	).Scan(&recordedCmd, &resultJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if recordedCmd != command {
		return nil, safety.NewBadArgs(
			"Idempotency key %q was already used for command %q",
			key, recordedCmd,
		)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Record persists a successful write result envelope for later replay. No-op
// when key is empty. Mirrors tgcli.idempotency.record.
func Record(db *sql.DB, key, command, requestID string, result map[string]any) error {
	if key == "" {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT INTO tg_idempotency(key, command, request_id, result_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		key, command, requestID, string(encoded),
		time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	)
	return err
}
