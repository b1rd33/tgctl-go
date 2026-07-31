package commands

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/spf13/cobra"
)

type immutableFile struct {
	data []byte
	info os.FileInfo
}

func captureImmutableFile(t *testing.T, path string) immutableFile {
	t.Helper()
	wantTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return immutableFile{data: data, info: info}
}

func assertImmutableFile(t *testing.T, path string, before immutableFile) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, before.data) || !os.SameFile(before.info, info) || before.info.Mode() != info.Mode() || before.info.Size() != info.Size() || !before.info.ModTime().Equal(info.ModTime()) {
		t.Fatalf("file changed: %s", path)
	}
}

func TestCachedReadsReadOnlyDoNotCreateMissingAccountOrDatabase(t *testing.T) {
	commands := [][]string{{"show", "1", "--json"}, {"stats", "--json"}, {"contacts", "--json"}, {"unread", "--json"}}
	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			rootDir := t.TempDir()
			mgr := accounts.New(rootDir)
			root := NewRootCommand()
			registerReadCommands(root, mgr)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append([]string{"--read-only"}, args...))
			if code := ExecuteRoot(root); code != 4 {
				t.Fatalf("exit code = %d, want NOT_FOUND=4", code)
			}
			assertPathMissing(t, filepath.Join(rootDir, "accounts"))
		})
	}
}

func TestCachedReadsReadOnlyLeaveDatabaseAndAuditUntouched(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	before := captureImmutableFile(t, paths.db)
	for _, args := range [][]string{{"show", "1", "--json"}, {"stats", "--json"}, {"contacts", "--json"}, {"unread", "--json"}} {
		if out, code := runRoot(t, cfg, append([]string{"--read-only"}, args...)...); code != 0 {
			t.Fatalf("%v code=%d out=%s", args, code, out)
		}
	}
	assertImmutableFile(t, paths.db, before)
	assertPathMissing(t, paths.audit)
}

func TestAccountsShowReadOnlyDoesNotCreatePaths(t *testing.T) {
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerAccountCommands(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "accounts-show", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestTelegramReadsReadOnlyUseReadonlyClientAndNoLocalWrites(t *testing.T) {
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	paths, err := mgr.ResolvePaths("default")
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Connect(paths.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id, type, title) VALUES (1, 'group', 'Ops')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := captureImmutableFile(t, paths.DBPath)
	fc := &client.FakeClient{Me: client.User{ID: 99}, Topics: []client.TopicInfo{{ID: 2, Title: "General"}}, Folders: []client.FolderInfo{{ID: 2, Title: "Ops"}}}
	readonlyCalls := 0
	cfg := CommandsConfig{
		Paths: mgr,
		ClientFactory: func(context.Context, string, string) (client.Client, error) {
			return nil, errors.New("writable client factory called")
		},
		ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) {
			readonlyCalls++
			return fc, nil
		},
	}
	commands := [][]string{
		{"topics-list", "1", "--json"}, {"folders-list", "--json"},
		{"chat-pinned-list", "1", "--json"}, {"chat-members", "1", "--json"},
		{"chats-info", "1", "--json"}, {"account-sessions", "--json"},
	}
	for _, args := range commands {
		_, code := executeIsolationCommand(t, mgr, cfg, append([]string{"--read-only"}, args...)...)
		if code != 0 {
			t.Fatalf("%v exit code = %d, want 0", args, code)
		}
	}
	if readonlyCalls != len(commands) {
		t.Fatalf("read-only client calls = %d, want %d", readonlyCalls, len(commands))
	}
	assertImmutableFile(t, paths.DBPath, before)
	assertPathMissing(t, paths.AuditPath)
	assertPathMissing(t, paths.SessionPath)
}

func TestTelegramWritesReadOnlyFailBeforeCreatingAccountPaths(t *testing.T) {
	commands := []struct {
		name     string
		register func(*cobra.Command, *accounts.Manager, CommandsConfig)
		args     []string
	}{
		{
			name: "send",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerWriteCommands(root, cfg)
			},
			args: []string{"send", "1", "hello", "--allow-write", "--json"},
		},
		{
			name: "delete-msg",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerDestructiveCommands(root, cfg)
			},
			args: []string{"delete-msg", "1", "1", "--allow-write", "--confirm", "1", "--json"},
		},
		{
			name: "send-by-username",
			register: func(root *cobra.Command, mgr *accounts.Manager, _ CommandsConfig) {
				registerSendByUsername(root, mgr)
			},
			args: []string{"send-by-username", "@ada", "hello", "--allow-write", "--json"},
		},
	}
	for _, tt := range commands {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := t.TempDir()
			mgr := accounts.New(rootDir)
			cfg := CommandsConfig{Paths: mgr, ClientFactory: func(context.Context, string, string) (client.Client, error) {
				return nil, errors.New("client factory called")
			}}
			root := NewRootCommand()
			tt.register(root, mgr, cfg)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append([]string{"--read-only"}, tt.args...))
			if code := ExecuteRoot(root); code != 6 {
				t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
			}
			assertPathMissing(t, filepath.Join(rootDir, "accounts"))
		})
	}
}

func TestTelegramWritesWithoutAllowWriteFailBeforeCreatingAccountPaths(t *testing.T) {
	commands := []struct {
		name     string
		register func(*cobra.Command, *accounts.Manager, CommandsConfig)
		args     []string
	}{
		{
			name: "send",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerWriteCommands(root, cfg)
			},
			args: []string{"send", "1", "hello", "--json"},
		},
		{
			name: "send dry-run",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerWriteCommands(root, cfg)
			},
			args: []string{"send", "1", "hello", "--dry-run", "--json"},
		},
		{
			name: "delete-msg",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerDestructiveCommands(root, cfg)
			},
			args: []string{"delete-msg", "1", "1", "--confirm", "1", "--json"},
		},
		{
			name: "folder-create",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerFolderCommands(root, cfg)
			},
			args: []string{"folder-create", "Ops", "--include-chats", "1", "--json"},
		},
		{
			name: "discover",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerLocalDBCommands(root, cfg)
			},
			args: []string{"discover", "--json"},
		},
		{
			name: "listen",
			register: func(root *cobra.Command, _ *accounts.Manager, cfg CommandsConfig) {
				registerLiveCommands(root, cfg)
			},
			args: []string{"listen", "--json"},
		},
		{
			name: "send-by-username",
			register: func(root *cobra.Command, mgr *accounts.Manager, _ CommandsConfig) {
				registerSendByUsername(root, mgr)
			},
			args: []string{"send-by-username", "@ada", "hello", "--json"},
		},
	}
	for _, tt := range commands {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := t.TempDir()
			mgr := accounts.New(rootDir)
			factoryCalls := 0
			cfg := CommandsConfig{Paths: mgr, ClientFactory: func(context.Context, string, string) (client.Client, error) {
				factoryCalls++
				return nil, errors.New("client factory called")
			}}
			root := NewRootCommand()
			tt.register(root, mgr, cfg)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(tt.args)
			if code := ExecuteRoot(root); code != 6 {
				t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
			}
			if factoryCalls != 0 {
				t.Fatalf("client factory calls = %d, want 0", factoryCalls)
			}
			assertPathMissing(t, filepath.Join(rootDir, "accounts"))
		})
	}
}
