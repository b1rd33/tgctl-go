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
	"github.com/b1rd33/tgctl-go/internal/store"
)

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
			if got := selectedAccount(root, stubPaths{current: tt.current}); got != tt.want {
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

	show := runIsolationCommand(t, mgr, cfg, "show", "10", "--json")
	if got := show["data"].(map[string]any)["chat"].(map[string]any)["title"]; got != "Work Chat" {
		t.Fatalf("show selected chat = %v, want Work Chat", got)
	}

	send := runIsolationCommand(t, mgr, cfg, "send", "10", "hello", "--allow-write", "--dry-run", "--json")
	if got := send["data"].(map[string]any)["chat"].(map[string]any)["title"]; got != "Work Chat" {
		t.Fatalf("send selected chat = %v, want Work Chat", got)
	}

	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		return &client.FakeClient{Dialogs: []client.ChatInfo{{ID: 20, Type: "channel", Title: "Work Only"}}}, nil
	}
	runIsolationCommand(t, mgr, cfg, "discover", "--allow-write", "--json")
	assertChatExists(t, workPaths.DBPath, 20, true)
	assertChatExists(t, defaultPaths.DBPath, 20, false)
}

func TestBackfillUsesCurrentAccountWithoutCrossAccountLeakage(t *testing.T) {
	t.Setenv("TG_ACCOUNT", "")
	mgr, cfg, defaultPaths, workPaths := setupAccountIsolation(t)
	if err := mgr.Use("work"); err != nil {
		t.Fatalf("Use work: %v", err)
	}
	seedChat(t, defaultPaths.DBPath, 10, "Default Chat")
	seedChat(t, workPaths.DBPath, 10, "Work Chat")
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		return &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 10, MessageID: 99, Date: "2026-07-31T12:00:00Z", Text: "work only"}}}, nil
	}

	runIsolationCommand(t, mgr, cfg, "backfill", "10", "--max-messages", "10", "--allow-write", "--json")
	assertMessageExists(t, workPaths.DBPath, 10, 99, true)
	assertMessageExists(t, defaultPaths.DBPath, 10, 99, false)
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
	}
	return mgr, cfg, defaultPaths, workPaths
}

func runIsolationCommand(t *testing.T, mgr *accounts.Manager, cfg CommandsConfig, args ...string) map[string]any {
	t.Helper()
	root := NewRootCommand()
	RegisterAll(root, mgr, cfg)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("%v exit code = %d\nstdout: %s\nstderr: %s", args, code, stdout.String(), stderr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %v: %v\nstdout: %s", args, err, stdout.String())
	}
	return envelope
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
