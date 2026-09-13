package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerResolveCommand(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{
		Use:          "resolve <selector>",
		Short:        "Resolve a selector to a marked Telegram peer identity",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, _ := cmd.Flags().GetString("source")
			if source != "cache" && source != "telegram" {
				return emitDispatchedFailure(cmd, "resolve", fmt.Errorf("--source must be cache or telegram"))
			}
			p, err := resolvePaths(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "resolve", err)
			}
			return runDispatchedRead(cmd, "resolve", map[string]any{"selector": args[0], "source": source}, cfg.Paths, func(ctx context.Context, _ readPaths) (any, error) {
				if source == "cache" {
					return resolveCachedPeer(p.db, args[0])
				}
				c, err := openReadClient(ctx, cfg, p)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				peer, err := c.ResolveSelector(ctx, args[0])
				if err != nil {
					return nil, err
				}
				return map[string]any{"selector": args[0], "source": source, "peer": peer}, nil
			})
		},
	}
	cmd.Flags().String("source", "cache", "Resolution source: cache or telegram")
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}

func resolveCachedPeer(dbPath, selector string) (any, error) {
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	id, title, err := resolve.ResolveChatDB(db, selector)
	if err != nil {
		return nil, err
	}
	kind, accessHash, ok := store.LoadEntity(db, id)
	if !ok {
		kind = store.EntityKind(peerid.Kind(id))
	}
	return map[string]any{
		"selector": selector,
		"source":   "cache",
		"peer": client.ResolvedPeer{
			ChatID: id, Kind: string(kind), Title: title, AccessHash: accessHash,
			Self: selector == "self" || selector == "me",
		},
	}, nil
}
