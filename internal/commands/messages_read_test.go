package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func setupReadDB(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "telegram.sqlite")
	auditPath := filepath.Join(dir, "audit.log")
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id, title, username) VALUES (1, 'Bjørn Müller', 'bjorn')"); err != nil {
		t.Fatalf("seed chat: %v", err)
	}
	hello := "hello"
	bye := "bye"
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 10, Date: "2026-05-01T10:00:00", Text: &hello, IsOutgoing: false}); err != nil {
		t.Fatalf("seed msg: %v", err)
	}
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 11, Date: "2026-05-02T10:00:00", Text: &bye, IsOutgoing: true}); err != nil {
		t.Fatalf("seed msg2: %v", err)
	}
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 12, Date: "2026-05-03T10:00:00", Text: &hello, IsOutgoing: false}); err != nil {
		t.Fatalf("seed msg3: %v", err)
	}
	if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 99, Date: "2026-05-04T10:00:00", Text: &bye}); err != nil {
		t.Fatalf("seed msg4: %v", err)
	}
	if err := store.MarkDeleted(db, 1, 99); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	return dbPath, auditPath
}

func TestShowRunnerResolverIntegration(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	got, err := ShowRunner(context.Background(), dbPath, "@BJORN", 100, false, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m := got.(map[string]any)
	chat := m["chat"].(ChatRef)
	if chat.ChatID != 1 {
		t.Fatalf("chat = %+v", chat)
	}
	msgs := m["messages"].([]MessageSummaryDTO)
	// Default newest first, deleted excluded.
	if len(msgs) != 3 || msgs[0].MessageID != 12 {
		t.Fatalf("messages = %#v", msgs)
	}
	if m["order"] != "newest_first" {
		t.Fatalf("order = %v", m["order"])
	}
}

func TestShowRunnerIncludeDeleted(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	got, err := ShowRunner(context.Background(), dbPath, "1", 100, false, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	msgs := got.(map[string]any)["messages"].([]MessageSummaryDTO)
	if len(msgs) != 4 {
		t.Fatalf("len=%d, want 4 (deleted included)", len(msgs))
	}
}

func TestSearchRunnerEmptyQueryRejected(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	_, err := SearchRunner(context.Background(), dbPath, "1", "", false, 10, false)
	var ba *safety.BadArgs
	if !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchRunnerCaseSensitive(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	got, err := SearchRunner(context.Background(), dbPath, "1", "Hello", true, 10, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got.(map[string]any)["messages"].([]MessageSummaryDTO)) != 0 {
		t.Fatalf("uppercase Hello should not match lowercase hello")
	}
}

func TestListMsgsRunnerInvalidSinceFormat(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	_, err := ListMsgsRunner(context.Background(), dbPath, "1", "2026/05/01", "", 10, false, false)
	var ba *safety.BadArgs
	if !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
}

func TestListMsgsRunnerDateRange(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	got, err := ListMsgsRunner(context.Background(), dbPath, "1", "2026-05-02", "2026-05-03", 10, false, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	msgs := got.(map[string]any)["messages"].([]MessageSummaryDTO)
	if len(msgs) != 2 {
		t.Fatalf("len=%d, want 2", len(msgs))
	}
}

func TestGetMsgRunnerNotFound(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	_, err := GetMsgRunner(context.Background(), dbPath, "1", 999, false)
	if err == nil {
		t.Fatalf("expected NotFound, got nil")
	}
}

func TestGetMsgRunnerIncludeDeleted(t *testing.T) {
	dbPath, _ := setupReadDB(t)
	got, err := GetMsgRunner(context.Background(), dbPath, "1", 99, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	msg := got.(map[string]any)["message"].(FullMessageDTO)
	if msg.MessageID != 99 {
		t.Fatalf("msg = %#v", msg)
	}
}

func TestShowCommandEmitsEnvelopeAndAudits(t *testing.T) {
	dbPath, auditPath := setupReadDB(t)
	root := NewRootCommand()
	registerReadCommands(root, stubPaths{db: dbPath, audit: auditPath})

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"show", "1", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("code = %d", code)
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\nstdout: %s", err, stdout.String())
	}
	if env["ok"] != true || env["command"] != "show" {
		t.Fatalf("envelope = %#v", env)
	}
	chat := env["data"].(map[string]any)["chat"].(map[string]any)
	if chat["title"] != "Bjørn Müller" {
		t.Fatalf("chat = %#v", chat)
	}
	b, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit not written: %v", err)
	}
	if !bytes.Contains(b, []byte(`"cmd":"show"`)) {
		t.Fatalf("audit log missing show entry: %s", b)
	}
}
