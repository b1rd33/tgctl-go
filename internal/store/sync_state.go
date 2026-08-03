package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SyncState is the durable cursor for one account/chat pair. The database is
// already isolated per account, but Account remains explicit so exported
// state and future shared stores cannot silently mix cursors.
type SyncState struct {
	Account       string
	ChatID        int64
	LastMessageID int64
	LastSyncAt    string
	UpdatedAt     string
}

func normalizeSyncAccount(account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "default"
	}
	return account
}

func validateSyncState(state SyncState) error {
	if state.ChatID == 0 {
		return fmt.Errorf("sync state chat_id must not be zero")
	}
	if state.LastMessageID < 0 {
		return fmt.Errorf("sync state last_message_id must not be negative")
	}
	if state.UpdatedAt == "" {
		return fmt.Errorf("sync state updated_at must not be empty")
	}
	return nil
}

// LoadSyncState returns sql.ErrNoRows when no checkpoint exists yet.
func LoadSyncState(db *sql.DB, account string, chatID int64) (SyncState, error) {
	account = normalizeSyncAccount(account)
	var state SyncState
	err := db.QueryRow(`
		SELECT account, chat_id, last_message_id, COALESCE(last_sync_at, ''), updated_at
		FROM tg_sync_state WHERE account = ? AND chat_id = ?`, account, chatID).Scan(
		&state.Account, &state.ChatID, &state.LastMessageID, &state.LastSyncAt, &state.UpdatedAt,
	)
	return state, err
}

// SaveSyncState atomically upserts one checkpoint. Callers should only save a
// cursor after its history page has been persisted successfully.
func SaveSyncState(db *sql.DB, state SyncState) error {
	state.Account = normalizeSyncAccount(state.Account)
	if state.UpdatedAt == "" {
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateSyncState(state); err != nil {
		return err
	}
	_, err := db.Exec(`
		INSERT INTO tg_sync_state(account, chat_id, last_message_id, last_sync_at, updated_at)
		VALUES (?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(account, chat_id) DO UPDATE SET
			last_message_id = excluded.last_message_id,
			last_sync_at = excluded.last_sync_at,
			updated_at = excluded.updated_at`,
		state.Account, state.ChatID, state.LastMessageID, state.LastSyncAt, state.UpdatedAt,
	)
	return err
}

// ListSyncStates returns deterministic account/chat order for diagnostics and
// restart recovery.
func ListSyncStates(db *sql.DB, account string) ([]SyncState, error) {
	account = normalizeSyncAccount(account)
	rows, err := db.Query(`
		SELECT account, chat_id, last_message_id, COALESCE(last_sync_at, ''), updated_at
		FROM tg_sync_state WHERE account = ? ORDER BY chat_id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []SyncState
	for rows.Next() {
		var state SyncState
		if err := rows.Scan(&state.Account, &state.ChatID, &state.LastMessageID, &state.LastSyncAt, &state.UpdatedAt); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}
