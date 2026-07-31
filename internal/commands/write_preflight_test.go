package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type operationTestPaths struct {
	db, session, audit string
}

type adversarialAccountPaths struct {
	currentCalls  int
	readonlyCalls int
	writableCalls int
	current       []string
	accounts      map[string]operationTestPaths
	beforeWrite   func()
}

func (p *adversarialAccountPaths) Current() string {
	index := p.currentCalls
	p.currentCalls++
	if index >= len(p.current) {
		index = len(p.current) - 1
	}
	return p.current[index]
}

func (p *adversarialAccountPaths) AccountPathsReadonly(account string) (string, string, string, error) {
	p.readonlyCalls++
	paths, ok := p.accounts[account]
	if !ok {
		return "", "", "", fmt.Errorf("unknown account %q", account)
	}
	return paths.db, paths.session, paths.audit, nil
}

func (p *adversarialAccountPaths) AccountPaths(account string) (string, string, string, error) {
	p.writableCalls++
	if p.beforeWrite != nil {
		p.beforeWrite()
	}
	paths, ok := p.accounts[account]
	if !ok {
		return "", "", "", fmt.Errorf("unknown account %q", account)
	}
	return paths.db, paths.session, paths.audit, nil
}

func operationAccount(t *testing.T, root, name string, chatID int64, title string) operationTestPaths {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := operationTestPaths{
		db:      filepath.Join(dir, "telegram.sqlite"),
		session: filepath.Join(dir, "tg.session"),
		audit:   filepath.Join(dir, "audit.log"),
	}
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id, type, title) VALUES (?, 'group', ?)", chatID, title); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return paths
}

func idempotencyCount(t *testing.T, dbPath, key string) int {
	t.Helper()
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_idempotency WHERE key = ?", key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func auditResolvedChatID(t *testing.T, auditPath string) int64 {
	t.Helper()
	file, err := os.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("audit is empty: %v", scanner.Err())
	}
	var entry map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	value, ok := entry["resolved_chat_id"].(float64)
	if !ok {
		t.Fatalf("audit resolved_chat_id=%#v", entry["resolved_chat_id"])
	}
	return int64(value)
}

func TestConfirmedWriteUsesSingleAccountSnapshot(t *testing.T) {
	root := t.TempDir()
	alpha := operationAccount(t, root, "alpha", 1, "Ops")
	beta := operationAccount(t, root, "beta", 2, "Ops")
	paths := &adversarialAccountPaths{
		current:  []string{"alpha", "beta"},
		accounts: map[string]operationTestPaths{"alpha": alpha, "beta": beta},
	}
	cfg, fc, _ := setupWriteEnv(t)
	cfg.Paths = paths
	clientDBPath := ""
	cfg.ClientFactory = func(_ context.Context, _, dbPath string) (client.Client, error) {
		clientDBPath = dbPath
		return fc, nil
	}
	key := "single-account-snapshot"

	out, code := runRoot(t, cfg, "promote", "Ops", "42", "--fuzzy", "--allow-write", "--confirm", "1", "--idempotency-key", key, "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if paths.currentCalls != 1 || paths.readonlyCalls != 1 || paths.writableCalls != 0 {
		t.Fatalf("path resolution calls current=%d readonly=%d writable=%d, want 1/1/0", paths.currentCalls, paths.readonlyCalls, paths.writableCalls)
	}
	if clientDBPath != alpha.db {
		t.Fatalf("client DB path=%q want confirmed account %q", clientDBPath, alpha.db)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].ChatID != 1 {
		t.Fatalf("AdminActions=%#v, want confirmed chat 1", fc.AdminActions)
	}
	if idempotencyCount(t, alpha.db, key) != 1 || idempotencyCount(t, beta.db, key) != 0 {
		t.Fatal("idempotency key was not recorded only in the confirmed account")
	}
	if _, err := os.Stat(alpha.audit); err != nil {
		t.Fatalf("confirmed account audit missing: %v", err)
	}
	if got := auditResolvedChatID(t, alpha.audit); got != 1 {
		t.Fatalf("audit resolved chat=%d want confirmed chat 1", got)
	}
	assertPathMissing(t, beta.audit)
}

