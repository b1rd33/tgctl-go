package commands

import (
	"context"
	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerAdminCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(adminValueCommand(cfg, "chat-title", "channels.EditTitle", "Edit chat title", "title"))
	root.AddCommand(adminValueCommand(cfg, "chat-description", "channels.EditAbout", "Edit chat description", "description"))
	root.AddCommand(adminValueCommand(cfg, "chat-photo", "channels.EditPhoto", "Edit chat photo", "photo"))
	root.AddCommand(adminValueCommand(cfg, "set-permissions", "messages.EditChatDefaultBannedRights", "Set default chat permissions", "permissions"))
	root.AddCommand(adminNoValueCommand(cfg, "chat-invite-link", "messages.ExportChatInvite", "Export an invite link"))
	root.AddCommand(adminUserCommand(cfg, "promote", "channels.EditAdmin", false))
	root.AddCommand(adminUserCommand(cfg, "demote", "channels.EditAdmin", false))
	root.AddCommand(adminUserCommand(cfg, "ban-from-chat", "channels.EditBanned", true))
	root.AddCommand(adminUserCommand(cfg, "kick", "channels.EditBanned", true))
	root.AddCommand(adminUserCommand(cfg, "unban-from-chat", "channels.EditBanned", false))
	root.AddCommand(chatMembersCommand(cfg))
	root.AddCommand(chatsInfoCommand(cfg))
	root.AddCommand(accountSessionsCommand(cfg))
}

func adminValueCommand(cfg CommandsConfig, name, method, short, field string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          name + " <chat> <value>",
		Short:        short,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{field: args[1]}
			return runWrite(cmd, name, method, args[0], cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, _ string) (map[string]any, error) {
					req := client.AdminActionReq{Action: name, ChatID: chatID, Value: args[1]}
					if field == "photo" {
						req.Path = args[1]
					}
					resp, err := c.AdminAction(ctx, req)
					if err != nil {
						return nil, err
					}
					out := map[string]any{"updated": true, field: args[1]}
					if resp.Link != "" {
						out["invite_link"] = resp.Link
					}
					return out, nil
				})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func adminNoValueCommand(cfg CommandsConfig, name, method, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          name + " <chat>",
		Short:        short,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrite(cmd, name, method, args[0], cfg, map[string]any{},
				func(ctx context.Context, c client.Client, chatID int64, _ string) (map[string]any, error) {
					resp, err := c.AdminAction(ctx, client.AdminActionReq{Action: name, ChatID: chatID})
					if err != nil {
						return nil, err
					}
					return map[string]any{"invite_link": resp.Link}, nil
				})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func adminUserCommand(cfg CommandsConfig, name, method string, destructive bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:          name + " <chat> <user-id>",
		Short:        name + " user in chat",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			userID, err := parseOneInt(args[1], "user id")
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			payload := map[string]any{"user_id": userID}
			return runWrite(cmd, name, method, args[0], cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, _ string) (map[string]any, error) {
					if destructive {
						if err := safety.RequireTypedConfirm(writeArgsFrom(cmd).Args, userID, "user_id"); err != nil {
							return nil, err
						}
					}
					if _, err := c.AdminAction(ctx, client.AdminActionReq{Action: name, ChatID: chatID, UserID: userID}); err != nil {
						return nil, err
					}
					return map[string]any{"user_id": userID, "action": name}, nil
				})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func chatMembersCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "chat-members <chat>",
		Short:        "List chat members",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runAdminRead(cmd, cfg, "chat-members", args[0], func(ctx context.Context, c client.Client, chatID int64, title string) (any, error) {
				members, err := c.ListChatMembers(ctx, chatID, positiveLimit(limit, 50))
				if err != nil {
					return nil, err
				}
				return map[string]any{"chat": map[string]any{"chat_id": chatID, "title": title}, "members": members}, nil
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Maximum members")
	AddOutputFlags(cmd)
	return cmd
}

func chatsInfoCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "chats-info <chat-ids>",
		Short:        "Show chat info for comma-separated chat ids",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIntCSV(args[0])
			if err != nil {
				return emitDispatchedFailure(cmd, "chats-info", err)
			}
			dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
			code := dispatch.Run("chats-info", dispatch.Options{JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath}, func(ctx context.Context) (any, error) {
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				chats, err := c.GetChatsInfo(ctx, ids)
				if err != nil {
					return nil, err
				}
				return map[string]any{"chats": chats}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func accountSessionsCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "account-sessions",
		Short:        "List authorized Telegram sessions",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
			code := dispatch.Run("account-sessions", dispatch.Options{JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath}, func(ctx context.Context) (any, error) {
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				sessions, err := c.ListSessions(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"sessions": sessions}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func runAdminRead(cmd *cobra.Command, cfg CommandsConfig, name, selector string, runner func(context.Context, client.Client, int64, string) (any, error)) error {
	dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
	code := dispatch.Run(name, dispatch.Options{JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath}, func(ctx context.Context) (any, error) {
		db, err := store.Connect(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		chatID, title, err := resolve.ResolveChatDB(db, selector)
		if err != nil {
			return nil, err
		}
		c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		return runner(ctx, c, chatID, title)
	})
	storeExitCode(cmd, code)
	return nil
}
