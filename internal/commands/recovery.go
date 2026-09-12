package commands

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/spf13/cobra"
	"path/filepath"
)

func registerRecoveryCommands(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{Use: "operations-list", Short: "Inspect durable write outcomes without exposing request payloads", Args: cobra.NoArgs}
	cmd.Flags().Int("limit", 100, "Maximum operations (1–1000)")
	AddOutputFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		account, err := selectedAccount(cmd, cfg.Paths)
		if err != nil {
			return emitDispatchedFailure(cmd, "operations-list", err)
		}
		path, _, _, err := accountPathsForMode(cfg.Paths, account, true)
		if err != nil {
			return emitDispatchedFailure(cmd, "operations-list", err)
		}
		code := dispatch.Run("operations-list", dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(ctx context.Context) (any, error) {
			limit, _ := cmd.Flags().GetInt("limit")
			db, err := store.ConnectReadonly(path)
			if err != nil {
				return nil, err
			}
			defer db.Close()
			rows, err := store.LedgerOperations(ctx, db, limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{"operations": rows, "source": "cache", "unknown_outcome_policy": "prepared/unknown operations must not be blindly resent; this command never retries or releases reservations"}, nil
		})
		storeExitCode(cmd, code)
		return nil
	}
	root.AddCommand(cmd)
	for _, restore := range []bool{false, true} {
		name := "db-backup"
		short := "Create a consistent private cache snapshot (session and media excluded)"
		if restore {
			name = "db-restore"
			short = "Restore a cache snapshot into an account with no existing database"
		}
		c := &cobra.Command{Use: name + " <snapshot>", Short: short, Args: cobra.ExactArgs(1)}
		addLocalWriteFlags(c)
		c.Flags().Bool("dry-run", false, "Preview paths without creating a snapshot or contacting Telegram")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			dbPath, session, _, err := accountPathsForMode(cfg.Paths, account, true)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(ctx context.Context) (any, error) {
				target, err := filepath.Abs(args[0])
				if err != nil {
					return nil, err
				}
				dry, _ := cmd.Flags().GetBool("dry-run")
				if dry {
					return map[string]any{"dry_run": true, "snapshot": target, "database": dbPath}, nil
				}
				lock := &safety.SessionLock{}
				if err := lock.AcquireContext(ctx, session, safety.LockWait(ctx), false); err != nil {
					return nil, err
				}
				defer lock.Release()
				source, dest := dbPath, target
				if restore {
					source, dest = target, dbPath
				}
				if err := store.Snapshot(ctx, source, dest); err != nil {
					return nil, err
				}
				return map[string]any{"snapshot": target, "database": dbPath, "session_included": false, "media_included": false}, nil
			})
			storeExitCode(cmd, code)
			return nil
		}
		root.AddCommand(c)
	}
}
