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
	"github.com/b1rd33/tgctl-go/internal/safety"
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

// stubClientFactory is the placeholder for the gotd/td-backed factory that
// will land alongside the live MTProto wiring. It returns a clear error so
// the dispatch layer maps it to NOT_AUTHED.
// gotdClientFactory returns the real gotd/td-backed Client. It expects a
// session at sessionPath created by `tg login`.
func gotdClientFactory(ctx context.Context, sessionPath string) (client.Client, error) {
	apiID, apiHash, err := client.EnsureCredentials()
	if err != nil {
		return nil, err
	}
	return client.New(ctx, apiID, apiHash, sessionPath)
}

var _ = safety.NewMissingCredentials // kept for any future fallback factory
