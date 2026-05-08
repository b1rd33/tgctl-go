package commands

import "github.com/spf13/cobra"

func AddOutputFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("json", false, "Force JSON envelope output (default when stdout is not a TTY)")
	cmd.Flags().Bool("human", false, "Force human-readable output (default on a TTY)")
	cmd.MarkFlagsMutuallyExclusive("json", "human")
}

func jsonMode(cmd *cobra.Command) bool {
	jsonFlag, _ := cmd.Flags().GetBool("json")
	humanFlag, _ := cmd.Flags().GetBool("human")
	if jsonFlag {
		return true
	}
	if humanFlag {
		return false
	}
	return true
}
