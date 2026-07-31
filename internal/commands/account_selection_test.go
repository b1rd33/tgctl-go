package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type failingAccountPaths struct{}

func (failingAccountPaths) Current() string { return "default" }

func (failingAccountPaths) AccountPaths(string) (string, string, string, error) {
	return "", "", "", safety.NewBadArgs("account path resolution failed")
}

func TestSelectedAccountPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
		current string
		want    string
	}{
		{name: "flag", flag: "personal", env: "work", current: "current", want: "personal"},
		{name: "environment", env: "work", current: "current", want: "work"},
		{name: "current", current: "current", want: "current"},
		{name: "default", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TG_ACCOUNT", tt.env)
			root := NewRootCommand()
			rootConfigPtr(root).Account = tt.flag
			got, err := selectedAccount(root, stubPaths{current: tt.current})
			if err != nil {
				t.Fatalf("selectedAccount: %v", err)
			}
			if got != tt.want {
				t.Fatalf("selectedAccount = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandsUseTGAccountWithoutCrossAccountLeakage(t *testing.T) {
	t.Setenv("TG_ACCOUNT", "work")
	mgr, cfg, defaultPaths, workPaths := setupAccountIsolation(t)

	seedMe(t, defaultPaths.DBPath, 1, "Default User")
	seedMe(t, workPaths.DBPath, 2, "Work User")
	seedChat(t, defaultPaths.DBPath, 10, "Default Chat")
	seedChat(t, workPaths.DBPath, 10, "Work Chat")

	me := runIsolationCommand(t, mgr, cfg, "me", "--offline", "--json")
	if got := me["data"].(map[string]any)["user_id"]; got != float64(2) {
		t.Fatalf("me selected user_id = %v, want work user 2", got)
	}
	if got := me["data"].(map[string]any)["session_path"]; got != workPaths.SessionPath {
		t.Fatalf("me session_path = %v, want %s", got, workPaths.SessionPath)
	}

	show := runIsolationCommand(t, mgr, cfg, "show", "10", "--json")
	if got := show["data"].(map[string]any)["chat"].(map[string]any)["title"]; got != "Work Chat" {
		t.Fatalf("show selected chat = %v, want Work Chat", got)
	}

	send := runIsolationCommand(t, mgr, cfg, "send", "10", "hello", "--allow-write", "--dry-run", "--json")
	if got := send["data"].(map[string]any)["chat"].(map[string]any)["title"]; got != "Work Chat" {
		t.Fatalf("send selected chat = %v, want Work Chat", got)
	}

	var gotSessionPath, gotDBPath string
	cfg.ClientFactory = func(_ context.Context, sessionPath, dbPath string) (client.Client, error) {
		gotSessionPath, gotDBPath = sessionPath, dbPath
		return &client.FakeClient{Dialogs: []client.ChatInfo{{ID: 20, Type: "channel", Title: "Work Only"}}}, nil
	}
	runIsolationCommand(t, mgr, cfg, "discover", "--allow-write", "--json")
	if gotSessionPath != workPaths.SessionPath || gotDBPath != workPaths.DBPath {
		t.Fatalf("discover client paths = (%q, %q), want (%q, %q)", gotSessionPath, gotDBPath, workPaths.SessionPath, workPaths.DBPath)
	}
	assertChatExists(t, workPaths.DBPath, 20, true)
	assertChatExists(t, defaultPaths.DBPath, 20, false)
	assertAuditContains(t, workPaths.AuditPath, `"cmd":"me"`, `"cmd":"show"`, `"cmd":"send"`, `"cmd":"discover"`)
	assertPathMissing(t, defaultPaths.AuditPath)
}

func TestBackfillUsesCurrentAccountWithoutCrossAccountLeakage(t *testing.T) {
	t.Setenv("TG_ACCOUNT", "")
	mgr, cfg, defaultPaths, workPaths := setupAccountIsolation(t)
	if err := mgr.Use("work"); err != nil {
		t.Fatalf("Use work: %v", err)
	}
	seedChat(t, defaultPaths.DBPath, 10, "Default Chat")
	seedChat(t, workPaths.DBPath, 10, "Work Chat")
	var gotSessionPath, gotDBPath string
	cfg.ClientFactory = func(_ context.Context, sessionPath, dbPath string) (client.Client, error) {
		gotSessionPath, gotDBPath = sessionPath, dbPath
		return &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 10, MessageID: 99, Date: "2026-07-31T12:00:00Z", Text: "work only"}}}, nil
	}

	runIsolationCommand(t, mgr, cfg, "backfill", "10", "--max-messages", "10", "--allow-write", "--json")
	if gotSessionPath != workPaths.SessionPath || gotDBPath != workPaths.DBPath {
		t.Fatalf("backfill client paths = (%q, %q), want (%q, %q)", gotSessionPath, gotDBPath, workPaths.SessionPath, workPaths.DBPath)
	}
	assertMessageExists(t, workPaths.DBPath, 10, 99, true)
	assertMessageExists(t, defaultPaths.DBPath, 10, 99, false)
	assertAuditContains(t, workPaths.AuditPath, `"cmd":"backfill"`)
	assertPathMissing(t, defaultPaths.AuditPath)
}

