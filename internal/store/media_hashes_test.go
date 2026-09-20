package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindMediaHashWithoutOptionalTableIsEmpty(t *testing.T) {
	db := testMediaHashDB(t)
	matches, err := FindMediaHash(context.Background(), db, strings.Repeat("a", 64), 10)
	if err != nil {
		t.Fatalf("FindMediaHash: %v", err)
	}
	if matches == nil || len(matches) != 0 {
		t.Fatalf("matches = %#v, want empty non-nil slice", matches)
	}
}

func TestIndexAndFindMediaHashUpsertsAndAllowsDuplicateBytes(t *testing.T) {
	db := testMediaHashDB(t)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatalf("EnsureMediaHashIndex: %v", err)
	}
	identity := "photo-1"
	path := "/media/one.jpg"
	if err := InsertMessage(db, Message{ChatID: 1, MessageID: 10, Date: "2026-01-01T00:00:00Z", HasMedia: true, MediaType: strPtr("photo"), MediaPath: &path, MediaIdentity: &identity}); err != nil {
		t.Fatalf("InsertMessage one: %v", err)
	}
	identity2 := "photo-2"
	path2 := "/media/two.jpg"
	if err := InsertMessage(db, Message{ChatID: 2, MessageID: 20, Date: "2026-01-01T00:00:00Z", HasMedia: true, MediaType: strPtr("photo"), MediaPath: &path2, MediaIdentity: &identity2}); err != nil {
		t.Fatalf("InsertMessage two: %v", err)
	}
	sha := strings.Repeat("AB", 32)
	if err := IndexMediaHash(context.Background(), db, 1, 10, sha, 123, path, identity); err != nil {
		t.Fatalf("IndexMediaHash one: %v", err)
	}
	if err := IndexMediaHash(context.Background(), db, 1, 10, sha, 123, path, identity); err != nil {
		t.Fatalf("IndexMediaHash duplicate: %v", err)
	}
	if err := IndexMediaHash(context.Background(), db, 2, 20, sha, 456, path2, identity2); err != nil {
		t.Fatalf("IndexMediaHash two: %v", err)
	}
	matches, err := FindMediaHash(context.Background(), db, strings.ToLower(sha), 10)
	if err != nil {
		t.Fatalf("FindMediaHash: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if matches[0].SHA256 != strings.ToLower(sha) || matches[0].MediaPath != path || matches[0].Bytes != 123 {
		t.Fatalf("first match = %#v", matches[0])
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tg_media_hashes WHERE chat_id=1 AND message_id=10`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("upsert rows = %d, want 1", count)
	}
	limited, err := FindMediaHash(context.Background(), db, sha, 1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit result = %#v, %v", limited, err)
	}
}

func TestFindMediaHashHidesDeletedAndChangedMedia(t *testing.T) {
	db := testMediaHashDB(t)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatalf("EnsureMediaHashIndex: %v", err)
	}
	sha := strings.Repeat("c", 64)
	seed := func(chatID, messageID int64, path, identity string) {
		t.Helper()
		if err := InsertMessage(db, Message{ChatID: chatID, MessageID: messageID, Date: "2026-01-01T00:00:00Z", HasMedia: true, MediaType: strPtr("photo"), MediaPath: &path, MediaIdentity: &identity}); err != nil {
			t.Fatal(err)
		}
		if err := IndexMediaHash(context.Background(), db, chatID, messageID, sha, 1, path, identity); err != nil {
			t.Fatal(err)
		}
	}
	seed(1, 1, "/media/deleted.jpg", "deleted")
	seed(1, 2, "/media/path.jpg", "path-old")
	seed(1, 3, "/media/id.jpg", "id-old")
	seed(1, 4, "/media/live.jpg", "live")
	if _, err := db.Exec(`UPDATE tg_messages SET deleted=1 WHERE chat_id=1 AND message_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tg_messages SET media_path='/media/path-new.jpg' WHERE chat_id=1 AND message_id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tg_messages SET media_id='id-new' WHERE chat_id=1 AND message_id=3`); err != nil {
		t.Fatal(err)
	}
	matches, err := FindMediaHash(context.Background(), db, sha, 0)
	if err != nil {
		t.Fatalf("FindMediaHash: %v", err)
	}
	if len(matches) != 1 || matches[0].MessageID != 4 {
		t.Fatalf("matches = %#v, want only live message", matches)
	}
}

func TestIndexMediaHashRequiresActiveMediaPathAndExpectedSnapshot(t *testing.T) {
	db := testMediaHashDB(t)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatalf("EnsureMediaHashIndex: %v", err)
	}
	path := ""
	if err := InsertMessage(db, Message{ChatID: 1, MessageID: 1, Date: "2026-01-01T00:00:00Z", HasMedia: true, MediaPath: &path}); err != nil {
		t.Fatal(err)
	}
	if err := IndexMediaHash(context.Background(), db, 1, 1, strings.Repeat("d", 64), 1, "", ""); err == nil || !strings.Contains(err.Error(), "media path") {
		t.Fatalf("empty path error = %v", err)
	}
	path = "/media/a.jpg"
	identity := "a"
	if err := UpdateMessageMediaPath(db, 1, 1, "photo", path); err != nil {
		t.Fatal(err)
	}
	if err := IndexMediaHash(context.Background(), db, 1, 1, strings.Repeat("d", 64), 1, "/media/old.jpg", identity); err == nil {
		t.Fatal("stale expected snapshot unexpectedly indexed")
	}
	if err := IndexMediaHash(context.Background(), db, 1, 1, strings.Repeat("d", 64), 1, path, identity); err == nil {
		t.Fatal("identity mismatch unexpectedly indexed")
	}
	if err := IndexMediaHash(context.Background(), db, 1, 1, strings.Repeat("d", 64), 1, path, ""); err != nil {
		t.Fatalf("nil identity snapshot: %v", err)
	}
}

func TestMediaHashValidation(t *testing.T) {
	db := testMediaHashDB(t)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"", "xyz", strings.Repeat("g", 64)} {
		if err := IndexMediaHash(context.Background(), db, 1, 1, digest, 0, "", ""); err == nil {
			t.Errorf("IndexMediaHash(%q) accepted invalid digest", digest)
		}
		if _, err := FindMediaHash(context.Background(), db, digest, 1); err == nil {
			t.Errorf("FindMediaHash(%q) accepted invalid digest", digest)
		}
	}
}

func testMediaHashDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Connect(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func strPtr(value string) *string { return &value }

func TestHashRepresentationsCoexistAcrossDownloadPath(t *testing.T) {
	db := testMediaHashDB(t)
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tg_messages(chat_id,message_id,has_media,media_path,media_id) VALUES(1,1,1,'original','photo:1')`); err != nil {
		t.Fatal(err)
	}
	if err := IndexMediaHashKind(ctx, db, 1, 1, digest, 3, "original", "photo:1", "upload_original"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tg_messages SET media_path='downloaded'`); err != nil {
		t.Fatal(err)
	}
	if err := IndexMediaHashKind(ctx, db, 1, 1, digest, 3, "downloaded", "photo:1", "downloaded"); err != nil {
		t.Fatal(err)
	}
	matches, err := FindMediaHash(ctx, db, digest, 10)
	if err != nil || len(matches) != 2 {
		t.Fatalf("%+v %v", matches, err)
	}
	if _, err := db.Exec(`UPDATE tg_messages SET media_id='photo:2'`); err != nil {
		t.Fatal(err)
	}
	matches, err = FindMediaHash(ctx, db, digest, 10)
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale %+v %v", matches, err)
	}
}
