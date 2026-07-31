package commands

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
)

func registerDestructiveCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(deleteMsgCommand(cfg))
	root.AddCommand(leaveChatCommand(cfg))
	root.AddCommand(blockUserCommand(cfg, false))
	root.AddCommand(blockUserCommand(cfg, true))
	root.AddCommand(terminateSessionCommand(cfg))
}

// ---- delete-msg ----

func deleteMsgCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "delete-msg <chat> <message-ids>",
		Short:        "Delete one or more messages (revoke for everyone by default)",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			ids, err := parseIntCSV(args[1])
			if err != nil {
				return emitDispatchedFailure(cmd, "delete-msg", err)
			}
			forEveryone, _ := cmd.Flags().GetBool("for-everyone")
			noForEveryone, _ := cmd.Flags().GetBool("no-for-everyone")
			if forEveryone && noForEveryone {
				return emitDispatchedFailure(cmd, "delete-msg",
					safety.NewBadArgs("--for-everyone and --no-for-everyone are mutually exclusive"))
			}

			payload := map[string]any{"message_ids": ids, "for_everyone": forEveryone}
			if err := requireResolvedTypedWriteConfirm(cmd, cfg.Paths, selector, "chat_id", func(chatID int64) any { return chatID }); err != nil {
				return emitDispatchedFailure(cmd, "delete-msg", err)
			}
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "delete-msg", pathErr)
			}
			wargs := writeArgsFrom(cmd)

			code := dispatch.Run("delete-msg", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: payload,
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()

				return writes.Run(ctx, db, writes.PipelineInput{
					Cmd: "delete-msg", RawSelector: selector, Args: wargs,
					DBPath: dbPath, AuditPath: auditPath,
					TelethonMethod: "messages.DeleteMessages",
					PayloadPreview: payload,
					Run: func(ctx context.Context, chatID int64, _ string) (map[string]any, error) {
						effective := forEveryone
						if !forEveryone && !noForEveryone {
							out, err := allCachedMessagesOutgoing(db, chatID, ids)
							if err != nil {
								return nil, err
							}
							effective = out
						}
						if noForEveryone {
							effective = false
						}
						c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
						if err != nil {
							return nil, err
						}
						defer c.Close()
						resp, err := c.DeleteMessages(ctx, client.DeleteMessagesReq{
							ChatID: chatID, MessageIDs: ids, ForEveryone: effective,
						})
						if err != nil {
							return nil, err
						}
						if effective {
							for _, id := range ids {
								_ = store.MarkDeleted(db, chatID, id)
							}
						}
						results := make([]map[string]any, len(ids))
						for i, id := range ids {
							results[i] = map[string]any{"message_id": id, "deleted": true}
						}
						return map[string]any{
							"summary": map[string]any{
								"requested":    len(ids),
								"deleted":      resp.Deleted,
								"for_everyone": effective,
							},
							"results": results,
						}, nil
					},
				})
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Bool("for-everyone", false, "Force revoke for everyone")
	cmd.Flags().Bool("no-for-everyone", false, "Force delete only for self")
	addWriteFlags(cmd)
	return cmd
}

// allCachedMessagesOutgoing returns true when every cached message in `ids`
// for `chatID` has is_outgoing=1. Missing rows count as false.
func allCachedMessagesOutgoing(db *sql.DB, chatID int64, ids []int64) (bool, error) {
	for _, id := range ids {
		var out sql.NullInt64
		err := db.QueryRow(
			"SELECT is_outgoing FROM tg_messages WHERE chat_id = ? AND message_id = ?",
			chatID, id,
		).Scan(&out)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !out.Valid || out.Int64 == 0 {
			return false, nil
		}
	}
	return true, nil
}

// ---- leave-chat ----

func leaveChatCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "leave-chat <chat>",
		Short:        "Leave a group or channel (typed confirm required)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			payload := map[string]any{}
			if err := requireResolvedTypedWriteConfirm(cmd, cfg.Paths, selector, "chat_id", func(chatID int64) any { return chatID }); err != nil {
				return emitDispatchedFailure(cmd, "leave-chat", err)
			}
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "leave-chat", pathErr)
			}
			wargs := writeArgsFrom(cmd)

			code := dispatch.Run("leave-chat", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: payload,
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				return writes.Run(ctx, db, writes.PipelineInput{
					Cmd: "leave-chat", RawSelector: selector, Args: wargs,
					DBPath: dbPath, AuditPath: auditPath,
					TelethonMethod: "channels.LeaveChannel",
					PayloadPreview: payload,
					Run: func(ctx context.Context, chatID int64, _ string) (map[string]any, error) {
						if isUserChat(db, chatID) {
							return nil, safety.NewBadArgs(
								"leave-chat refuses 1-on-1 user chats; use block-user instead")
						}
						c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
						if err != nil {
							return nil, err
						}
						defer c.Close()
						if err := c.LeaveChat(ctx, client.LeaveChatReq{ChatID: chatID}); err != nil {
							return nil, err
						}
						_, _ = db.Exec("UPDATE tg_chats SET left = 1 WHERE chat_id = ?", chatID)
						return map[string]any{"left": true}, nil
					},
				})
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func isUserChat(db *sql.DB, chatID int64) bool {
	var t sql.NullString
	if err := db.QueryRow("SELECT type FROM tg_chats WHERE chat_id = ?", chatID).Scan(&t); err != nil {
		return false
	}
	return t.Valid && t.String == "user"
}

// ---- block-user / unblock-user ----

func blockUserCommand(cfg CommandsConfig, unblock bool) *cobra.Command {
	name := "block-user"
	method := "contacts.Block"
	short := "Block a user"
	if unblock {
		name = "unblock-user"
		method = "contacts.Unblock"
		short = "Unblock a previously blocked user"
	}
	cmd := &cobra.Command{
		Use:          name + " <user>",
		Short:        short,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			payload := map[string]any{}
			if err := requireResolvedTypedWriteConfirm(cmd, cfg.Paths, selector, "user_id", func(userID int64) any { return userID }); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, name, pathErr)
			}
			wargs := writeArgsFrom(cmd)
			code := dispatch.Run(name, dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: payload,
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				return writes.Run(ctx, db, writes.PipelineInput{
					Cmd: name, RawSelector: selector, Args: wargs,
					DBPath: dbPath, AuditPath: auditPath, TelethonMethod: method,
					PayloadPreview: payload,
					Run: func(ctx context.Context, userID int64, _ string) (map[string]any, error) {
						c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
						if err != nil {
							return nil, err
						}
						defer c.Close()
						req := client.BlockUserReq{UserID: userID}
						if unblock {
							err = c.UnblockUser(ctx, req)
						} else {
							err = c.BlockUser(ctx, req)
						}
						if err != nil {
							return nil, err
						}
						return map[string]any{"user_id": userID, "blocked": !unblock}, nil
					},
				})
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	addWriteFlags(cmd)
	return cmd
}

// ---- terminate-session ----

func terminateSessionCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "terminate-session <session-hash>",
		Short:        "Terminate one of your authorized Telegram sessions",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rawHash := args[0]
			var hash int64
			if _, err := fmt.Sscan(rawHash, &hash); err != nil {
				return emitDispatchedFailure(cmd, "terminate-session",
					safety.NewBadArgs("session-hash must be an integer (got %q)", rawHash))
			}
			payload := map[string]any{"session_hash": hash}
			if err := requireTypedWriteConfirm(cmd, hash, "session_hash"); err != nil {
				return emitDispatchedFailure(cmd, "terminate-session", err)
			}
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "terminate-session", pathErr)
			}
			code := dispatch.Run("terminate-session", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: payload,
			}, func(ctx context.Context) (any, error) {
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				if err := c.TerminateSession(ctx, client.TerminateSessionReq{Hash: hash}); err != nil {
					return nil, err
				}
				return map[string]any{"session_hash": hash, "terminated": true}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	addWriteFlags(cmd)
	return cmd
}
