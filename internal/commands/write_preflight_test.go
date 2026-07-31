package commands

import (
	"context"
	"os"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
)

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
