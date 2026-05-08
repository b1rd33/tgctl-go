package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/output"
)

type RootConfig struct {
	ReadOnly        bool
	LockWaitSeconds float64
	Full            bool
	Account         string
	// ExitCode is set by command runners that emit their own envelope so the
	// process exit matches the dispatch classification rather than cobra's
	// default exit-1-on-RunE-error.
	ExitCode int
}

type rootConfigKey struct{}

func NewRootCommand() *cobra.Command {
	cfg := &RootConfig{}
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		SilenceUsage: true,
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.ErrOrStderr(), c.Long)
		},
	}
	cmd.PersistentFlags().BoolVar(&cfg.ReadOnly, "read-only", false, "Reject any write to Telegram or local DB. Also via TG_READONLY=1.")
	cmd.PersistentFlags().Float64Var(&cfg.LockWaitSeconds, "lock-wait", 0, "Seconds to wait for the Telegram session lock (default 0 = fail-fast).")
	cmd.PersistentFlags().BoolVar(&cfg.Full, "full", false, "Disable column truncation in human-mode output.")
	cmd.PersistentFlags().StringVar(&cfg.Account, "account", "", "Account name (uses accounts/<NAME>/). Default selected via accounts-use or TG_ACCOUNT env.")
	cmd.SetContext(context.WithValue(context.Background(), rootConfigKey{}, cfg))
	registerVersion(cmd)
	return cmd
}

func RootConfigFrom(cmd *cobra.Command) RootConfig {
	if cfg, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok && cfg != nil {
		return *cfg
	}
	return RootConfig{}
}

func registerVersion(root *cobra.Command) {
	v := &cobra.Command{
		Use:          "version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env := output.Success("version", map[string]any{"version": "dev"}, output.NewRequestID(), nil)
			code := output.Emit(env, output.EmitOptions{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			})
			if code != output.OK {
				return fmt.Errorf("version failed")
			}
			return nil
		},
	}
	AddOutputFlags(v)
	root.AddCommand(v)
}

func ExecuteRoot(root *cobra.Command) int {
	err := root.Execute()
	cfg := rootConfigPtr(root)
	if cfg != nil && cfg.ExitCode != 0 {
		return cfg.ExitCode
	}
	if err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return 1
	}
	return 0
}

// rootConfigPtr exposes the live *RootConfig so command runners can write
// their dispatch-issued exit code back into it. Distinct from RootConfigFrom,
// which returns a copy for read-only consumers.
func rootConfigPtr(cmd *cobra.Command) *RootConfig {
	if v, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok {
		return v
	}
	return nil
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}
