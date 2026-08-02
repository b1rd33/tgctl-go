package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestSyncOncePersistsBackfillAndCheckpoint(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	text := "caught up"
	fake.BackfillResult = client.BackfillResult{Messages: []client.BackfillMessage{{
		ChatID: 1, MessageID: 12, Date: "2026-08-02T12:00:00Z", Text: text, HasMedia: true,
		MediaType: "photo", GroupedID: 44,
	}}}
	out, code := runRoot(t, cfg, "sync", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if !strings.Contains(out, `"messages_persisted":1`) || !strings.Contains(out, `"last_message_id":12`) {
		t.Fatalf("output=%s", out)
	}
	db, err := store.ConnectReadonly(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := store.GetOne(db, 1, 12, true)
	if err != nil || row.GroupedID != 44 || row.Text == nil || *row.Text != text {
		t.Fatalf("cached row=%+v err=%v", row, err)
	}
	state, err := store.LoadSyncState(db, "default", 1)
	if err != nil || state.LastMessageID != 12 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if len(fake.Backfills) != 1 {
		t.Fatalf("backfills=%#v", fake.Backfills)
	}
}

func TestSyncFollowOncePersistsMatchingEventAndSkipsOtherChats(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	fake.ListenEvents = []client.ListenEvent{
		{ChatID: 99, MessageID: 1, Text: "other"},
		{ChatID: 1, MessageID: 13, Date: "2026-08-02T12:01:00Z", Text: "live", GroupedID: 55},
	}
	out, code := runRoot(t, cfg, "sync", "1", "--allow-write", "--follow", "--once", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	var envelope struct {
		Data struct {
			Events int `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Events != 1 || len(fake.ListenCalls) != 2 {
		t.Fatalf("events=%d listen_calls=%d out=%s", envelope.Data.Events, len(fake.ListenCalls), out)
	}
	db, err := store.ConnectReadonly(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, err := store.GetOne(db, 1, 13, true)
	if err != nil || row.GroupedID != 55 {
		t.Fatalf("live row=%+v err=%v", row, err)
	}
}

func TestSyncRejectsOnceWithoutFollow(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "sync", "1", "--allow-write", "--once", "--json")
	if code != 2 || !strings.Contains(out, "--once requires --follow") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("client called: %#v", fake.Calls)
	}
}
