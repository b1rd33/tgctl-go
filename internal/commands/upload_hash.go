package commands

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/b1rd33/tgctl-go/internal/store"
)

// recordUploadHash only consumes the digest returned by the immutable upload
// snapshot. Never hash the caller's mutable source file after a confirmed send.
func recordUploadHash(ctx context.Context, db *sql.DB, chatID, messageID int64, path, digest string, size int64, identity string) error {
	if digest == "" {
		return fmt.Errorf("upload snapshot digest unavailable")
	}
	if identity != "" {
		// A live update may already have enriched or replaced the cached media.
		// Never overwrite a known identity just to make a hash indexable.
		if _, err := db.ExecContext(ctx, `UPDATE tg_messages SET media_id=? WHERE chat_id=? AND message_id=? AND media_path=? AND COALESCE(media_id,'')='' AND COALESCE(edit_date,0)=0 AND deleted=0`, identity, chatID, messageID, path); err != nil {
			return err
		}
	}
	if err := store.EnsureMediaHashIndex(db); err != nil {
		return err
	}
	return store.IndexMediaHashKind(ctx, db, chatID, messageID, digest, size, path, identity, "upload_original")
}