func TestConfirmedWriteDoesNotResolveSelectorAgainAfterCacheChange(t *testing.T) {
	root := t.TempDir()
	alpha := operationAccount(t, root, "alpha", 1, "Ops")
	mutations := 0
	paths := &adversarialAccountPaths{
		current:  []string{"alpha"},
		accounts: map[string]operationTestPaths{"alpha": alpha},
	}
	paths.beforeWrite = func() {
		mutations++
		db, err := store.Connect(alpha.db)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("UPDATE tg_chats SET title = 'Archived' WHERE chat_id = 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO tg_chats(chat_id, type, title) VALUES (2, 'group', 'Ops')"); err != nil {
			t.Fatal(err)
		}
	}
	cfg, fc, _ := setupWriteEnv(t)
	cfg.Paths = paths
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) { return fc, nil }

	out, code := runRoot(t, cfg, "--account", "alpha", "promote", "Ops", "42", "--fuzzy", "--allow-write", "--confirm", "1", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if paths.readonlyCalls != 1 || paths.writableCalls != 0 || mutations != 0 {
		t.Fatalf("path/cache calls readonly=%d writable=%d mutations=%d, want 1/0/0", paths.readonlyCalls, paths.writableCalls, mutations)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].ChatID != 1 {
		t.Fatalf("AdminActions=%#v, want confirmed chat 1", fc.AdminActions)
	}
	if got := auditResolvedChatID(t, alpha.audit); got != 1 {
		t.Fatalf("audit resolved chat=%d want confirmed chat 1", got)
	}
}

func TestConfirmedWriteIdempotencyKeyCannotReplayDifferentConfirmedTarget(t *testing.T) {
	tests := []struct {
		name       string
		first      []string
		second     []string
		actionSize func(*client.FakeClient) int
	}{
		{
			name:   "resolved chat",
			first:  []string{"promote", "1", "42", "--allow-write", "--confirm", "1"},
			second: []string{"promote", "2", "42", "--allow-write", "--confirm", "2"},
			actionSize: func(fc *client.FakeClient) int {
				return len(fc.AdminActions)
			},
		},
		{
			name:   "user id",
			first:  []string{"ban-from-chat", "1", "42", "--allow-write", "--confirm", "42"},
			second: []string{"ban-from-chat", "1", "43", "--allow-write", "--confirm", "43"},
			actionSize: func(fc *client.FakeClient) int {
				return len(fc.AdminActions)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			if _, err := openSeed(cfg.Paths.(stubPaths).db,
				"INSERT INTO tg_chats(chat_id, type, title) VALUES (2, 'group', 'Other')"); err != nil {
				t.Fatal(err)
			}
			key := "confirmed-target-key"
			firstArgs := append(append([]string{}, tt.first...), "--idempotency-key", key, "--json")
			if out, code := runRoot(t, cfg, firstArgs...); code != 0 {
				t.Fatalf("first code=%d\nout:%s", code, out)
			}
			secondArgs := append(append([]string{}, tt.second...), "--idempotency-key", key, "--json")
			out, code := runRoot(t, cfg, secondArgs...)
			if code != 2 {
				t.Fatalf("second code=%d want BAD_ARGS=2\nout:%s", code, out)
			}
			if got := tt.actionSize(fc); got != 1 {
				t.Fatalf("Telegram actions=%d want 1", got)
			}
		})
	}
}

