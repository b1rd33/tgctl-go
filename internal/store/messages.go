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
	GroupedID  int64
	Date       string
	IsOutgoing bool
	Text       *string
	MediaType  *string
}

// Message mirrors the Python `_full_message` row shape.
type Message struct {
	ChatID        int64
	MessageID     int64
	SenderID      *int64
	Date          string
	Text          *string
	IsOutgoing    bool
	ReplyToMsgID  *int64
	HasMedia      bool
	MediaType     *string
	MediaPath     *string
	MediaIdentity *string
	// GroupedID is Telegram's media-group identifier. Zero means the message
	// is not part of an album; Telegram's grouped_id is nullable on disk.
	GroupedID int64
	RawJSON   *string
}

// LiveMessage is the normalized mutation shape produced by Telegram update
// handling. Deleted mutations only need ChatID/MessageID/Deleted; all other
// fields are retained for idempotent upserts.
type LiveMessage struct {
	ChatID        int64
	MessageID     int64
	SenderID      *int64
	Date          string
	Text          *string
	IsOutgoing    bool
	ReplyToMsgID  *int64
	HasMedia      bool
	MediaType     *string
	MediaPath     *string
	MediaIdentity *string
	GroupedID     int64
	RawJSON       *string
	Deleted       bool
}

// UpsertLiveMessage applies a new/edit mutation without allowing an older
// update to overwrite newer cached text or media metadata. Message dates are
// RFC3339 values, so lexical comparison is chronological for normalized rows.
func UpsertLiveMessage(db *sql.DB, m LiveMessage) error {
	if m.ChatID == 0 || m.MessageID == 0 {
		return fmt.Errorf("live message chat_id and message_id must be positive")
	}
	if m.Date == "" {
		m.Date = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(`
		INSERT INTO tg_messages(
			chat_id, message_id, sender_id, date, text, is_outgoing,
			reply_to_msg_id, has_media, media_type, media_path, media_id, grouped_id, raw_json, deleted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			sender_id = CASE WHEN excluded.date >= tg_messages.date THEN excluded.sender_id ELSE tg_messages.sender_id END,
			date = CASE WHEN excluded.date >= tg_messages.date THEN excluded.date ELSE tg_messages.date END,
			text = CASE WHEN excluded.date >= tg_messages.date THEN excluded.text ELSE tg_messages.text END,
			is_outgoing = CASE WHEN excluded.date >= tg_messages.date THEN excluded.is_outgoing ELSE tg_messages.is_outgoing END,
			reply_to_msg_id = CASE WHEN excluded.date >= tg_messages.date THEN excluded.reply_to_msg_id ELSE tg_messages.reply_to_msg_id END,
			has_media = CASE WHEN excluded.date >= tg_messages.date THEN excluded.has_media ELSE tg_messages.has_media END,
			media_type = CASE WHEN excluded.date >= tg_messages.date THEN COALESCE(excluded.media_type, tg_messages.media_type) ELSE tg_messages.media_type END,
			media_path = CASE WHEN excluded.date >= tg_messages.date THEN COALESCE(excluded.media_path, tg_messages.media_path) ELSE tg_messages.media_path END,
			media_id = CASE WHEN excluded.date >= tg_messages.date THEN COALESCE(excluded.media_id, tg_messages.media_id) ELSE tg_messages.media_id END,
			grouped_id = CASE WHEN excluded.date >= tg_messages.date THEN COALESCE(excluded.grouped_id, tg_messages.grouped_id) ELSE tg_messages.grouped_id END,
			raw_json = CASE WHEN excluded.date >= tg_messages.date THEN COALESCE(excluded.raw_json, tg_messages.raw_json) ELSE tg_messages.raw_json END,
			deleted = CASE WHEN excluded.date >= tg_messages.date THEN excluded.deleted ELSE tg_messages.deleted END`,
		m.ChatID, m.MessageID, optInt64(m.SenderID), m.Date, optStr(m.Text), boolInt(m.IsOutgoing), optInt64(m.ReplyToMsgID), boolInt(m.HasMedia), optStr(m.MediaType), optStr(m.MediaPath), optStr(m.MediaIdentity), nullInt64(m.GroupedID), optStr(m.RawJSON), boolInt(m.Deleted),
	)
	return err
}

// MarkLiveMessagesDeleted tombstones messages without deleting their cached
// text/media fields, preserving auditability and idempotent replay behavior.
func MarkLiveMessagesDeleted(db *sql.DB, chatID int64, messageIDs []int64) error {
	if chatID == 0 || len(messageIDs) == 0 {
		return fmt.Errorf("delete mutation requires chat_id and message ids")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, messageID := range messageIDs {
		if messageID == 0 {
			return fmt.Errorf("delete mutation message id must not be zero")
		}
		if _, err := tx.Exec(`UPDATE tg_messages SET deleted = 1 WHERE chat_id = ? AND message_id = ?`, chatID, messageID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	groupedIDExpr := groupedIDProjection(db)
	order := "DESC"
	if opts.Reverse {
		order = "ASC"
	}
	deletedClause := ""
	if !opts.IncludeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	q := fmt.Sprintf(`
		SELECT message_id, %s, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE chat_id = ?%s
		ORDER BY date %s
		LIMIT ?`,
		groupedIDExpr, deletedClause, order,
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
	groupedIDExpr := groupedIDProjection(db)
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
		SELECT message_id, %s, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE chat_id = ?
		  AND text IS NOT NULL
		  AND text LIKE ? ESCAPE '\'
		  %s%s
		ORDER BY date DESC, message_id DESC
		LIMIT ?`,
		groupedIDExpr, caseClause, deletedClause,
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
	groupedIDExpr := groupedIDProjection(db)
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
		SELECT message_id, %s, date, is_outgoing, text, media_type
		FROM tg_messages
		WHERE %s
		ORDER BY date %s, message_id %s
		LIMIT ?`,
		groupedIDExpr, strings.Join(where, " AND "), order, order,
	)
	return scanSummaries(db, q, args...)
}

// GetOne returns the full message row, or sql.ErrNoRows when absent.
func GetOne(db *sql.DB, chatID, messageID int64, includeDeleted bool) (*Message, error) {
	groupedIDExpr := groupedIDProjection(db)
	deletedClause := ""
	if !includeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	q := fmt.Sprintf(`
		SELECT chat_id, message_id, sender_id, date, text, is_outgoing,
		       reply_to_msg_id, has_media, media_type, media_path, media_id, %s, raw_json
		FROM tg_messages
		WHERE chat_id = ? AND message_id = ?%s`,
		groupedIDExpr, deletedClause,
	)
	var (
		m             Message
		text          sql.NullString
		mediaType     sql.NullString
		mediaPath     sql.NullString
		mediaIdentity sql.NullString
		groupedID     sql.NullInt64
		rawJSON       sql.NullString
		senderID      sql.NullInt64
		replyTo       sql.NullInt64
		hasMediaInt   sql.NullInt64
		isOutgoingI   sql.NullInt64
	)
	err := db.QueryRow(q, chatID, messageID).Scan(
		&m.ChatID, &m.MessageID, &senderID, &m.Date, &text, &isOutgoingI,
		&replyTo, &hasMediaInt, &mediaType, &mediaPath, &mediaIdentity, &groupedID, &rawJSON,
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
	if mediaIdentity.Valid {
		m.MediaIdentity = &mediaIdentity.String
	}
	if groupedID.Valid {
		m.GroupedID = groupedID.Int64
	}
	if rawJSON.Valid {
		m.RawJSON = &rawJSON.String
	}
	return &m, nil
}

// ListAlbum returns the cached messages belonging to one Telegram media
// group, ordered by Telegram message id. Message ids are monotonic within a
// chat and are the only ordering metadata persisted by this schema; no
// synthetic album position is inferred.
func ListAlbum(db *sql.DB, chatID, groupedID int64, includeDeleted bool) ([]Message, error) {
	if groupedID <= 0 {
		return nil, fmt.Errorf("grouped_id must be positive")
	}
	// ConnectReadonly deliberately skips migrations. A legacy database cannot
	// have cached album membership, so report an empty album rather than issuing
	// a query against a column that does not exist.
	if !columnExists(db, "tg_messages", "grouped_id") {
		return []Message{}, nil
	}
	deletedClause := ""
	if !includeDeleted {
		deletedClause = " AND (deleted = 0 OR deleted IS NULL)"
	}
	rows, err := db.Query(fmt.Sprintf(`
		SELECT chat_id, message_id, sender_id, date, text, is_outgoing,
		       reply_to_msg_id, has_media, media_type, media_path, media_id, grouped_id, raw_json
		FROM tg_messages
		WHERE chat_id = ? AND grouped_id = ?%s
		ORDER BY message_id ASC`, deletedClause), chatID, groupedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// groupedIDProjection keeps read-only access compatible with databases made
// before grouped_id was introduced. ConnectReadonly intentionally never runs
// migrations, so a SELECT must use a NULL projection when that column is
// absent. The fixed projection is safe to interpolate into SQL.
func groupedIDProjection(db *sql.DB) string {
	if columnExists(db, "tg_messages", "grouped_id") {
		return "grouped_id"
	}
	return "NULL AS grouped_id"
}

// GetAlbum resolves an anchor message to its media group. An ungrouped
// anchor is returned as a one-item slice so callers can treat single media
// and album downloads uniformly.
func GetAlbum(db *sql.DB, chatID, messageID int64, includeDeleted bool) ([]Message, error) {
	anchor, err := GetOne(db, chatID, messageID, includeDeleted)
	if err != nil {
		return nil, err
	}
	if anchor.GroupedID <= 0 {
		return []Message{*anchor}, nil
	}
	return ListAlbum(db, chatID, anchor.GroupedID, includeDeleted)
}

type messageScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row messageScanner) (Message, error) {
	var (
		m             Message
		text          sql.NullString
		mediaType     sql.NullString
		mediaPath     sql.NullString
		mediaIdentity sql.NullString
		groupedID     sql.NullInt64
		rawJSON       sql.NullString
		senderID      sql.NullInt64
		replyTo       sql.NullInt64
		hasMediaInt   sql.NullInt64
		isOutgoingI   sql.NullInt64
	)
	if err := row.Scan(
		&m.ChatID, &m.MessageID, &senderID, &m.Date, &text, &isOutgoingI,
		&replyTo, &hasMediaInt, &mediaType, &mediaPath, &mediaIdentity, &groupedID, &rawJSON,
	); err != nil {
		return Message{}, err
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
	if mediaIdentity.Valid {
		m.MediaIdentity = &mediaIdentity.String
	}
	if groupedID.Valid {
		m.GroupedID = groupedID.Int64
	}
	if rawJSON.Valid {
		m.RawJSON = &rawJSON.String
	}
	return m, nil
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
		var groupedID sql.NullInt64
		var isOut sql.NullInt64
		if err := rows.Scan(&s.MessageID, &groupedID, &s.Date, &isOut, &text, &mediaType); err != nil {
			return nil, err
		}
		if groupedID.Valid {
			s.GroupedID = groupedID.Int64
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
			reply_to_msg_id, has_media, media_type, media_path, media_id, grouped_id, raw_json, deleted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		m.ChatID, m.MessageID, optInt64(m.SenderID), m.Date, optStr(m.Text),
		boolInt(m.IsOutgoing), optInt64(m.ReplyToMsgID), boolInt(m.HasMedia),
		optStr(m.MediaType), optStr(m.MediaPath), optStr(m.MediaIdentity), nullInt64(m.GroupedID), optStr(m.RawJSON),
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
			media_id = NULL,
			deleted = 0`,
		chatID, messageID, time.Now().UTC().Format(time.RFC3339), nullString(text), mediaType, mediaPath,
	)
	return err
}

// UploadedMedia is one message row produced by a successful multi-item
// upload. The command layer commits the complete slice in one transaction so
// a partially persisted album cannot be mistaken for a successful send.
type UploadedMedia struct {
	MessageID int64
	Text      string
	MediaType string
	MediaPath string
	GroupedID int64
}

// RecordUploadedAlbum atomically records every message created by one album.
// It intentionally uses the existing tg_messages rows; Telegram already
// represents an album as one message per item.
func RecordUploadedAlbum(db *sql.DB, chatID int64, items []UploadedMedia) error {
	if len(items) == 0 {
		return fmt.Errorf("album must contain at least one uploaded message")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range items {
		if item.MessageID <= 0 {
			return fmt.Errorf("album message id must be positive")
		}
		if _, err := tx.Exec(`
			INSERT INTO tg_messages(
				chat_id, message_id, date, text, is_outgoing,
				has_media, media_type, media_path, grouped_id, deleted
			) VALUES (?, ?, ?, ?, 1, 1, ?, ?, ?, 0)
			ON CONFLICT(chat_id, message_id) DO UPDATE SET
				date = excluded.date,
				text = excluded.text,
				is_outgoing = 1,
				has_media = 1,
				media_type = excluded.media_type,
				media_path = excluded.media_path,
				grouped_id = excluded.grouped_id,
				media_id = NULL,
				deleted = 0`,
			chatID, item.MessageID, time.Now().UTC().Format(time.RFC3339), nullString(item.Text), item.MediaType, item.MediaPath, nullInt64(item.GroupedID),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateMessageMediaPath updates only the media cache fields for one existing
// message. It returns sql.ErrNoRows when the composite message identity is not
// cached.
func UpdateMessageMediaPath(db *sql.DB, chatID, messageID int64, mediaType, mediaPath string) error {
	result, err := db.Exec(`
		UPDATE tg_messages
		SET has_media = 1, media_type = ?, media_path = ?, media_id = NULL
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
// message was already cached. It updates existing rows first; if no row exists,
// a conditional UPSERT inserts the minimal record or changes only the media
// fields when another writer wins the insert race, preserving richer data.
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
			media_path = excluded.media_path,
			media_id = NULL`,
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

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// MarkDeleted is a test helper that sets deleted=1 on a row.
func MarkDeleted(db *sql.DB, chatID, messageID int64) error {
	_, err := db.Exec(
		"UPDATE tg_messages SET deleted=1 WHERE chat_id=? AND message_id=?",
		chatID, messageID,
	)
	return err
}
