package commands

import (
	"context"
	"database/sql"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
	"strings"
)

// registerSendByUsername wires `tg send-by-username @name <text>`. This is the
// minimum-viable send path because it bypasses the chat_id→access_hash cache
// requirement: ContactsResolveUsername gives us a usable InputPeer in one
// round-trip.
func registerSendByUsername(root *cobra.Command, mgr *accounts.Manager, cfg CommandsConfig) {
	cmd := &cobra.Command{
		Use:          "send-by-username <@user|@channel> <text>",
		Short:        "Send a text message by resolving an @username (no entity cache required)",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			text := args[1]
			if strings.TrimSpace(text) == "" {
				return emitDispatchedFailure(cmd, "send-by-username", safety.NewBadArgs("text cannot be empty"))
			}
			replyTo, _ := cmd.Flags().GetInt64("reply-to")
			if err := validateOptionalPositiveInt32(replyTo, "--reply-to"); err != nil {
				return emitDispatchedFailure(cmd, "send-by-username", err)
			}
			rootCfg := RootConfigFrom(cmd.Root())
			allow, _ := cmd.Flags().GetBool("allow-write")
			if err := safety.RequireWriteAllowed(safety.Args{ReadOnly: rootCfg.ReadOnly, AllowWrite: allow}); err != nil {
				return emitDispatchedFailure(cmd, "send-by-username", err)
			}
			account, err := selectedAccount(cmd, mgr)
			if err != nil {
				return emitDispatchedFailure(cmd, "send-by-username", err)
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			paths, err := mgr.Paths(account)
			if !dryRun && err == nil {
				paths, err = mgr.ResolvePaths(account)
			}
			if err != nil {
				return emitDispatchedFailure(cmd, "send-by-username", err)
			}

			silent, _ := cmd.Flags().GetBool("silent")
			noWeb, _ := cmd.Flags().GetBool("no-webpage")

			payload := map[string]any{
				"selector": selector, "text": text,
				"reply_to": replyTo, "silent": silent, "no_webpage": noWeb,
			}

			code := dispatch.Run("send-by-username", dispatch.Options{Context: cmd.Context(),
				JSON:      jsonMode(cmd),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				AuditPath: paths.AuditPath,
				Args:      map[string]any{"selector": selector, "dry_run": dryRun},
			}, func(ctx context.Context) (any, error) {

				var db *sql.DB
				var err error
				if !dryRun {
					db, err = store.Connect(paths.DBPath)
					if err != nil {
						return nil, err
					}
					defer db.Close()
				}
				return writes.Run(ctx, db, writes.PipelineInput{
					Cmd: "send-by-username", RawSelector: selector, LiveSelector: true, Args: writeArgsFrom(cmd), DBPath: paths.DBPath, AuditPath: paths.AuditPath, TelethonMethod: "messages.SendMessage", PayloadPreview: payload,
					Run: func(ctx context.Context, _ int64, _ string) (map[string]any, error) {
						c, err := cfg.ClientFactory(ctx, paths.SessionPath, paths.DBPath)
						if err != nil {
							return nil, &safety.DefinitiveRejection{Err: err}
						}
						defer c.Close()
						sender, ok := c.(interface {
							SendMessageBySelector(context.Context, string, string, int64, bool, bool) (client.SendMessageResp, error)
						})
						if !ok {
							return nil, &safety.DefinitiveRejection{Err: safety.NewBadArgs("client does not support username sends")}
						}
						resp, err := sender.SendMessageBySelector(ctx, selector, text, replyTo, silent, noWeb)
						if err != nil {
							return nil, err
						}
						return map[string]any{"selector": selector, "text": text, "message_id": resp.MessageID}, nil
					},
				})
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int64("reply-to", 0, "Reply-to message id")
	cmd.Flags().Bool("silent", false, "Send silently")
	cmd.Flags().Bool("no-webpage", false, "Disable link preview")
	addWriteFlags(cmd)
	root.AddCommand(cmd)
}
