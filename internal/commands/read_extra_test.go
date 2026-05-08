package commands

import (
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestStatsContactsUnreadReadFromCache(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO tg_contacts(user_id, phone, first_name, username, is_mutual) VALUES (42, '123', 'Ada', 'ada', 1)"); err != nil {
		t.Fatal(err)
	}
	text := "unread"
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 99, Date: "2026-05-08T12:00:00", Text: &text}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	out, code := runRoot(t, cfg, "stats", "--json")
	if code != 0 || !strings.Contains(out, `"messages":1`) {
		t.Fatalf("stats code=%d out=%s", code, out)
	}
	out, code = runRoot(t, cfg, "contacts", "--json")
	if code != 0 || !strings.Contains(out, `"username":"ada"`) {
		t.Fatalf("contacts code=%d out=%s", code, out)
	}
	out, code = runRoot(t, cfg, "unread", "--json")
	if code != 0 || !strings.Contains(out, `"messages"`) {
		t.Fatalf("unread code=%d out=%s", code, out)
	}
}
