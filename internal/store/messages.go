package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MessageSummary mirrors the Python `_message_summary` row shape.
type MessageSummary struct {
	MessageID  int64
	Date       string
	IsOutgoing bool
	Text       *string
	MediaType  *string
}

// Message mirrors the Python `_full_message` row shape.
type Message struct {
	ChatID       int64
	MessageID    int64
	SenderID     *int64
	Date         string
	Text         *string
	IsOutgoing   bool
	ReplyToMsgID *int64
	HasMedia     bool
	MediaType    *string
	MediaPath    *string
	RawJSON      *string
}

// ShowOptions mirrors the Python `show` arg surface.
type ShowOptions struct {
	ChatID         int64
	Limit          int
	Reverse        bool
	IncludeDeleted bool
}

// Show returns up to Limit messages for ChatID. When IncludeDeleted is false
// (the Python default), tombstoned rows are excluded.
func Show(db *sql.DB, opts ShowOptions) ([]MessageSummary, error) {
	order := "DESC"
	if opts.Reverse {
		order = "ASC"
	}
	deletedClause := ""
	if !opts.IncludeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	q := fmt.Sprintf(`
		SELECT message_id, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE chat_id = ?%s
		ORDER BY date %s
		LIMIT ?`,
		deletedClause, order,
	)
	return scanSummaries(db, q, opts.ChatID, opts.Limit)
}

// SearchOptions mirrors the Python `search` arg surface (sans chat resolver).
type SearchOptions struct {
	ChatID         int64
	Query          string
	CaseSensitive  bool
	Limit          int
	IncludeDeleted bool
}

// Search filters tg_messages by chat + LIKE pattern, optionally case-sensitive.
func Search(db *sql.DB, opts SearchOptions) ([]MessageSummary, error) {
	pattern := likePattern(opts.Query)
	args := []any{opts.ChatID, pattern}
	caseClause := ""
	if opts.CaseSensitive {
		caseClause = " AND instr(text, ?) > 0"
		args = append(args, opts.Query)
	}
	deletedClause := ""
	if !opts.IncludeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	args = append(args, opts.Limit)
	q := fmt.Sprintf(`
		SELECT message_id, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE chat_id = ?
		  AND text IS NOT NULL
		  AND text LIKE ? ESCAPE '\'
		  %s%s
		ORDER BY date DESC, message_id DESC
		LIMIT ?`,
		caseClause, deletedClause,
	)
	return scanSummaries(db, q, args...)
}

// likePattern escapes %, _, and \ for SQLite LIKE with ESCAPE '\'.
// Mirrors Python `_like_pattern`.
func likePattern(query string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(query) + "%"
}

// ListOptions mirrors `list-msgs` with optional inclusive day boundaries.
type ListOptions struct {
	ChatID         int64
	Since          string // empty = no lower bound (already in YYYY-MM-DDT00:00:00 form)
	Until          string // empty = no upper bound (in YYYY-MM-DDT23:59:59 form)
	Limit          int
	Reverse        bool
	IncludeDeleted bool
}

// List filters by chat + optional date range.
func List(db *sql.DB, opts ListOptions) ([]MessageSummary, error) {
	order := "DESC"
	if opts.Reverse {
		order = "ASC"
	}
	where := []string{"chat_id = ?"}
	args := []any{opts.ChatID}
	if opts.Since != "" {
		where = append(where, "date >= ?")
		args = append(args, opts.Since)
	}
	if opts.Until != "" {
		where = append(where, "date <= ?")
		args = append(args, opts.Until)
	}
	if !opts.IncludeDeleted {
		where = append(where, "(deleted = 0 OR deleted IS NULL)")
	}
	args = append(args, opts.Limit)
	q := fmt.Sprintf(`
		SELECT message_id, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE %s
		ORDER BY date %s, message_id %s
		LIMIT ?`,
		strings.Join(where, " AND "), order, order,
	)
	return scanSummaries(db, q, args...)
}

