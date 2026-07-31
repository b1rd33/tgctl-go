package commands

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestDatabaseSizeBytesUsesSQLiteAllocatedPages(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	got, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if want := pageCount * pageSize; got != want {
		t.Fatalf("size=%d, want page_count*page_size=%d", got, want)
	}
}

func TestDatabaseSizeBytesReturnsPragmaErrors(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseSizeBytes(db); err == nil {
		t.Fatal("databaseSizeBytes on closed DB returned nil error")
	}
}

func TestBackfillRejectsReadOnlyBeforeClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "--read-only", "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 6 {
		t.Fatalf("code=%d, want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsWhenMaxAlreadyReachedBeforeRPC(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	text := "cached"
	for i := int64(1); i <= 2; i++ {
		if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: i, Date: "2026-05-08T12:00:00", Text: &text}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called despite cap: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsDatabaseAlreadyOverCapBeforeClientOrMutation(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE cap_fixture(payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cap_fixture(payload) VALUES (zeroblob(1200000))"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := captureImmutableFile(t, paths.db)
	factoryCalls := 0
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		factoryCalls++
		return &client.FakeClient{}, nil
	}

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls=%d, want 0", factoryCalls)
	}
	assertImmutableFile(t, paths.db, before)
	if _, err := os.Stat(paths.audit); !os.IsNotExist(err) {
		t.Fatalf("audit path was created: %v", err)
	}
	if _, err := os.Stat(paths.session); !os.IsNotExist(err) {
		t.Fatalf("session path was created: %v", err)
	}
}

func TestBackfillDatabaseCapStopsBeforeNextInsert(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, Date: "2026-05-08T12:00:00", Text: strings.Repeat("x", 1200000)},
		{ChatID: 1, MessageID: 11, Date: "2026-05-08T12:01:00", Text: "must be skipped"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"] != float64(1) || data["messages_skipped"] != float64(1) || data["db_cap_reached"] != true {
		t.Fatalf("data=%#v", data)
	}
	if data["db_size_bytes"].(float64) < 1024*1024 {
		t.Fatalf("db_size_bytes=%v, want at least cap", data["db_size_bytes"])
	}
	if warnings, ok := data["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("warnings=%#v, want non-null empty array", data["warnings"])
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id IN (10, 11)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("inserted fixture rows=%d, want 1", count)
	}
}

func TestBackfillInsertsMessagesAndWarnsNearCap(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, SenderID: 99, Date: "2026-05-08T12:00:00", Text: "hi", IsOutgoing: true},
		{ChatID: 1, MessageID: 11, SenderID: 42, Date: "2026-05-08T12:01:00", Text: "there"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--throttle-seconds", "1.5", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"].(float64) != 2 || data["messages_skipped"].(float64) != 0 || len(data["warnings"].([]any)) == 0 {
		t.Fatalf("data=%#v", data)
	}
	for _, key := range []string{"db_size_bytes", "db_cap_reached", "media_downloaded", "media_skipped", "media_failed", "warnings"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("data missing %q: %#v", key, data)
		}
	}
	if data["db_cap_reached"] != false || data["media_downloaded"] != float64(0) || data["media_skipped"] != float64(0) || data["media_failed"] != float64(0) {
		t.Fatalf("unexpected cap/media counters: %#v", data)
	}
	if len(fc.Backfills) != 1 || fc.Backfills[0].Throttle != 1500*time.Millisecond {
		t.Fatalf("backfills=%#v, want throttle 1.5s", fc.Backfills)
	}
}

func TestBackfillRejectsNegativeLimitsAndThrottle(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "messages", args: []string{"--max-messages", "-1"}},
		{name: "database size", args: []string{"--max-db-size-mb", "-1"}},
		{name: "throttle", args: []string{"--throttle-seconds", "-0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			args := append([]string{"backfill", "1", "--allow-write", "--json"}, tc.args...)
			out, code := runRoot(t, cfg, args...)
			if code != 2 {
				t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
			}
			if len(fc.Backfills) != 0 {
				t.Fatalf("client called: %#v", fc.Backfills)
			}
		})
	}
}

func TestDiscoverUpsertsChats(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Dialogs = []client.ChatInfo{{ID: 2, Type: "channel", Title: "Ops", Username: "ops"}}
	out, code := runRoot(t, cfg, "discover", "--limit", "10", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Discoveries) != 1 {
		t.Fatalf("discoveries=%#v", fc.Discoveries)
	}
	db, _ := store.Connect(cfg.Paths.(stubPaths).db)
	defer db.Close()
	var title string
	if err := db.QueryRow("SELECT title FROM tg_chats WHERE chat_id=2").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Ops" {
		t.Fatalf("title=%q", title)
	}
}

func TestSyncContactsUpsertsContacts(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Contacts = []client.ContactInfo{{UserID: 42, Phone: "123", FirstName: "Ada", Username: "ada", IsMutual: true}}
	out, code := runRoot(t, cfg, "sync-contacts", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.ContactSyncs) != 1 {
		t.Fatalf("sync calls=%#v", fc.ContactSyncs)
	}
	if !strings.Contains(out, `"synced":1`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
