package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func seedPendingEvent(t *testing.T, path string) client.ListenEvent {
	t.Helper()
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	event := client.ListenEvent{UpdateKind: "new_message", ChatID: 1, MessageID: 10, Text: "original"}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec("INSERT INTO tg_event_outbox(event) VALUES(?)", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	event.EventID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func listPending(t *testing.T, cfg CommandsConfig, account string) []store.PendingEvent {
	t.Helper()
	out, code := runRoot(t, cfg, "--account", account, "events-list", "--read-only", "--json")
	if code != 0 {
		t.Fatalf("list: %d %s", code, out)
	}
	var env struct {
		Data struct {
			Events []store.PendingEvent `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data.Events
}

func TestEventsOfflineReplayAndAckGates(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	seedPendingEvent(t, paths.db)
	lock := &safety.SessionLock{}
	if err := lock.AcquireContext(context.Background(), paths.session, 0, false); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		t.Fatal("offline command opened Telegram")
		return nil, nil
	}
	cfg.ReadOnlyClientFactory = func(context.Context, string) (client.Client, error) {
		t.Fatal("offline command opened Telegram")
		return nil, nil
	}
	before := captureImmutableFile(t, paths.db)
	rows := listPending(t, cfg, "default")
	again := listPending(t, cfg, "default")
	if len(rows) != 1 || rows[0].Receipt != again[0].Receipt {
		t.Fatal("event was consumed")
	}
	assertImmutableFile(t, paths.db, before)
	assertPathMissing(t, paths.audit)
	for _, flags := range [][]string{nil, {"--read-only", "--allow-write"}} {
		args := append([]string{"events-ack", rows[0].Receipt, "--json"}, flags...)
		if out, code := runRoot(t, cfg, args...); code != 6 {
			t.Fatalf("write guard %d %s", code, out)
		}
	}
	if out, code := runRoot(t, cfg, "events-ack", rows[0].Receipt, "--allow-write", "--dry-run", "--json"); code != 0 || !strings.Contains(out, `"pending":true`) {
		t.Fatalf("preview %d %s", code, out)
	}
	assertImmutableFile(t, paths.db, before)
	for attempt := 0; attempt < 2; attempt++ {
		out, code := runRoot(t, cfg, "events-ack", rows[0].Receipt, "--allow-write", "--json")
		if code != 0 || strings.Contains(out, `"removed":true`) != (attempt == 0) {
			t.Fatalf("ack %d %s", code, out)
		}
	}
	if len(listPending(t, cfg, "default")) != 0 {
		t.Fatal("ack failed")
	}
	assertPathMissing(t, paths.audit)
	assertPathMissing(t, paths.session)
}

func TestEventsAccountsAndMissingState(t *testing.T) {
	_, cfg, def, work := setupAccountIsolation(t)
	seedPendingEvent(t, def.DBPath)
	seedPendingEvent(t, work.DBPath)
	receipt := listPending(t, cfg, "default")[0].Receipt
	if out, code := runRoot(t, cfg, "--account", "work", "events-ack", receipt, "--allow-write", "--json"); code == 0 {
		t.Fatal("cross-account ack succeeded", out)
	}
	if len(listPending(t, cfg, "default")) != 1 || len(listPending(t, cfg, "work")) != 1 {
		t.Fatal("cross-account ack changed queue")
	}
	for _, args := range [][]string{{"events-list", "--read-only"}, {"events-ack", receipt, "--allow-write"}} {
		root := t.TempDir()
		cfg.Paths = accounts.New(root)
		if out, code := runRoot(t, cfg, append(args, "--json")...); code == 0 {
			t.Fatal("missing state succeeded", out)
		}
		assertPathMissing(t, filepath.Join(root, "accounts"))
	}
}

func TestManualListenReplaysWithoutReapplyingOrAcknowledging(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	event := seedPendingEvent(t, path)
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a newer update already committed after the pending event.
	newer := event
	newer.EventID = 0
	newer.Text = "newer edit"
	newer.UpdateKind = "edit_message"
	if err := client.ApplyListenEvent(db, newer); err != nil {
		t.Fatal(err)
	}
	var receipt string
	for i := 0; i < 2; i++ {
		fc.ListenEvents = []client.ListenEvent{event}
		out, code := runRoot(t, cfg, "--account", "default", "listen", "--once", "--manual-ack", "--allow-write", "--json")
		if code != 0 {
			t.Fatalf("listen %d %s", code, out)
		}
		var env struct {
			Data struct {
				Receipt string `json:"receipt"`
				Ack     bool   `json:"ack_required"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.Split(out, "\n")[0]), &env); err != nil {
			t.Fatal(err)
		}
		if !env.Data.Ack || env.Data.Receipt == "" || (i > 0 && receipt != env.Data.Receipt) {
			t.Fatal("receipt unstable/missing", out)
		}
		receipt = env.Data.Receipt
	}
	var text string
	if err := db.QueryRow("SELECT text FROM tg_messages WHERE chat_id=1 AND message_id=10").Scan(&text); err != nil || text != "newer edit" {
		t.Fatal("replay reverted newer cache", text, err)
	}
	db.Close()
	if len(listPending(t, cfg, "default")) != 1 {
		t.Fatal("listen consumed event")
	}
	if out, code := runRoot(t, cfg, "events-ack", receipt, "--allow-write", "--json"); code != 0 {
		t.Fatal(out)
	}
}

func TestManualListenFailureAndValidation(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	event := seedPendingEvent(t, cfg.Paths.(stubPaths).db)
	fc.ListenEvents = []client.ListenEvent{event}
	root := NewRootCommand()
	registerLiveCommands(root, cfg)
	root.SetOut(brokenEventWriter{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"listen", "--once", "--manual-ack", "--allow-write", "--json"})
	if ExecuteRoot(root) == 0 {
		t.Fatal("broken output succeeded")
	}
	if len(listPending(t, cfg, "default")) != 1 {
		t.Fatal("failed output consumed event")
	}
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		t.Fatal("invalid invocation opened Telegram")
		return nil, nil
	}
	for _, flags := range [][]string{nil, {"--once", "--only-dms"}, {"--once", "--only-groups"}} {
		args := append([]string{"listen", "--manual-ack", "--allow-write", "--json"}, flags...)
		if out, code := runRoot(t, cfg, args...); code == 0 {
			t.Fatal("accepted invalid manual mode", out)
		}
	}
}
