package idempotency

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

// reservationLease bounds crash-stale pending markers while allowing a slow
// ten-item upload of very large media to finish without a duplicate send.
const reservationLease = 24 * time.Hour

// Reserve atomically claims an idempotency key for an album operation. The
// pending marker is stored in the existing result_json column so this remains
// compatible with databases created before album support. A false reserved
// result returns the existing completed or pending envelope.
func Reserve(db *sql.DB, key, command, requestID, fingerprint string) (existing map[string]any, reserved bool, err error) {
	if key == "" {
		return nil, true, nil
	}
	pending := map[string]any{
		"pending":                 true,
		"command":                 command,
		"request_id":              requestID,
		"idempotency_fingerprint": fingerprint,
		"reserved_at":             time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(pending)
	if err != nil {
		return nil, false, err
	}
	_, err = db.Exec(
		`INSERT INTO tg_idempotency(key, command, request_id, result_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		key, command, requestID, string(encoded), time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	)
	if err == nil {
		return nil, true, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		existing, lookupErr := Lookup(db, key, command)
		if lookupErr != nil {
			return nil, false, lookupErr
		}
		if existing == nil {
			return nil, false, err
		}
		if IsPending(existing) {
			storedFingerprint := strings.TrimSpace(fmt.Sprintf("%v", existing["idempotency_fingerprint"]))
			if storedFingerprint != "" && storedFingerprint != fingerprint {
				return nil, false, safety.NewBadArgs("Idempotency key %q was already used for a different album request", key)
			}
		}
		if !IsPending(existing) || !staleReservation(existing) {
			return existing, false, nil
		}
		oldRequest := strings.TrimSpace(fmt.Sprintf("%v", existing["request_id"]))
		if oldRequest == "" {
			return existing, false, nil
		}
		if releaseErr := Release(db, key, command, oldRequest); releaseErr != nil {
			return nil, false, releaseErr
		}
		_, err = db.Exec(
			`INSERT INTO tg_idempotency(key, command, request_id, result_json, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			key, command, requestID, string(encoded), time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		)
		if err == nil {
			return nil, true, nil
		}
	}
	return nil, false, err
}

// IsPending reports whether an idempotency result is an in-flight reservation.
func IsPending(result map[string]any) bool {
	pending, _ := result["pending"].(bool)
	return pending
}

func staleReservation(result map[string]any) bool {
	reservedAt := strings.TrimSpace(fmt.Sprintf("%v", result["reserved_at"]))
	when, err := time.Parse(time.RFC3339Nano, reservedAt)
	return err == nil && time.Since(when) > reservationLease
}

// Finalize replaces an album's pending marker with its successful envelope.
// The request id prevents a different process from finalizing this key.
func Finalize(db *sql.DB, key, command, requestID string, result map[string]any) error {
	if key == "" {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	res, err := db.Exec(
		`UPDATE tg_idempotency SET result_json = ?, created_at = ?
		 WHERE key = ? AND command = ? AND request_id = ?`,
		string(encoded), time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"), key, command, requestID,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("idempotency reservation was not owned by request")
	}
	return nil
}

// Release removes an album reservation after a failure known to occur before
// Telegram committed. Committed failures intentionally retain the marker.
func Release(db *sql.DB, key, command, requestID string) error {
	if key == "" {
		return nil
	}
	var resultJSON string
	err := db.QueryRow(`SELECT result_json FROM tg_idempotency WHERE key = ? AND command = ? AND request_id = ?`, key, command, requestID).Scan(&resultJSON)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var marker map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &marker); err != nil || !IsPending(marker) {
		return nil
	}
	_, err = db.Exec(`DELETE FROM tg_idempotency WHERE key = ? AND command = ? AND request_id = ? AND result_json = ?`, key, command, requestID, resultJSON)
	return err
}

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
