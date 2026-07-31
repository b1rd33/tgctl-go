package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

func registerAccountCommands(root *cobra.Command, mgr *accounts.Manager) {
	root.AddCommand(accountsAddCommand(mgr))
	root.AddCommand(accountsUseCommand(mgr))
	root.AddCommand(accountsListCommand(mgr))
	root.AddCommand(accountsShowCommand(mgr))
	root.AddCommand(accountsRemoveCommand(mgr))
}

func runAccountCommand(cmd *cobra.Command, name string, fn func() (any, error)) error {
	code := dispatch.Run(name, dispatch.Options{
		JSON:   jsonMode(cmd),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}, func(_ context.Context) (any, error) { return fn() })
	storeExitCode(cmd, code)
	return nil
}

func accountsAddCommand(m *accounts.Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "accounts-add <name>",
		Short:        "Create a new account directory",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountCommand(cmd, "accounts-add", func() (any, error) {
				if err := safety.RequireWritesNotReadOnly(safety.Args{ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
					return nil, err
				}
				dir, err := m.Add(args[0])
				if err != nil {
					return nil, err
				}
				return map[string]any{"name": args[0], "dir": dir}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func accountsUseCommand(m *accounts.Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "accounts-use <name>",
		Short:        "Select an existing account",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAccountCommand(cmd, "accounts-use", func() (any, error) {
				if err := safety.RequireWritesNotReadOnly(safety.Args{ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
					return nil, err
				}
				if err := m.Use(args[0]); err != nil {
					var anf *accounts.AccountNotFound
					if errors.As(err, &anf) {
						return nil, safety.NewBadArgs(
							"%s; run `tg accounts-add %s` first", anf.Error(), args[0])
					}
					return nil, err
				}
				return map[string]any{"name": args[0], "current": true}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func accountsListCommand(m *accounts.Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "accounts-list",
		Short:        "List known accounts",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAccountCommand(cmd, "accounts-list", func() (any, error) {
				names, err := m.List()
				if err != nil {
					return nil, err
				}
				rows := make([]map[string]any, len(names))
				for i, n := range names {
					d, _ := m.AccountDir(n, false)
					rows[i] = map[string]any{"name": n, "dir": d}
				}
				return map[string]any{
					"current":  m.Current(),
					"accounts": rows,
				}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func accountsShowCommand(m *accounts.Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "accounts-show",
		Short:        "Show the currently selected account and its paths",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAccountCommand(cmd, "accounts-show", func() (any, error) {
				name := m.Current()
				var (
					p   accounts.Paths
					err error
				)
				if commandReadOnly(cmd) {
					p, err = m.Paths(name)
				} else {
					p, err = m.ResolvePaths(name)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"name":         name,
					"account_dir":  p.AccountDir,
					"db_path":      p.DBPath,
					"session_path": p.SessionPath,
					"audit_path":   p.AuditPath,
					"media_dir":    p.MediaDir,
				}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func accountsRemoveCommand(m *accounts.Manager) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "accounts-remove <name>",
		Short:        "Delete an account directory",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			confirm, _ := cmd.Flags().GetString("confirm")
			return runAccountCommand(cmd, "accounts-remove", func() (any, error) {
				if err := safety.RequireWritesNotReadOnly(safety.Args{ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
					return nil, err
				}
				if err := safety.RequireTypedConfirm(safety.Args{Confirm: confirm}, args[0], "account"); err != nil {
					return nil, err
				}
				if err := m.Remove(args[0]); err != nil {
					var anf *accounts.AccountNotFound
					if errors.As(err, &anf) {
						return nil, fmt.Errorf("%w", anf)
					}
					return nil, err
				}
				return map[string]any{"name": args[0], "removed": true}, nil
			})
		},
	}
	cmd.Flags().String("confirm", "", "Typed account name to confirm removal")
	AddOutputFlags(cmd)
	return cmd
}
