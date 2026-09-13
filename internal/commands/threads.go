package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerThreadReadCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(repliesCommand(cfg))
	root.AddCommand(discussionMessageCommand(cfg))
}

func repliesCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "replies <chat> <message-id>",
		Short:        "Retrieve a bounded server page of replies to a message",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rootID, err := parsePositiveInt32Decimal(args[1], "message-id")
			if err != nil {
				return emitDispatchedFailure(cmd, "replies", err)
			}
			limit, _ := cmd.Flags().GetInt("limit")
			if limit < 1 || limit > 100 {
				return emitDispatchedFailure(cmd, "replies", fmt.Errorf("replies limit must be between 1 and 100"))
			}
			cursor, _ := cmd.Flags().GetString("cursor")
			return runThreadRead(cmd, cfg, "replies", args[0], map[string]any{"chat": args[0], "message_id": rootID, "limit": limit}, func(ctx context.Context, c client.Client, p readPaths, peer client.ResolvedPeer) (any, error) {
				expected := store.RemoteCursor{Account: p.account, Operation: "replies", Chat: peer.ChatID, Root: int64(rootID)}
				decoded, err := remoteCursor(cursor, expected)
				if err != nil {
					return nil, err
				}
				page, err := c.GetReplies(ctx, client.RepliesReq{ChatID: peer.ChatID, RootID: int64(rootID), OffsetID: decoded.OffsetID, Limit: limit})
				if err != nil {
					return nil, err
				}
				rows, nextID := remoteRows(page, decoded.OffsetID, limit)
				var next string
				if nextID != 0 {
					next = store.EncodeRemoteCursor(store.RemoteCursor{Account: p.account, Operation: "replies", Chat: peer.ChatID, Root: int64(rootID), OffsetID: nextID})
				}
				messages := make([]MessageSummaryDTO, len(rows))
				for i, row := range rows {
					messages[i] = remoteSummaryDTO(row)
				}
				return map[string]any{"source": "telegram", "coverage": "one bounded server page; not a frozen snapshot", "chat": ChatRef{ChatID: peer.ChatID, Title: peer.Title}, "root_message_id": rootID, "next_cursor": next, "messages": messages}, nil
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Maximum replies to return (1-100)")
	cmd.Flags().String("cursor", "", "Continue from next_cursor using the same root and chat")
	AddOutputFlags(cmd)
	return cmd
}

func discussionMessageCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "discussion-message <chat> <message-id>",
		Short:        "Resolve a channel post to its discussion thread without joining it",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			messageID, err := parsePositiveInt32Decimal(args[1], "message-id")
			if err != nil {
				return emitDispatchedFailure(cmd, "discussion-message", err)
			}
			return runThreadRead(cmd, cfg, "discussion-message", args[0], map[string]any{"chat": args[0], "message_id": messageID}, func(ctx context.Context, c client.Client, _ readPaths, peer client.ResolvedPeer) (any, error) {
				info, err := c.GetDiscussionMessage(ctx, peer.ChatID, int64(messageID))
				if err != nil {
					return nil, err
				}
				messages := make([]FullMessageDTO, len(info.Messages))
				for i, row := range info.Messages {
					messages[i] = remoteFullMessageDTO(row)
				}
				return map[string]any{"source": "telegram", "original_chat": ChatRef{ChatID: peer.ChatID, Title: peer.Title}, "original_message_id": messageID, "discussion_chat_id": info.DiscussionChatID, "max_id": info.MaxID, "read_inbox_max_id": info.ReadInboxMaxID, "read_outbox_max_id": info.ReadOutboxMaxID, "unread_count": info.UnreadCount, "messages": messages}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func runThreadRead(cmd *cobra.Command, cfg CommandsConfig, name, selector string, args map[string]any, runner func(context.Context, client.Client, readPaths, client.ResolvedPeer) (any, error)) error {
	p, err := resolvePaths(cmd, cfg.Paths)
	if err != nil {
		return emitDispatchedFailure(cmd, name, err)
	}
	code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: p.audit, Args: args}, func(ctx context.Context) (any, error) {
		c, err := openRemoteReadClient(ctx, cfg, p)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		peer, err := remoteChat(ctx, c, selector)
		if err != nil {
			return nil, err
		}
		return runner(ctx, c, p, peer)
	})
	storeExitCode(cmd, code)
	return nil
}
