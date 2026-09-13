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

// RemoteCursor is a bounded continuation token for one server-read query.
// It is opaque at the CLI boundary but intentionally contains enough query
// identity to reject accidental cross-account or cross-filter reuse.
type RemoteCursor struct {
	Version   int    `json:"v"`
	Account   string `json:"a"`
	Operation string `json:"o"`
	Chat      int64  `json:"c"`
	Root      int64  `json:"t,omitempty"`
	Query     string `json:"q,omitempty"`
	Sender    int64  `json:"s,omitempty"`
	Media     string `json:"m,omitempty"`
	Since     string `json:"n,omitempty"`
	Until     string `json:"u,omitempty"`
	OffsetID  int64  `json:"i"`
	Reverse   bool   `json:"r,omitempty"`
}

func EncodeRemoteCursor(cursor RemoteCursor) string {
	cursor.Version = 1
	b, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeRemoteCursor(raw string, expected RemoteCursor) (RemoteCursor, error) {
	if raw == "" {
		return expected, nil
	}
	if len(raw) > 2048 {
		return RemoteCursor{}, fmt.Errorf("invalid remote message cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return RemoteCursor{}, fmt.Errorf("invalid remote message cursor")
	}
	var got RemoteCursor
	if err := json.Unmarshal(b, &got); err != nil || got.Version != 1 {
		return RemoteCursor{}, fmt.Errorf("invalid remote message cursor")
	}
	if got.Account != expected.Account || got.Operation != expected.Operation || got.Chat != expected.Chat ||
		got.Root != expected.Root || got.Query != expected.Query || got.Sender != expected.Sender || got.Media != expected.Media ||
		got.Since != expected.Since || got.Until != expected.Until || got.Reverse != expected.Reverse || got.OffsetID <= 0 {
		return RemoteCursor{}, fmt.Errorf("remote cursor does not match account, chat, filters, or order")
	}
	return got, nil
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
