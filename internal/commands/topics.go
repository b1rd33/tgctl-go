package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerTopicCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(topicsListCommand(cfg))
	root.AddCommand(topicCreateCommand(cfg))
	root.AddCommand(topicEditCommand(cfg))
	root.AddCommand(topicPinCommand(cfg, true))
	root.AddCommand(topicPinCommand(cfg, false))
}

func topicsListCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "topics-list <chat>",
		Short:        "List forum topics",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			query, _ := cmd.Flags().GetString("query")
			dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
			code := dispatch.Run("topics-list", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: map[string]any{"chat": args[0], "limit": limit, "query": query},
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				chatID, title, err := resolve.ResolveChatDB(db, args[0])
				if err != nil {
					return nil, err
				}
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				topics, err := c.ListTopics(ctx, chatID, positiveLimit(limit, 50), query)
				if err != nil {
					return nil, err
				}
				return map[string]any{"chat": map[string]any{"chat_id": chatID, "title": title}, "topics": topics}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int("limit", 50, "Maximum topics")
	cmd.Flags().String("query", "", "Filter query")
	AddOutputFlags(cmd)
	return cmd
}

func topicCreateCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "topic-create <chat> <title>",
		Short:        "Create a forum topic",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.TrimSpace(args[1])
			if title == "" {
				return emitDispatchedFailure(cmd, "topic-create", safety.NewBadArgs("topic title cannot be empty"))
			}
			iconColor, _ := cmd.Flags().GetInt("icon-color")
			iconEmojiID, _ := cmd.Flags().GetInt64("icon-emoji-id")
			payload := map[string]any{"title": title, "icon_color": iconColor, "icon_emoji_id": iconEmojiID}
			return runWrite(cmd, "topic-create", "channels.CreateForumTopic", args[0], cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					resp, err := c.CreateTopic(ctx, client.CreateTopicReq{ChatID: chatID, Title: title, IconColor: iconColor, IconEmojiID: iconEmojiID})
					if err != nil {
						return nil, err
					}
					return map[string]any{"topic_id": resp.TopicID, "title": resp.Title}, nil
				})
		},
	}
	cmd.Flags().Int("icon-color", 0, "Topic icon color")
	cmd.Flags().Int64("icon-emoji-id", 0, "Topic icon custom emoji id")
	addWriteFlags(cmd)
	return cmd
}

func topicEditCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "topic-edit <chat> <topic-id>",
		Short:        "Edit a forum topic",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var topicID int64
			if _, err := fmt.Sscan(args[1], &topicID); err != nil {
				return emitDispatchedFailure(cmd, "topic-edit", safety.NewBadArgs("topic-id must be an integer"))
			}
			title, _ := cmd.Flags().GetString("title")
			iconEmojiID, _ := cmd.Flags().GetInt64("icon-emoji-id")
			if strings.TrimSpace(title) == "" && iconEmojiID == 0 {
				return emitDispatchedFailure(cmd, "topic-edit", safety.NewBadArgs("nothing to edit"))
			}
			payload := map[string]any{"topic_id": topicID, "title": title, "icon_emoji_id": iconEmojiID}
			return runWrite(cmd, "topic-edit", "channels.EditForumTopic", args[0], cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, _ string) (map[string]any, error) {
					if err := c.EditTopic(ctx, client.EditTopicReq{ChatID: chatID, TopicID: topicID, Title: title, IconEmojiID: iconEmojiID}); err != nil {
						return nil, err
					}
					return map[string]any{"topic_id": topicID, "edited": true}, nil
				})
		},
	}
	cmd.Flags().String("title", "", "New topic title")
	cmd.Flags().Int64("icon-emoji-id", 0, "New icon custom emoji id")
	addWriteFlags(cmd)
	return cmd
}

func topicPinCommand(cfg CommandsConfig, pinned bool) *cobra.Command {
	name := "topic-pin"
	method := "channels.UpdatePinnedForumTopic"
	short := "Pin a forum topic"
	if !pinned {
		name = "topic-unpin"
		short = "Unpin a forum topic"
	}
	cmd := &cobra.Command{
		Use:          name + " <chat> <topic-id>",
		Short:        short,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var topicID int64
			if _, err := fmt.Sscan(args[1], &topicID); err != nil {
				return emitDispatchedFailure(cmd, name, safety.NewBadArgs("topic-id must be an integer"))
			}
			payload := map[string]any{"topic_id": topicID, "pinned": pinned}
			return runWrite(cmd, name, method, args[0], cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, _ string) (map[string]any, error) {
					if err := c.PinTopic(ctx, client.PinTopicReq{ChatID: chatID, TopicID: topicID, Pinned: pinned}); err != nil {
						return nil, err
					}
					return map[string]any{"topic_id": topicID, "pinned": pinned}, nil
				})
		},
	}
	addWriteFlags(cmd)
	return cmd
}
