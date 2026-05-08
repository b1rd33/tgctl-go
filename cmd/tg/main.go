package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/commands"
)

func main() {
	root := projectRoot()
	mgr := accounts.New(root)
	if _, err := mgr.MaybeMigrateDefaultFromRoot(); err != nil {
		fmt.Fprintln(os.Stderr, "WARN: account migration failed:", err)
	}

	cfg := commands.CommandsConfig{
		Paths:         mgr,
		ClientFactory: stubClientFactory,
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
func stubClientFactory(_ context.Context, _ string) (client.Client, error) {
	return nil, errors.New("live Telegram client not wired in this build")
}
