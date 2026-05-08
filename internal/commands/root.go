package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		SilenceUsage: true,
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.ErrOrStderr(), c.Long)
		},
	}
	return cmd
}

func ExecuteRoot(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return 1
	}
	return 0
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}