func TestEveryResolvedConfirmationCommandUsesPreflightSnapshot(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "delete message", args: []string{"delete-msg", "Ops", "10", "--confirm", "1"}},
		{name: "leave chat", args: []string{"leave-chat", "Ops", "--confirm", "1"}},
		{name: "block user", args: []string{"block-user", "Ops", "--confirm", "1"}},
		{name: "unblock user", args: []string{"unblock-user", "Ops", "--confirm", "1"}},
		{name: "promote", args: []string{"promote", "Ops", "42", "--confirm", "1"}},
		{name: "demote", args: []string{"demote", "Ops", "42", "--confirm", "1"}},
		{name: "ban", args: []string{"ban-from-chat", "Ops", "42", "--confirm", "42"}},
		{name: "kick", args: []string{"kick", "Ops", "42", "--confirm", "42"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			alpha := operationAccount(t, root, "alpha", 1, "Ops")
			paths := &adversarialAccountPaths{
				current:  []string{"alpha"},
				accounts: map[string]operationTestPaths{"alpha": alpha},
			}
			cfg, _, _ := setupWriteEnv(t)
			cfg.Paths = paths
			args := append([]string{"--account", "alpha"}, tt.args...)
			args = append(args, "--fuzzy", "--allow-write", "--json")

			out, code := runRoot(t, cfg, args...)
			if code != 0 {
				t.Fatalf("code=%d\nout:%s", code, out)
			}
			if paths.readonlyCalls != 1 || paths.writableCalls != 0 {
				t.Fatalf("path resolution readonly=%d writable=%d, want 1/0", paths.readonlyCalls, paths.writableCalls)
			}
		})
	}
}

func TestTypedDestructiveConfirmationPrecedesWritableSideEffects(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "delete message", args: []string{"delete-msg", "1", "10", "--allow-write", "--json"}},
		{name: "leave chat", args: []string{"leave-chat", "1", "--allow-write", "--json"}},
		{name: "block user", args: []string{"block-user", "1", "--allow-write", "--json"}},
		{name: "unblock user", args: []string{"unblock-user", "1", "--allow-write", "--json"}},
		{name: "promote", args: []string{"promote", "1", "42", "--allow-write", "--json"}},
		{name: "demote", args: []string{"demote", "1", "42", "--allow-write", "--json"}},
		{name: "ban", args: []string{"ban-from-chat", "1", "42", "--allow-write", "--json"}},
		{name: "kick", args: []string{"kick", "1", "42", "--allow-write", "--json"}},
		{name: "folder delete", args: []string{"folder-delete", "2", "--allow-write", "--json"}},
		{name: "terminate session", args: []string{"terminate-session", "12345", "--allow-write", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, _ := setupWriteEnv(t)
			paths := cfg.Paths.(stubPaths)
			baseFactory := cfg.ClientFactory
			factoryCalls := 0
			cfg.ClientFactory = func(ctx context.Context, sessionPath, dbPath string) (client.Client, error) {
				factoryCalls++
				return baseFactory(ctx, sessionPath, dbPath)
			}
			before := captureImmutableFile(t, paths.db)

			out, code := runRoot(t, cfg, tt.args...)
			if code != 7 {
				t.Fatalf("code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
			}
			if factoryCalls != 0 {
				t.Fatalf("client factory calls=%d want 0", factoryCalls)
			}
			assertImmutableFile(t, paths.db, before)
			assertPathMissing(t, paths.audit)
			assertPathMissing(t, paths.session)
		})
	}
}

