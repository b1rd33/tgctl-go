package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// indexDownloadedMediaHash records the exact bytes of a newly persisted
// download. The expected cache identity is captured before hashing by the
// caller and is checked again by the store transaction, so a concurrent cache
// replacement cannot attach the digest to the wrong Telegram message.
//
// Hash indexing is deliberately a best-effort post-download step. Callers
// should surface its error as a warning while retaining the successful
// download result.
func indexDownloadedMediaHash(ctx context.Context, db *sql.DB, chatID, messageID int64, artifact media.DownloadedArtifact, expectedIdentity string, producerIdentity media.ArtifactIdentity) error {
	if err := store.EnsureMediaHashIndex(db); err != nil {
		return fmt.Errorf("ensure media hash index: %w", err)
	}
	// Keep the path and identity snapshot separate from the hash operation.
	// HashFile validates that the artifact itself remains stable while read.
	verified, err := media.InspectDownloadedArtifactWithIdentity(filepath.Dir(artifact.Path), artifact.Path, producerIdentity)
	if err != nil {
		return fmt.Errorf("verify downloaded media identity: %w", err)
	}
	artifact = verified
	expectedPath := artifact.Path
	sha, size, err := media.HashFile(ctx, expectedPath, 0)
	if err != nil {
		return fmt.Errorf("hash downloaded media: %w", err)
	}
	if size != artifact.Size {
		return fmt.Errorf("downloaded media size changed before indexing")
	}
	if _, err := media.InspectDownloadedArtifactWithIdentity(filepath.Dir(artifact.Path), artifact.Path, producerIdentity); err != nil {
		return fmt.Errorf("verify downloaded media identity after hashing: %w", err)
	}
	if err := store.IndexMediaHashKind(ctx, db, chatID, messageID, sha, size, expectedPath, expectedIdentity, "downloaded"); err != nil {
		return fmt.Errorf("store downloaded media hash: %w", err)
	}
	return nil
}

// bindDownloadedMediaIdentity fills the cache identity for a previously
// uncached message only when its path is still the one persisted by this
// download. A known identity mismatch is rejected, because indexing under a
// stale identity would make later lookups return the wrong bytes.
func bindDownloadedMediaIdentity(db *sql.DB, chatID, messageID int64, path, expectedIdentity, downloadedIdentity string) (string, error) {
	if downloadedIdentity == "" {
		return expectedIdentity, nil
	}
	if expectedIdentity != "" && expectedIdentity != downloadedIdentity {
		return "", fmt.Errorf("downloaded media identity differs from the cache snapshot")
	}
	if expectedIdentity != "" {
		return expectedIdentity, nil
	}
	res, err := db.Exec(`
		UPDATE tg_messages
		SET media_id=?
		WHERE chat_id=? AND message_id=? AND COALESCE(media_path,'')=?
		  AND COALESCE(media_id,'')='' AND COALESCE(edit_date,0)=0 AND deleted=0`,
		downloadedIdentity, chatID, messageID, path)
	if err != nil {
		return "", fmt.Errorf("bind downloaded media identity: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		return "", fmt.Errorf("confirm downloaded media identity: %w", err)
	} else if affected == 0 {
		row, lookupErr := store.GetOne(db, chatID, messageID, false)
		if lookupErr != nil || row.MediaPath == nil || *row.MediaPath != path || row.MediaIdentity == nil || *row.MediaIdentity != downloadedIdentity {
			return "", fmt.Errorf("downloaded media identity could not be bound safely")
		}
	}
	return downloadedIdentity, nil
}

const downloadedMediaHashWarning = "exact media hash indexing failed; download succeeded and can be indexed later with media-index"

// indexBackfillDownloadedHashes indexes only media freshly downloaded during
// this backfill. Cached/skipped files are intentionally excluded because the
// downloader did not verify those bytes against Telegram in this operation.
// Any local indexing failure is returned as a warning so committed backfill
// rows remain a successful operation.
func indexBackfillDownloadedHashes(ctx context.Context, dbPath string, chatID int64, rows []client.BackfillMessage) []string {
	hasDownloads := false
	for _, row := range rows {
		if row.MediaDisposition == client.BackfillMediaDownloaded && strings.TrimSpace(row.MediaPath) != "" {
			hasDownloads = true
			break
		}
	}
	if !hasDownloads {
		return nil
	}

	db, err := store.Connect(dbPath)
	if err != nil {
		return []string{downloadedMediaHashWarning}
	}
	failures := 0
	for _, source := range rows {
		if source.MediaDisposition != client.BackfillMediaDownloaded || strings.TrimSpace(source.MediaPath) == "" {
			continue
		}
		cached, lookupErr := store.GetOne(db, chatID, source.MessageID, false)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			// A database cap or duplicate filtering may have excluded this
			// source row. There is no cache record to index.
			continue
		}
		if lookupErr != nil || cached.MediaPath == nil || *cached.MediaPath != source.MediaPath {
			failures++
			continue
		}
		expectedIdentity := source.MediaIdentity
		if cached.MediaIdentity != nil && *cached.MediaIdentity != expectedIdentity {
			failures++
			continue
		}
		producerIdentity, artifact, inspectErr := media.CaptureArtifactIdentity(filepath.Dir(source.MediaPath), source.MediaPath)
		if inspectErr != nil || indexDownloadedMediaHash(ctx, db, chatID, source.MessageID, artifact, expectedIdentity, producerIdentity) != nil {
			failures++
		}
	}
	if closeErr := db.Close(); closeErr != nil {
		failures++
	}
	if failures == 0 {
		return nil
	}
	return []string{downloadedMediaHashWarning}
}
