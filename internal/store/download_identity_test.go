package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVerifiedDownloadPreservesUploadHashAndRejectsStaleResult(t *testing.T) {
	db := setupMessages(t)
	ctx := context.Background()
	identity, kind, path := "document:70", "document", "/old/original"
	if err := InsertMessage(db, Message{ChatID: 1, MessageID: 700, Date: "2026-09-01T00:00:00Z", HasMedia: true, MediaType: &kind, MediaPath: &path, MediaIdentity: &identity}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := IndexMediaHashKind(ctx, db, 1, 700, sha, 3, path, identity, "upload_original"); err != nil {
		t.Fatal(err)
	}
	if err := StoreVerifiedDownload(db, 1, 700, time.Now(), kind, "/new/download", identity, identity); err != nil {
		t.Fatal(err)
	}
	if err := IndexMediaHashKind(ctx, db, 1, 700, sha, 3, "/new/download", identity, "downloaded"); err != nil {
		t.Fatal(err)
	}
	matches, err := FindMediaHash(ctx, db, sha, 10)
	if err != nil || len(matches) != 2 {
		t.Fatalf("matches=%v err=%v", matches, err)
	}
	// These paths do not exist: lookup depends on cached identity, not file existence.
	if _, err := db.Exec("UPDATE tg_messages SET media_id='document:71',edit_date=2 WHERE message_id=700"); err != nil {
		t.Fatal(err)
	}
	if err := StoreVerifiedDownload(db, 1, 700, time.Now(), kind, "/stale", identity, identity); !errors.Is(err, ErrDownloadedMediaChanged) {
		t.Fatalf("err=%v", err)
	}
	if err := StoreVerifiedDownload(db, 1, 700, time.Now(), kind, "/unknown", "", ""); !errors.Is(err, ErrDownloadedMediaChanged) {
		t.Fatal("unknown download erased concurrent identity")
	}
	matches, err = FindMediaHash(ctx, db, sha, 10)
	if err != nil || len(matches) != 0 {
		t.Fatal("stale result visible")
	}
	if _, err := db.Exec("UPDATE tg_messages SET deleted=1 WHERE message_id=700"); err != nil {
		t.Fatal(err)
	}
	if err := StoreVerifiedDownload(db, 1, 700, time.Now(), kind, "/deleted", "document:71", "document:71"); !errors.Is(err, ErrDownloadedMediaChanged) {
		t.Fatal("deleted message overwritten")
	}
	if err := StoreVerifiedDownload(db, 1, 701, time.Now(), kind, "/fresh", "", identity); err != nil {
		t.Fatal(err)
	}
	if err := StoreVerifiedDownload(db, 1, 702, time.Now(), kind, "/mismatch", identity, "document:72"); !errors.Is(err, ErrDownloadedMediaChanged) {
		t.Fatal("snapshot mismatch accepted")
	}
}
