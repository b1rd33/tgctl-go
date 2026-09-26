package commands

import (
	"context"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/spf13/cobra"
)

func registerEventCommands(root *cobra.Command, cfg CommandsConfig) {
	for _, ack := range []bool{false, true} {
		name, use, short := "events-list", "events-list", "Read pending cached updates without consuming them or opening Telegram"
		args := cobra.NoArgs
		if ack {
			name, use, short = "events-ack", "events-ack <receipt>", "Acknowledge one event after saving it in your own durable inbox"
			args = cobra.ExactArgs(1)
		}
		cmd := &cobra.Command{Use: use, Short: short, Args: args}
		if ack {
			addLocalWriteFlags(cmd)
			cmd.Flags().Bool("dry-run", false, "Validate receipt without removing the event")
		} else {
			AddOutputFlags(cmd)
			cmd.Flags().Int("limit", 20, "Maximum pending events (1–100)")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if ack {
				if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
					return emitDispatchedFailure(cmd, name, err)
				}
			}
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			path, _, _, err := accountPathsForMode(cfg.Paths, account, true)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			dry, _ := cmd.Flags().GetBool("dry-run")
			code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(ctx context.Context) (any, error) {
				// Open existing state first; an ack typo must not create an account.
				db, err := store.ConnectReadonly(path)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				scope := store.EventScope(path, account)
				if !ack {
					limit, _ := cmd.Flags().GetInt("limit")
					events, more, err := store.PendingEvents(ctx, db, scope, limit)
					return map[string]any{"events": events, "has_more": more, "source": "local_outbox", "ack_required": true, "network_refresh": false}, err
				}
				id, present, err := store.AcknowledgePendingEvent(ctx, db, scope, args[0], true)
				if err != nil {
					return nil, err
				}
				if dry {
					return map[string]any{"event_id": id, "pending": present, "dry_run": true}, nil
				}
				if present {
					writer, err := store.Connect(path)
					if err != nil {
						return nil, err
					}
					defer writer.Close()
					id, present, err = store.AcknowledgePendingEvent(ctx, writer, scope, args[0], false)
					if err != nil {
						return nil, err
					}
				}
				return map[string]any{"event_id": id, "removed": present, "already_absent": !present}, nil
			})
			storeExitCode(cmd, code)
			return nil
		}
		root.AddCommand(cmd)
	}
}
