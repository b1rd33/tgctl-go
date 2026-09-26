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