// GetOne returns the full message row, or sql.ErrNoRows when absent.
func GetOne(db *sql.DB, chatID, messageID int64, includeDeleted bool) (*Message, error) {
	deletedClause := ""
	if !includeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	q := fmt.Sprintf(`
		SELECT chat_id, message_id, sender_id, date, text, is_outgoing,
		       reply_to_msg_id, has_media, media_type, media_path, raw_json
		FROM tg_messages
		WHERE chat_id = ? AND message_id = ?%s`,
		deletedClause,
	)
	var (
		m           Message
		text        sql.NullString
		mediaType   sql.NullString
		mediaPath   sql.NullString
		rawJSON     sql.NullString
		senderID    sql.NullInt64
		replyTo     sql.NullInt64
		hasMediaInt sql.NullInt64
		isOutgoingI sql.NullInt64
	)
	err := db.QueryRow(q, chatID, messageID).Scan(
		&m.ChatID, &m.MessageID, &senderID, &m.Date, &text, &isOutgoingI,
		&replyTo, &hasMediaInt, &mediaType, &mediaPath, &rawJSON,
	)
	if err != nil {
		return nil, err
	}
	if senderID.Valid {
		m.SenderID = &senderID.Int64
	}
	if replyTo.Valid {
		m.ReplyToMsgID = &replyTo.Int64
	}
	m.IsOutgoing = isOutgoingI.Valid && isOutgoingI.Int64 != 0
	m.HasMedia = hasMediaInt.Valid && hasMediaInt.Int64 != 0
	if text.Valid && text.String != "" {
		m.Text = &text.String
	}
	if mediaType.Valid {
		m.MediaType = &mediaType.String
	}
	if mediaPath.Valid {
		m.MediaPath = &mediaPath.String
	}
	if rawJSON.Valid {
		m.RawJSON = &rawJSON.String
	}
	return &m, nil
}

func scanSummaries(db *sql.DB, q string, args ...any) ([]MessageSummary, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageSummary
	for rows.Next() {
		var s MessageSummary
		var text, mediaType sql.NullString
		var isOut sql.NullInt64
		if err := rows.Scan(&s.MessageID, &s.Date, &isOut, &text, &mediaType); err != nil {
			return nil, err
		}
		s.IsOutgoing = isOut.Valid && isOut.Int64 != 0
		if text.Valid && text.String != "" {
			s.Text = &text.String
		}
		if mediaType.Valid {
			s.MediaType = &mediaType.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// InsertMessage is a test helper for seeding tg_messages.
func InsertMessage(db *sql.DB, m Message) error {
	_, err := db.Exec(`
		INSERT INTO tg_messages(
			chat_id, message_id, sender_id, date, text, is_outgoing,
			reply_to_msg_id, has_media, media_type, media_path, raw_json, deleted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		m.ChatID, m.MessageID, optInt64(m.SenderID), m.Date, optStr(m.Text),
		boolInt(m.IsOutgoing), optInt64(m.ReplyToMsgID), boolInt(m.HasMedia),
		optStr(m.MediaType), optStr(m.MediaPath), optStr(m.RawJSON),
	)
	return err
}

// RecordUploadedMedia upserts the local cache row for a message created by an upload.
func RecordUploadedMedia(db *sql.DB, chatID, messageID int64, text, mediaType, mediaPath string) error {
	_, err := db.Exec(`
		INSERT INTO tg_messages(
			chat_id, message_id, date, text, is_outgoing,
			has_media, media_type, media_path, deleted
		) VALUES (?, ?, ?, ?, 1, 1, ?, ?, 0)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			date = excluded.date,
			text = excluded.text,
			is_outgoing = 1,
			has_media = 1,
			media_type = excluded.media_type,
			media_path = excluded.media_path,
			deleted = 0`,
		chatID, messageID, time.Now().UTC().Format(time.RFC3339), nullString(text), mediaType, mediaPath,
	)
	return err
}

// UpdateMessageMediaPath updates only the media cache fields for one existing
// message. It returns sql.ErrNoRows when the composite message identity is not
// cached.
func UpdateMessageMediaPath(db *sql.DB, chatID, messageID int64, mediaType, mediaPath string) error {
	result, err := db.Exec(`
		UPDATE tg_messages
		SET has_media = 1, media_type = ?, media_path = ?
		WHERE chat_id = ? AND message_id = ?`,
		mediaType, mediaPath, chatID, messageID,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// StoreMessageMediaPath records downloaded media whether or not the live
// message was already cached. The single UPSERT is race-safe: if another
// writer inserts a richer row first, the conflict arm changes only the media
// fields and preserves all other message data.
func StoreMessageMediaPath(db *sql.DB, chatID, messageID int64, messageDate time.Time, mediaType, mediaPath string) error {
	if err := UpdateMessageMediaPath(db, chatID, messageID, mediaType, mediaPath); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if messageDate.IsZero() {
		return fmt.Errorf("cannot cache uncached message without authoritative message date")
	}
	_, err := db.Exec(`
		INSERT INTO tg_messages(
			chat_id, message_id, date, has_media, media_type, media_path, deleted
		) VALUES (?, ?, ?, 1, ?, ?, 0)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			has_media = 1,
			media_type = excluded.media_type,
			media_path = excluded.media_path`,
		chatID, messageID, messageDate.UTC().Format(time.RFC3339), mediaType, mediaPath,
	)
	return err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func optStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func optInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// MarkDeleted is a test helper that sets deleted=1 on a row.
func MarkDeleted(db *sql.DB, chatID, messageID int64) error {
	_, err := db.Exec(
		"UPDATE tg_messages SET deleted=1 WHERE chat_id=? AND message_id=?",
		chatID, messageID,
	)
	return err
}
