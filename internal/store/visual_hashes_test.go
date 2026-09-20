package store

import (
	"context"
	"strings"
	"testing"
)

func TestVisualSearchRankingIsolationAndStaleMedia(t *testing.T) {
	ctx := context.Background()
	db := testMediaHashDB(t)
	matches, _, _, err := FindSimilarMedia(ctx, db, "0000000000000000", 6, 10)
	if err != nil || len(matches) != 0 {
		t.Fatalf("%+v %v", matches, err)
	}
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatal(err)
	}
	for i, h := range []string{"0000000000000003", "0000000000000000", "ffffffffffffffff"} {
		id := int64(i + 1)
		sha := strings.Repeat(string(rune('a'+i)), 64)
		if _, err := db.Exec(`INSERT INTO tg_messages(chat_id,message_id,has_media,media_path) VALUES(1,?,1,'image')`, id); err != nil {
			t.Fatal(err)
		}
		if err := IndexMediaHash(ctx, db, 1, id, sha, 1, "image", ""); err != nil {
			t.Fatal(err)
		}
		if err := StoreVisualHash(ctx, db, sha, h); err != nil {
			t.Fatal(err)
		}
	}
	matches, incomplete, truncated, err := FindSimilarMedia(ctx, db, "0000000000000000", 6, 1)
	if err != nil || incomplete || !truncated || len(matches) != 1 || matches[0].MessageID != 2 || matches[0].Distance != 0 {
		t.Fatalf("%+v %v %v %v", matches, incomplete, truncated, err)
	}
	if _, err := db.Exec(`UPDATE tg_messages SET deleted=1 WHERE message_id=2`); err != nil {
		t.Fatal(err)
	}
	matches, _, _, err = FindSimilarMedia(ctx, db, "0000000000000000", 6, 10)
	if err != nil || len(matches) != 1 || matches[0].MessageID != 1 {
		t.Fatalf("%+v %v", matches, err)
	}
	other := testMediaHashDB(t)
	matches, _, _, err = FindSimilarMedia(ctx, other, "0000000000000000", 6, 10)
	if err != nil || len(matches) != 0 {
		t.Fatal("account leak")
	}
}

func TestVisualSearchReportsScanCap(t *testing.T) {
	ctx := context.Background()
	db := testMediaHashDB(t)
	sha := strings.Repeat("a", 64)
	if err := EnsureMediaHashIndex(db); err != nil {
		t.Fatal(err)
	}
	if err := StoreVisualHash(ctx, db, sha, "0000000000000000"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<10001) INSERT INTO tg_messages(chat_id,message_id,has_media,media_path) SELECT 1,x,1,'image' FROM n`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tg_media_hashes(representation,chat_id,message_id,sha256,bytes,media_path) SELECT 'cached_file',chat_id,message_id,?,1,'image' FROM tg_messages`, sha); err != nil {
		t.Fatal(err)
	}
	matches, incomplete, truncated, err := FindSimilarMedia(ctx, db, "0000000000000000", 0, 10)
	if err != nil || !incomplete || !truncated || len(matches) != 10 {
		t.Fatalf("%d %v %v %v", len(matches), incomplete, truncated, err)
	}
}
