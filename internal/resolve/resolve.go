package resolve

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/text"
)

// ResolveChatDB resolves a user-supplied chat selector using only the local
// SQLite cache. Mirrors tgcli.resolve.resolve_chat_db.
func ResolveChatDB(db *sql.DB, raw string) (int64, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, "", NewNotFound("empty chat selector")
	}
	if strings.EqualFold(value, "self") || strings.EqualFold(value, "me") {
		return resolveSelfDB(db)
	}

	if id, ok := tryInt(value); ok {
		var chatID int64
		var title sql.NullString
		err := db.QueryRow("SELECT chat_id, title FROM tg_chats WHERE chat_id = ?", id).Scan(&chatID, &title)
		if err == sql.ErrNoRows {
			var selfID int64
			if selfErr := db.QueryRow("SELECT user_id FROM tg_me WHERE key='self'").Scan(&selfID); selfErr == nil && selfID == id {
				return selfID, selfTitle(db, selfID), nil
			}
			return 0, "", NewNotFound("chat_id %s not in DB", value)
		}
		if err != nil {
			return 0, "", err
		}
		return chatID, titleOrID(chatID, title), nil
	}

	if strings.HasPrefix(value, "@") {
		username := value[1:]
		if username == "" {
			return 0, "", NewNotFound("empty username")
		}
		rows, err := db.Query("SELECT chat_id,title FROM tg_chats WHERE LOWER(username)=LOWER(?) ORDER BY chat_id LIMIT 2", username)
		if err != nil {
			return 0, "", err
		}
		defer rows.Close()
		var matches [][2]any
		for rows.Next() {
			var id int64
			var title sql.NullString
			if err := rows.Scan(&id, &title); err != nil {
				return 0, "", err
			}
			matches = append(matches, [2]any{id, titleOrID(id, title)})
		}
		if err := rows.Err(); err != nil {
			return 0, "", err
		}
		if len(matches) == 0 {
			var selfID int64
			var selfUsername sql.NullString
			if err := db.QueryRow("SELECT user_id, username FROM tg_me WHERE key='self'").Scan(&selfID, &selfUsername); err == nil && selfUsername.Valid && strings.EqualFold(selfUsername.String, username) {
				return selfID, selfTitle(db, selfID), nil
			}
			return 0, "", NewNotFound("username %s not in DB", value)
		}
		if len(matches) > 1 {
			return 0, "", &Ambiguous{Raw: value, Candidates: matches}
		}
		return matches[0][0].(int64), matches[0][1].(string), nil
	}

	needle := text.StripAccents(value)
	rows, err := db.Query("SELECT chat_id, title FROM tg_chats ORDER BY chat_id")
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	var matches [][2]any
	for rows.Next() {
		var chatID int64
		var title sql.NullString
		if err := rows.Scan(&chatID, &title); err != nil {
			return 0, "", err
		}
		t := title.String
		if strings.Contains(text.StripAccents(t), needle) {
			matches = append(matches, [2]any{chatID, titleOrID(chatID, title)})
		}
	}

	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	if len(matches) == 1 {
		id := matches[0][0].(int64)
		title := matches[0][1].(string)
		return id, title, nil
	}
	if len(matches) == 0 {
		return 0, "", NewNotFound("no chat title contains %q", value)
	}
	return 0, "", &Ambiguous{Raw: value, Candidates: matches}
}

func resolveSelfDB(db *sql.DB) (int64, string, error) {
	var id int64
	if err := db.QueryRow("SELECT user_id FROM tg_me WHERE key='self'").Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", NewNotFound("self identity not in DB; run `tg me` once")
		}
		return 0, "", err
	}
	if id <= 0 {
		return 0, "", NewNotFound("cached self identity is invalid")
	}
	return id, selfTitle(db, id), nil
}

func selfTitle(db *sql.DB, id int64) string {
	var display, username sql.NullString
	if err := db.QueryRow("SELECT display_name, username FROM tg_me WHERE key='self'").Scan(&display, &username); err == nil {
		if display.Valid && strings.TrimSpace(display.String) != "" {
			return display.String
		}
		if username.Valid && strings.TrimSpace(username.String) != "" {
			return "@" + strings.TrimPrefix(strings.TrimSpace(username.String), "@")
		}
	}
	return fmt.Sprintf("user_%d", id)
}

func tryInt(s string) (int64, bool) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func titleOrID(chatID int64, title sql.NullString) string {
	if title.Valid && title.String != "" {
		return title.String
	}
	return fmt.Sprintf("chat_%d", chatID)
}
