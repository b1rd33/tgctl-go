package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/commands"
	"github.com/b1rd33/tgctl-go/internal/env"
)

func main() {
	root := projectRoot()
	_ = env.LoadFile(filepath.Join(root, ".env"))
	mgr := accounts.New(root)
	if _, err := mgr.MaybeMigrateDefaultFromRoot(); err != nil {
		fmt.Fprintln(os.Stderr, "WARN: account migration failed:", err)
	}

	cfg := commands.CommandsConfig{
		Paths:         mgr,
		ClientFactory: gotdClientFactory,
	}

	cmd := commands.NewRootCommand()
	commands.RegisterAll(cmd, mgr, cfg)
	os.Exit(commands.ExecuteRoot(cmd))
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
