package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
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

func TestRemoteHistoryUsesBoundedPageAndTypedCursor(t *testing.T) {
	_, _, dir := setupWriteEnv(t)
	fc := &client.FakeClient{
		Resolved:          map[string]client.ResolvedPeer{"1": {ChatID: 1, Kind: "user", Title: "Saved"}},
		RemoteHistoryPage: client.RemotePage{Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 12, Date: "2026-05-03T10:00:00Z", Text: "new"}, {ChatID: 1, MessageID: 11, Date: "2026-05-02T10:00:00Z", Text: "old"}}, NextOffsetID: 11},
	}
	p := readPaths{account: "default", db: filepath.Join(dir, "telegram.sqlite"), session: filepath.Join(dir, "tg.session")}
	cfg := CommandsConfig{ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) { return fc, nil }}
	data, err := RemoteHistoryRunner(context.Background(), cfg, p, "1", 2, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	result := data.(map[string]any)
	if result["source"] != "telegram" || result["next_cursor"] == "" {
		t.Fatalf("result = %#v", result)
	}
	if len(result["messages"].([]MessageSummaryDTO)) != 2 {
		t.Fatalf("messages = %#v", result["messages"])
	}
	if len(fc.RemoteHistoryReqs) != 1 || fc.RemoteHistoryReqs[0].ChatID != 1 || fc.RemoteHistoryReqs[0].Limit != 2 || fc.RemoteHistoryReqs[0].OffsetID != 0 {
		t.Fatalf("history request = %#v", fc.RemoteHistoryReqs)
	}
	if _, err := store.DecodeRemoteCursor(result["next_cursor"].(string), store.RemoteCursor{Account: "other", Operation: "history", Chat: 1}); err == nil {
		t.Fatal("cross-account cursor was accepted")
	}
}

func TestRemoteSearchUsesExplicitServerClient(t *testing.T) {
	_, fc, dir := setupWriteEnv(t)
	fc.Resolved = map[string]client.ResolvedPeer{
		"1": {ChatID: 1, Kind: "user", Title: "Saved"},
		"8": {ChatID: 8, Kind: "user", Title: "Sender"},
	}
	fc.RemoteSearchPage = client.RemotePage{Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 40, Date: "2026-05-02T00:00:00Z", Text: "term"}}, NextOffsetID: 40}
	p := readPaths{account: "default", db: filepath.Join(dir, "telegram.sqlite"), session: filepath.Join(dir, "tg.session")}
	cfg := CommandsConfig{ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) { return fc, nil }}
	cursor := store.EncodeRemoteCursor(store.RemoteCursor{Account: "default", Operation: "search", Chat: 1, Query: "term", Sender: 8, Media: "photo-video", Since: "2026-05-01", Until: "2026-05-03", OffsetID: 42})
	_, err := RemoteSearchRunner(context.Background(), cfg, p, "1", "term", 10, "8", "photo-video", "2026-05-01", "2026-05-03", cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.Calls) == 0 || fc.Calls[0] != "ResolveSelector" {
		t.Fatalf("calls = %#v", fc.Calls)
	}
	if len(fc.RemoteSearchReqs) != 1 {
		t.Fatalf("search requests = %#v", fc.RemoteSearchReqs)
	}
	req := fc.RemoteSearchReqs[0]
	if req.ChatID != 1 || req.SenderID != 8 || req.Query != "term" || req.Filter != "photo-video" || req.OffsetID != 42 || req.Limit != 10 {
		t.Fatalf("search request = %+v", req)
	}
}

func TestRemoteRowsBoundPagesAndTerminateContinuation(t *testing.T) {
	tests := []struct {
		name   string
		page   client.RemotePage
		offset int64
		limit  int
		ids    []int64
		next   int64
	}{
		{name: "empty", page: client.RemotePage{NextOffsetID: 99}, limit: 3, ids: []int64{}},
		{name: "exact full", page: client.RemotePage{Messages: remoteTestMessages(3, 10), NextOffsetID: 8}, limit: 3, ids: []int64{10, 9, 8}, next: 8},
		{name: "short server page", page: client.RemotePage{Messages: remoteTestMessages(2, 10), NextOffsetID: 8}, limit: 3, ids: []int64{10, 9}},
		{name: "overlap and duplicate", page: client.RemotePage{Messages: []client.BackfillMessage{{MessageID: 8}, {MessageID: 7}, {MessageID: 7}, {MessageID: 6}, {MessageID: 0}}, NextOffsetID: 8}, offset: 8, limit: 3, ids: []int64{7, 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, next := remoteRows(tt.page, tt.offset, tt.limit)
			if next != tt.next {
				t.Fatalf("next=%d, want %d", next, tt.next)
			}
			got := make([]int64, len(rows))
			for i, row := range rows {
				got[i] = row.MessageID
			}
			if !reflect.DeepEqual(got, tt.ids) {
				t.Fatalf("ids=%v, want %v", got, tt.ids)
			}
		})
	}

	first, next := remoteRows(client.RemotePage{Messages: remoteTestMessages(2, 10), NextOffsetID: 9}, 0, 2)
	second, final := remoteRows(client.RemotePage{Messages: []client.BackfillMessage{{MessageID: 9}, {MessageID: 8}, {MessageID: 8}}, NextOffsetID: 9}, next, 2)
	if next != 9 || final != 0 || len(first) != 2 || len(second) != 1 || second[0].MessageID != 8 {
		t.Fatalf("continuation first=%v next=%d second=%v final=%d", first, next, second, final)
	}
}

func remoteTestMessages(count int, first int64) []client.BackfillMessage {
	rows := make([]client.BackfillMessage, count)
	for i := range rows {
		rows[i].MessageID = first - int64(i)
	}
	return rows
}
