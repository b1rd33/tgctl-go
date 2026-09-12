package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type messageCursor struct {
	Chat    int64  `json:"c"`
	Date    string `json:"d"`
	ID      int64  `json:"i"`
	Reverse bool   `json:"r"`
}

func MessageCursor(chat int64, reverse bool, rows []MessageSummary, limit int) string {
	if len(rows) == 0 || len(rows) < limit {
		return ""
	}
	last := rows[len(rows)-1]
	b, _ := json.Marshal(messageCursor{chat, last.Date, last.MessageID, reverse})
	return base64.RawURLEncoding.EncodeToString(b)
}
func cursorClause(raw string, chat int64, reverse bool) (string, []any, error) {
	if raw == "" {
		return "", nil, nil
	}
	if len(raw) > 512 {
		return "", nil, fmt.Errorf("invalid message cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", nil, fmt.Errorf("invalid message cursor")
	}
	var c messageCursor
	if json.Unmarshal(b, &c) != nil || c.Chat != chat || c.Reverse != reverse || c.ID <= 0 {
		return "", nil, fmt.Errorf("cursor does not match chat or order")
	}
	op := "<"
	if reverse {
		op = ">"
	}
	return " AND (date " + op + " ? OR (date = ? AND message_id " + op + " ?))", []any{c.Date, c.Date, c.ID}, nil
}
