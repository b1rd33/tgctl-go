package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/commands"
	"github.com/b1rd33/tgctl-go/internal/env"
)

func main() {
	os.Exit(run())
}

func run() int {
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	_ = env.LoadFile(filepath.Join(root, ".env"))
	mgr := accounts.New(root)

	cfg := commands.CommandsConfig{
		Paths:                 mgr,
		ClientFactory:         gotdClientFactory,
		ReadOnlyClientFactory: gotdReadOnlyClientFactory,
	}

	cmd := commands.NewRootCommand()
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.SetContext(ctx)
	commands.RegisterAll(cmd, mgr, cfg)
	return commands.ExecuteRoot(cmd)
}

func projectRoot() (string, error) {
	if path := os.Getenv("TGCTL_HOME"); path != "" {
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("TGCTL_HOME must be an absolute path")
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real, nil
		}
		return filepath.Clean(path), nil
	}
	path, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(path, "tgctl"), nil
}

// gotdClientFactory returns the real gotd/td-backed Client. It expects a
// session at sessionPath created by `tg login`. dbPath is the per-account
// SQLite cache the client reads to turn chat_ids into InputPeers.
func gotdClientFactory(ctx context.Context, sessionPath, dbPath string) (client.Client, error) {
	apiID, apiHash, err := client.EnsureCredentials()
	if err != nil {
		return nil, err
	}
	return client.New(ctx, apiID, apiHash, sessionPath, dbPath)
}

func gotdReadOnlyClientFactory(ctx context.Context, sessionPath string) (client.Client, error) {
	apiID, apiHash, err := client.EnsureCredentials()
	if err != nil {
		return nil, err
	}
	return client.NewReadonly(ctx, apiID, apiHash, sessionPath)
}
