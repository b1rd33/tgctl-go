package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

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

func TestBackfillInsertsMessagesAndWarnsNearCap(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, SenderID: 99, Date: "2026-05-08T12:00:00", Text: "hi", IsOutgoing: true},
		{ChatID: 1, MessageID: 11, SenderID: 42, Date: "2026-05-08T12:01:00", Text: "there"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"].(float64) != 2 || len(data["cap_warnings"].([]any)) == 0 {
		t.Fatalf("data=%#v", data)
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
