package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/audit"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// registerSendByUsername wires `tg send-by-username @name <text>`. This is the
// minimum-viable send path because it bypasses the chat_id→access_hash cache
// requirement: ContactsResolveUsername gives us a usable InputPeer in one
// round-trip.
func registerSendByUsername(root *cobra.Command, mgr *accounts.Manager) {
	cmd := &cobra.Command{
		Use:          "send-by-username <@user|@channel> <text>",
		Short:        "Send a text message by resolving an @username (no entity cache required)",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			text := args[1]
			rootCfg := RootConfigFrom(cmd.Root())
			account := rootCfg.Account
			if account == "" {
				account = mgr.Current()
			}
			paths, err := mgr.ResolvePaths(account)
			if err != nil {
				return emitDispatchedFailure(cmd, "send-by-username", err)
			}

			allow, _ := cmd.Flags().GetBool("allow-write")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			replyTo, _ := cmd.Flags().GetInt64("reply-to")
			silent, _ := cmd.Flags().GetBool("silent")
			noWeb, _ := cmd.Flags().GetBool("no-webpage")

			apiID, apiHash, credErr := client.EnsureCredentials()

			payload := map[string]any{
				"selector": selector, "text": text,
				"reply_to": replyTo, "silent": silent,
			}

			code := dispatch.Run("send-by-username", dispatch.Options{
				JSON:      jsonMode(cmd),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				AuditPath: paths.AuditPath,
				Args:      map[string]any{"selector": selector, "dry_run": dryRun},
			}, func(ctx context.Context) (any, error) {
				if err := safety.RequireWriteAllowed(safety.Args{
					ReadOnly:   rootCfg.ReadOnly,
					AllowWrite: allow,
				}); err != nil {
					return nil, err
				}
				if dryRun {
					out := map[string]any{"dry_run": true, "selector": selector}
					for k, v := range payload {
						out[k] = v
					}
					return out, nil
				}
				if credErr != nil {
					return nil, credErr
				}
				if err := safety.OutboundWriteLimiter.CheckOrError(); err != nil {
					return nil, err
				}
				_ = audit.Pre(paths.AuditPath, audit.PreEntry{
					Cmd:               "send-by-username",
					RequestID:         dispatch.RequestIDFrom(ctx),
					ResolvedChatTitle: selector,
					TelethonMethod:    "messages.SendMessage",
					PayloadPreview:    payload,
					DryRun:            false,
				})
				gc, err := client.New(ctx, apiID, apiHash, paths.SessionPath)
				if err != nil {
					return nil, err
				}
				defer gc.Close()
				resp, err := gc.SendMessageBySelector(ctx, selector, text, replyTo, silent, noWeb)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"selector":   selector,
					"text":       text,
					"message_id": resp.MessageID,
				}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int64("reply-to", 0, "Reply-to message id")
	cmd.Flags().Bool("silent", false, "Send silently")
	cmd.Flags().Bool("no-webpage", false, "Disable link preview")
	cmd.Flags().Bool("allow-write", false, "Required for any Telegram-side write")
	cmd.Flags().Bool("dry-run", false, "Print payload preview without contacting Telegram")
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}
