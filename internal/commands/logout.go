package commands

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/writes"
	"github.com/spf13/cobra"
)

func logoutCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "logout", Short: "Revoke this account's current session; keep cached history and downloads", Args: cobra.NoArgs}
	addWriteFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		account, err := selectedAccount(cmd, cfg.Paths)
		if err != nil {
			return emitDispatchedFailure(cmd, "logout", err)
		}
		if err = requireTypedWriteConfirm(cmd, account, "account"); err != nil {
			return emitDispatchedFailure(cmd, "logout", err)
		}
		db, session, audit, err := accountPathsForMode(cfg.Paths, account, writeArgsFrom(cmd).DryRun)
		if err != nil {
			return emitDispatchedFailure(cmd, "logout", err)
		}
		target := writes.ConfirmedTarget{ConfirmationSlot: "account", ConfirmationValue: account}
		payload := map[string]any{"account": account, "revoke_current_session": true, "preserve_cache": true}
		return runWriteResolvedTargetDurable(cmd, "logout", "auth.LogOut", account, cfg, resolvedWritePaths{db, session, audit}, payload, &target, map[string]any{"account": account, "logged_out": true}, func(ctx context.Context, c client.Client, _ int64, _ string) (map[string]any, error) {
			if err := c.Logout(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"account": account, "logged_out": true, "session_removed": true, "cache_preserved": true}, nil
		})
	}
	return cmd
}
