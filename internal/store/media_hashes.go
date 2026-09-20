package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// MediaHashMatch identifies a locally indexed media file and the Telegram
// message that owns it. Original upload paths are historical; downloaded and
// manually indexed paths must still match the cache. Every representation
// requires the cached Telegram media identity to remain unchanged.
type MediaHashMatch struct {
	Representation string `json:"representation"`
	ChatID         int64  `json:"chat_id"`
	MessageID      int64  `json:"message_id"`
	SHA256         string `json:"sha256"`
	Bytes          int64  `json:"bytes"`
	MediaType      string `json:"media_type"`
	MediaPath      string `json:"media_path"`
}

// EnsureMediaHashIndex creates the optional media hash table. It is
// deliberately separate from Schema: existing caches do not gain a table
// unless a caller opts into media hashing.
func EnsureMediaHashIndex(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS tg_media_hashes (
			representation TEXT NOT NULL,
 chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			sha256 TEXT NOT NULL,
			bytes INTEGER NOT NULL CHECK(bytes >= 0),
			media_type TEXT NOT NULL DEFAULT '',
			media_path TEXT NOT NULL,
			media_id TEXT,
			indexed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (chat_id, message_id, representation)
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_media_hashes_sha256 ON tg_media_hashes(sha256)`); err != nil {
		return err
	}
	return tx.Commit()
}

// IndexMediaHash snapshots the current media identity and path for one
// non-deleted cached message and atomically upserts its hash record. The
// expected path and identity must still match the cached message, preventing
// a hash captured from an older local file from being attached after the
// message changes. The optional index must have been created with
// EnsureMediaHashIndex first.
func IndexMediaHash(ctx context.Context, db *sql.DB, chatID, messageID int64, sha string, size int64, expectedPath, expectedIdentity string) error {
	return IndexMediaHashKind(ctx, db, chatID, messageID, sha, size, expectedPath, expectedIdentity, "cached_file")
}

func IndexMediaHashKind(ctx context.Context, db *sql.DB, chatID, messageID int64, sha string, size int64, expectedPath, expectedIdentity, representation string) error {
	if representation != "cached_file" && representation != "downloaded" && representation != "upload_original" {
		return fmt.Errorf("invalid hash representation")
	}
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	normalized, err := normalizeSHA256(sha)
	if err != nil {
		return err
	}
	if chatID == 0 {
		return fmt.Errorf("chat_id must not be zero")
	}
	if messageID <= 0 {
		return fmt.Errorf("message_id must be positive")
	}
	if size < 0 {
		return fmt.Errorf("media size must not be negative")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var mediaID, mediaPath, mediaType sql.NullString
	var hasMedia, deleted int64
	messageQuery := `
		SELECT has_media, deleted, media_id, media_path, media_type
		FROM tg_messages
		WHERE chat_id = ? AND message_id = ?`
	messageQuery += ` AND COALESCE(media_path, '') = ? AND COALESCE(media_id, '') = ?`
	messageArgs := []any{chatID, messageID, expectedPath, expectedIdentity}
	err = tx.QueryRowContext(ctx, messageQuery, messageArgs...).
		Scan(&hasMedia, &deleted, &mediaID, &mediaPath, &mediaType)
	if err != nil {
		return err
	}
	if deleted != 0 || hasMedia == 0 {
		return fmt.Errorf("message %d/%d has no active media", chatID, messageID)
	}
	if !mediaPath.Valid || strings.TrimSpace(mediaPath.String) == "" {
		return fmt.Errorf("message %d/%d has no media path", chatID, messageID)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tg_media_hashes(chat_id, message_id, sha256, bytes, media_type, media_path, media_id, representation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id, representation) DO UPDATE SET
			sha256 = excluded.sha256,
			bytes = excluded.bytes,
			media_type = excluded.media_type,
			media_path = excluded.media_path,
			media_id = excluded.media_id,
			indexed_at = CURRENT_TIMESTAMP`,
		chatID, messageID, normalized, size, mediaType.String, mediaPath.String, nullString(mediaID.String), representation); err != nil {
		return err
	}
	return tx.Commit()
}

// FindMediaHash returns current matches for a digest. Entries are joined back
// to tg_messages so deleted messages or changed media identities cannot produce
// stale results. Original upload paths survive a later download to another path. A cache without the optional table is an empty read.
func FindMediaHash(ctx context.Context, db *sql.DB, sha string, limit int) ([]MediaHashMatch, error) {
	if db == nil {
		return nil, fmt.Errorf("database is nil")
	}
	normalized, err := normalizeSHA256(sha)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("limit must not be negative")
	}
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='tg_media_hashes'`).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return []MediaHashMatch{}, nil
		}
		return nil, err
	}
	query := `
		SELECT h.chat_id, h.message_id, h.sha256, h.bytes, h.media_type, h.media_path, h.representation
		FROM tg_media_hashes h
		JOIN tg_messages m ON m.chat_id = h.chat_id AND m.message_id = h.message_id
		WHERE h.sha256 = ?
		  AND COALESCE(m.deleted, 0) = 0
		  AND COALESCE(m.has_media, 0) != 0
		  AND (h.representation = 'upload_original' OR COALESCE(m.media_path, '') = h.media_path)
		  AND COALESCE(h.media_id, '') = COALESCE(m.media_id, '')
		ORDER BY h.chat_id, h.message_id, h.representation`
	args := []any{normalized}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := make([]MediaHashMatch, 0)
	for rows.Next() {
		var match MediaHashMatch
		if err := rows.Scan(&match.ChatID, &match.MessageID, &match.SHA256, &match.Bytes, &match.MediaType, &match.MediaPath, &match.Representation); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func normalizeSHA256(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return "", fmt.Errorf("sha256 must be 64 hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("sha256 must be 64 hexadecimal characters")
	}
	return strings.ToLower(value), nil
}
