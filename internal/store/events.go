package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

// A receipt binds an acknowledgement to the account, cache location, row and
// exact persisted payload. It is a consistency token, not a credential.
type eventReceipt struct {
	Version int    `json:"v"`
	Scope   string `json:"s"`
	ID      int64  `json:"i"`
	Digest  string `json:"h"`
}

type PendingEvent struct {
	EventID int64           `json:"event_id"`
	Receipt string          `json:"receipt"`
	Event   json.RawMessage `json:"event"`
}

func EventScope(dbPath, account string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(account+"\x00"+filepath.Clean(dbPath))))
}

func pendingEvent(scope string, id int64, raw string) (PendingEvent, error) {
	if id <= 0 || len(raw) > 256*1024 || !json.Valid([]byte(raw)) {
		return PendingEvent{}, errors.New("invalid or oversized pending event; cache inspection required")
	}
	receipt := eventReceipt{Version: 1, Scope: scope, ID: id, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))}
	b, err := json.Marshal(receipt)
	if err != nil {
		return PendingEvent{}, err
	}
	return PendingEvent{EventID: id, Receipt: base64.RawURLEncoding.EncodeToString(b), Event: json.RawMessage(raw)}, nil
}

// PendingEvents does not consume events and does not acquire the live session.
func PendingEvents(ctx context.Context, db *sql.DB, scope string, limit int) ([]PendingEvent, bool, error) {
	if limit < 1 || limit > 100 {
		return nil, false, safety.NewBadArgs("limit must be between 1 and 100")
	}
	rows, err := db.QueryContext(ctx, "SELECT id,substr(event,1,262145) FROM tg_event_outbox ORDER BY id LIMIT ?", limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]PendingEvent, 0, limit)
	more := false
	for rows.Next() {
		if len(result) == limit {
			more = true
			break
		}
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, false, err
		}
		e, err := pendingEvent(scope, id, raw)
		if err != nil {
			return nil, false, err
		}
		result = append(result, e)
	}
	return result, more, rows.Err()
}

func PendingEventReceipt(ctx context.Context, db *sql.DB, scope string, id int64) (string, error) {
	var raw string
	if err := db.QueryRowContext(ctx, "SELECT substr(event,1,262145) FROM tg_event_outbox WHERE id=?", id).Scan(&raw); err != nil {
		return "", err
	}
	e, err := pendingEvent(scope, id, raw)
	return e.Receipt, err
}

func decodeEventReceipt(token, scope string) (eventReceipt, error) {
	var v eventReceipt
	bad := safety.NewBadArgs("invalid event receipt or wrong account/cache")
	if len(token) > 1024 {
		return v, bad
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return v, bad
	}
	if json.Unmarshal(b, &v) != nil || v.Version != 1 || v.Scope != scope || v.ID <= 0 {
		return v, bad
	}
	hash, err := hex.DecodeString(v.Digest)
	if err != nil || len(hash) != 32 {
		return v, bad
	}
	return v, nil
}

// AcknowledgePendingEvent removes one exact receipt after the caller has stored
// the event. Missing rows are an idempotent already-absent result, not proof of
// delivery. There is no bulk acknowledgement or remote Telegram action.
func AcknowledgePendingEvent(ctx context.Context, db *sql.DB, scope, token string, dryRun bool) (int64, bool, error) {
	v, err := decodeEventReceipt(token, scope)
	if err != nil {
		return 0, false, err
	}
	// Serialize verification and deletion so a replaced row can never be
	// mistaken for an already-acknowledged event. Read-only previews need no lock.
	query := db.QueryRowContext
	var tx *sql.Tx
	if !dryRun {
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer tx.Rollback()
		query = tx.QueryRowContext
	}
	var raw string
	err = query(ctx, "SELECT substr(event,1,262145) FROM tg_event_outbox WHERE id=?", v.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v.ID, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(raw) > 256*1024 || fmt.Sprintf("%x", sha256.Sum256([]byte(raw))) != v.Digest {
		return 0, false, safety.NewBadArgs("pending event differs from receipt; inspect again")
	}
	if dryRun {
		return v.ID, true, nil
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM tg_event_outbox WHERE id=? AND event=?", v.ID, raw)
	if err != nil {
		return 0, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n != 1 {
		return 0, false, errors.New("pending event changed during acknowledgement")
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return v.ID, true, nil
}