func TestMalformedSelectedAccountsAreRejected(t *testing.T) {
	tests := []struct {
		name string
		env  string
		args []string
	}{
		{name: "environment", env: "bad/name", args: []string{"stats", "--json"}},
		{name: "flag", args: []string{"--account", "bad/name", "doctor", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TG_ACCOUNT", tt.env)
			mgr, cfg, _, _ := setupAccountIsolation(t)
			_, code := executeIsolationCommand(t, mgr, cfg, tt.args...)
			if code != 2 {
				t.Fatalf("%v exit code = %d, want BAD_ARGS=2", tt.args, code)
			}
			assertPathMissing(t, filepath.Join(mgr.Root, "accounts", "bad"))
		})
	}
}

func TestReadCommandPropagatesAccountPathResolutionError(t *testing.T) {
	root := NewRootCommand()
	registerReadCommands(root, failingAccountPaths{})
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"stats", "--json"})
	if code := ExecuteRoot(root); code != 2 {
		t.Fatalf("exit code = %d, want BAD_ARGS=2; stdout: %s", code, stdout.String())
	}
}

func setupAccountIsolation(t *testing.T) (*accounts.Manager, CommandsConfig, accounts.Paths, accounts.Paths) {
	t.Helper()
	mgr := accounts.New(t.TempDir())
	defaultPaths, err := mgr.ResolvePaths("default")
	if err != nil {
		t.Fatal(err)
	}
	workPaths, err := mgr.ResolvePaths("work")
	if err != nil {
		t.Fatal(err)
	}
	cfg := CommandsConfig{
		Paths: mgr,
		ClientFactory: func(context.Context, string, string) (client.Client, error) {
			return &client.FakeClient{}, nil
		},
		ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) {
			return &client.FakeClient{}, nil
		},
	}
	return mgr, cfg, defaultPaths, workPaths
}

func runIsolationCommand(t *testing.T, mgr *accounts.Manager, cfg CommandsConfig, args ...string) map[string]any {
	t.Helper()
	envelope, code := executeIsolationCommand(t, mgr, cfg, args...)
	if code != 0 {
		t.Fatalf("%v exit code = %d\nenvelope: %#v", args, code, envelope)
	}
	return envelope
}

func executeIsolationCommand(t *testing.T, mgr *accounts.Manager, cfg CommandsConfig, args ...string) (map[string]any, int) {
	t.Helper()
	root := NewRootCommand()
	RegisterAll(root, mgr, cfg)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	code := ExecuteRoot(root)
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %v: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return envelope, code
}

func seedMe(t *testing.T, dbPath string, id int64, name string) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.UpsertMe(db, store.MeRow{UserID: id, DisplayName: sql.NullString{String: name, Valid: true}, CachedAt: "2026-07-31T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}

func seedChat(t *testing.T, dbPath string, id int64, title string) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id, title) VALUES (?, ?)", id, title); err != nil {
		t.Fatal(err)
	}
}

func assertChatExists(t *testing.T, dbPath string, id int64, want bool) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_chats WHERE chat_id=?", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("chat %d exists in %s = %v, want %v", id, dbPath, got, want)
	}
}

func assertMessageExists(t *testing.T, dbPath string, chatID, messageID int64, want bool) {
	t.Helper()
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND message_id=?", chatID, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("message %d exists in %s = %v, want %v", messageID, dbPath, got, want)
	}
}

func assertAuditContains(t *testing.T, auditPath string, fragments ...string) {
	t.Helper()
	b, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit %s: %v", auditPath, err)
	}
	for _, fragment := range fragments {
		if !bytes.Contains(b, []byte(fragment)) {
			t.Fatalf("audit %s missing %s:\n%s", auditPath, fragment, b)
		}
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %s exists or stat failed unexpectedly: %v", path, err)
	}
}

func TestDoctorDoesNotCreateSelectedAccount(t *testing.T) {
	t.Setenv("TG_ACCOUNT", "inspection")
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerDoctor(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"doctor", "--json"})
	_ = ExecuteRoot(root)
	if _, err := os.Stat(filepath.Join(rootDir, "accounts", "inspection")); !os.IsNotExist(err) {
		t.Fatalf("doctor created selected account directory: %v", err)
	}
}
