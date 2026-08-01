package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// installLegacyMigrationPreflight defers legacy layout migration until Cobra
// has parsed the selected leaf and its flags. This keeps rejected write and
// read-only invocations free of local filesystem mutations. Cobra intentionally
// does not execute run hooks for help or its built-in --version path, so those
// informational invocations are non-mutating too.
func installLegacyMigrationPreflight(root *cobra.Command, mgr *accounts.Manager) {
	previousE := root.PersistentPreRunE
	previous := root.PersistentPreRun
	root.PersistentPreRun = nil
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Preserve Cobra's normal precedence: PersistentPreRunE wins over
		// PersistentPreRun when both were configured before this hook.
		if previousE != nil {
			if err := previousE(cmd, args); err != nil {
				return err
			}
		} else if previous != nil {
			previous(cmd, args)
		}

		rootCfg := RootConfigFrom(cmd.Root())
		if safety.ReadOnlyEnabled(rootCfg.ReadOnly) {
			return nil
		}
		if allowFlag := cmd.Flags().Lookup("allow-write"); allowFlag != nil {
			allowWrite, _ := cmd.Flags().GetBool("allow-write")
			if !allowWrite && os.Getenv("TG_ALLOW_WRITE") != "1" {
				return nil
			}
		}
		if _, err := mgr.MaybeMigrateDefaultFromRoot(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARN: account migration failed:", err)
		}
		return nil
	}
}
