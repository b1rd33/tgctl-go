package main

import (
	"context"
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
	root := projectRoot()
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

func projectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return filepath.Clean(wd)
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
