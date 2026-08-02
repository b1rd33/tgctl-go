package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type ExportOptions struct {
	ChatID         int64
	Since          string
	Until          string
	Limit          int
	IncludeDeleted bool
}

// ExportMessages returns a stable oldest-first snapshot for local archive
// writers. It never opens a Telegram client or mutates the database.
func ExportMessages(db *sql.DB, opts ExportOptions) ([]Message, error) {
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
	limit := ""
	if opts.Limit > 0 {
		limit = " LIMIT ?"
		args = append(args, opts.Limit)
	}
	q := fmt.Sprintf(`
		SELECT chat_id, message_id, sender_id, date, text, is_outgoing,
		       reply_to_msg_id, has_media, media_type, media_path, media_id,
		       %s, raw_json
		FROM tg_messages WHERE %s
		ORDER BY message_id ASC%s`, groupedIDProjection(db), strings.Join(where, " AND "), limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		row, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
