package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrDownloadedMediaChanged = errors.New("downloaded media differs from current cache identity")

// StoreVerifiedDownload publishes the path and its Telegram identity together.
// A newer replacement/deletion must not be overwritten by an older download.
func StoreVerifiedDownload(db *sql.DB, chatID, messageID int64, date time.Time, kind, path, expected, identity string) error {
	if expected != "" && expected != identity {
		return ErrDownloadedMediaChanged
	}
	if date.IsZero() {
		// Existing rows already have an authoritative message date.
		var existing string
		if err := db.QueryRow("SELECT date FROM tg_messages WHERE chat_id=? AND message_id=?", chatID, messageID).Scan(&existing); err != nil {
			return fmt.Errorf("download has no authoritative date: %w", err)
		}
		var err error
		date, err = time.Parse(time.RFC3339, existing)
		if err != nil {
			return err
		}
	}
	res, err := db.Exec(`INSERT INTO tg_messages(chat_id,message_id,date,has_media,media_type,media_path,media_id,deleted)
 VALUES(?,?,?,1,?,?,?,0)
 ON CONFLICT(chat_id,message_id) DO UPDATE SET has_media=1,media_type=excluded.media_type,media_path=excluded.media_path,media_id=excluded.media_id
 WHERE deleted=0 AND (media_id=excluded.media_id OR (COALESCE(media_id,'')='' AND COALESCE(edit_date,0)=0))`, chatID, messageID, date.UTC().Format(time.RFC3339), kind, path, nullString(identity))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDownloadedMediaChanged
	}
	return nil
}