func TestTypedDestructiveConfirmationPrecedesDryRun(t *testing.T) {
	for _, args := range [][]string{
		{"delete-msg", "1", "10", "--allow-write", "--dry-run", "--json"},
		{"promote", "1", "42", "--allow-write", "--dry-run", "--json"},
		{"folder-delete", "2", "--allow-write", "--dry-run", "--json"},
	} {
		t.Run(args[0], func(t *testing.T) {
			cfg, _, _ := setupWriteEnv(t)
			paths := cfg.Paths.(stubPaths)
			factoryCalls := 0
			baseFactory := cfg.ClientFactory
			cfg.ClientFactory = func(ctx context.Context, sessionPath, dbPath string) (client.Client, error) {
				factoryCalls++
				return baseFactory(ctx, sessionPath, dbPath)
			}
			before := captureImmutableFile(t, paths.db)

			out, code := runRoot(t, cfg, args...)
			if code != 7 {
				t.Fatalf("code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
			}
			if factoryCalls != 0 {
				t.Fatalf("client factory calls=%d want 0", factoryCalls)
			}
			assertImmutableFile(t, paths.db, before)
			assertPathMissing(t, paths.audit)
		})
	}
}

func TestTypedDestructiveConfirmationMismatchPrecedesWritableSideEffects(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "delete message", args: []string{"delete-msg", "1", "10", "--allow-write", "--confirm", "999", "--json"}},
		{name: "leave chat", args: []string{"leave-chat", "1", "--allow-write", "--confirm", "999", "--json"}},
		{name: "block user", args: []string{"block-user", "1", "--allow-write", "--confirm", "999", "--json"}},
		{name: "unblock user", args: []string{"unblock-user", "1", "--allow-write", "--confirm", "999", "--json"}},
		{name: "promote", args: []string{"promote", "1", "42", "--allow-write", "--confirm", "999", "--json"}},
		{name: "demote", args: []string{"demote", "1", "42", "--allow-write", "--confirm", "999", "--json"}},
		{name: "ban", args: []string{"ban-from-chat", "1", "42", "--allow-write", "--confirm", "999", "--json"}},
		{name: "kick", args: []string{"kick", "1", "42", "--allow-write", "--confirm", "999", "--json"}},
		{name: "folder delete", args: []string{"folder-delete", "2", "--allow-write", "--confirm", "999", "--json"}},
		{name: "terminate session", args: []string{"terminate-session", "12345", "--allow-write", "--confirm", "999", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, _ := setupWriteEnv(t)
			paths := cfg.Paths.(stubPaths)
			baseFactory := cfg.ClientFactory
			factoryCalls := 0
			cfg.ClientFactory = func(ctx context.Context, sessionPath, dbPath string) (client.Client, error) {
				factoryCalls++
				return baseFactory(ctx, sessionPath, dbPath)
			}
			before := captureImmutableFile(t, paths.db)

			out, code := runRoot(t, cfg, tt.args...)
			if code != 2 {
				t.Fatalf("code=%d want BAD_ARGS=2\nout:%s", code, out)
			}
			if factoryCalls != 0 {
				t.Fatalf("client factory calls=%d want 0", factoryCalls)
			}
			assertImmutableFile(t, paths.db, before)
			assertPathMissing(t, paths.audit)
			assertPathMissing(t, paths.session)
		})
	}
}

func TestTypedDestructiveConfirmationPrecedesIdempotentReplay(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	baseFactory := cfg.ClientFactory
	factoryCalls := 0
	cfg.ClientFactory = func(ctx context.Context, sessionPath, dbPath string) (client.Client, error) {
		factoryCalls++
		return baseFactory(ctx, sessionPath, dbPath)
	}
	key := "promote-confirm-preflight"
	if out, code := runRoot(t, cfg, "promote", "1", "42", "--allow-write", "--confirm", "1", "--idempotency-key", key, "--json"); code != 0 {
		t.Fatalf("seed code=%d\nout:%s", code, out)
	}
	if err := os.Remove(paths.audit); err != nil {
		t.Fatal(err)
	}
	factoryCalls = 0
	before := captureImmutableFile(t, paths.db)

	out, code := runRoot(t, cfg, "promote", "1", "42", "--allow-write", "--idempotency-key", key, "--json")
	if code != 7 {
		t.Fatalf("replay code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls=%d want 0", factoryCalls)
	}
	assertImmutableFile(t, paths.db, before)
	assertPathMissing(t, paths.audit)
}
