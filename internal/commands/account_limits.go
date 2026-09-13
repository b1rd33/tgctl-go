package commands

import (
	"context"

	"github.com/spf13/cobra"
)

func registerAccountLimits(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{
		Use:          "account-limits",
		Short:        "Show authenticated account capabilities and effective Telegram limits",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDispatchedRead(cmd, "account-limits", map[string]any{}, cfg.Paths, func(ctx context.Context, p readPaths) (any, error) {
				c, err := openRemoteReadClient(ctx, cfg, p)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				limits, err := c.GetAccountLimits(ctx)
				if err != nil {
					return nil, err
				}
				if limits.Premium == nil && !limits.PremiumKnown {
					limits.Source = "telegram"
				}
				return limits, nil
			})
		},
	}
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}
