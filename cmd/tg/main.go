package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if !startupReadOnly(os.Args[1:]) {
		if _, err := mgr.MaybeMigrateDefaultFromRoot(); err != nil {
			fmt.Fprintln(os.Stderr, "WARN: account migration failed:", err)
		}
	}

	cfg := commands.CommandsConfig{
		Paths:                 mgr,
		ClientFactory:         gotdClientFactory,
		ReadOnlyClientFactory: gotdReadOnlyClientFactory,
	}

	cmd := commands.NewRootCommand()
	commands.RegisterAll(cmd, mgr, cfg)
	os.Exit(commands.ExecuteRoot(cmd))
}

func startupReadOnly(args []string) bool {
	if safety.ReadOnlyEnabled(false) {
		return true
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--read-only" {
			return true
		}
		if strings.HasPrefix(arg, "--read-only=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--read-only=")))
			if value != "0" && value != "false" && value != "no" && value != "off" {
				return true
			}
		}
	}
	return false
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
