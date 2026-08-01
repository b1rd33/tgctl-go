package commands

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"

	"github.com/gotd/td/tg"
)

// registerLogin wires the interactive authorization flow against gotd/td.
func registerLogin(root *cobra.Command, mgr *accounts.Manager) {
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Interactively authorize this account against Telegram",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := safety.RequireWritesNotReadOnly(safety.Args{ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
				return emitDispatchedFailure(cmd, "login", err)
			}
			apiID, apiHash, err := client.EnsureCredentials()
			if err != nil {
				return emitDispatchedFailure(cmd, "login", err)
			}
			account, err := selectedAccount(cmd, mgr)
			if err != nil {
				return emitDispatchedFailure(cmd, "login", err)
			}
			paths, err := mgr.ResolvePaths(account)
			if err != nil {
				return emitDispatchedFailure(cmd, "login", err)
			}
			code := dispatch.Run("login", dispatch.Options{
				JSON:      jsonMode(cmd),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				AuditPath: paths.AuditPath,
				Args:      map[string]any{"account": account},
			}, func(ctx context.Context) (any, error) {
				me, err := client.Login(ctx, client.LoginOptions{
					APIID:   apiID,
					APIHash: apiHash,
					Session: paths.SessionPath,
					Prompt:  newCLIPrompt(cmd),
				})
				if err != nil {
					return nil, err
				}
				// Cache me so `tg me --offline` works immediately.
				if db, dberr := store.Connect(paths.DBPath); dberr == nil {
					_ = store.UpsertMe(db, store.MeRow{
						UserID:      me.ID,
						Username:    nullStringOf(me.Username),
						Phone:       nullStringOf(me.Phone),
						FirstName:   nullStringOf(me.FirstName),
						LastName:    nullStringOf(me.LastName),
						DisplayName: nullStringOf(me.DisplayName),
						IsBot:       boolInt(me.IsBot),
						CachedAt:    nowUTC(),
					})
					db.Close()
				}
				return map[string]any{
					"account":      account,
					"user_id":      me.ID,
					"username":     stringOrNil(me.Username),
					"display_name": me.DisplayName,
					"session_path": paths.SessionPath,
				}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}

func newCLIPrompt(cmd *cobra.Command) client.AuthPrompt {
	in := bufio.NewReader(os.Stdin)
	stderr := cmd.ErrOrStderr()
	return client.AuthPrompt{
		Phone: func() (string, error) {
			fmt.Fprint(stderr, "Phone (with country code, e.g. +491701234567): ")
			line, err := in.ReadString('\n')
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(line), nil
		},
		Code: func(_ context.Context, _ *tg.AuthSentCode) (string, error) {
			fmt.Fprint(stderr, "Login code from Telegram: ")
			line, err := in.ReadString('\n')
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(line), nil
		},
		Password: func(_ context.Context) (string, error) {
			fmt.Fprint(stderr, "2FA password: ")
			b, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Fprintln(stderr)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		AcceptTOS: func(_ context.Context, t tg.HelpTermsOfService) error {
			fmt.Fprintln(stderr, "Telegram terms of service:")
			fmt.Fprintln(stderr, t.Text)
			fmt.Fprint(stderr, "Type 'accept' to continue: ")
			line, err := in.ReadString('\n')
			if err != nil {
				return err
			}
			if strings.TrimSpace(line) != "accept" {
				return errors.New("terms of service not accepted")
			}
			return nil
		},
	}
}

func nullStringOf(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func stringOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nowUTC() string {
	// Avoid pulling time twice into this file; reuse what exists in store.
	// Simple 2026-format timestamp. RFC3339 second precision.
	return timeNow()
}
