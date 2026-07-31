package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
)

// ClientFactory builds a per-command Telegram client. Production wires this
// to gotd/td; tests inject a *client.FakeClient. The factory receives both
// the session path (gotd auth state) and the per-account DB path (so the
// gotd client can read tg_entities to turn chat_ids into InputPeers).
type ClientFactory func(ctx context.Context, sessionPath, dbPath string) (client.Client, error)

// CommandsConfig bundles dependencies the command tree needs.
type CommandsConfig struct {
	Paths         AccountPathProvider
	ClientFactory ClientFactory
}

// addWriteFlags adds the safety/idempotency flags every write command shares.
func addWriteFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("allow-write", false, "Required for any Telegram-side write")
	cmd.Flags().Bool("dry-run", false, "Print payload preview without contacting Telegram")
	cmd.Flags().Bool("fuzzy", false, "Allow title-based selectors for write commands")
	cmd.Flags().String("idempotency-key", "", "Per-account replay-safe key")
	cmd.Flags().String("confirm", "", "Typed confirm against the resolved id")
	AddOutputFlags(cmd)
}

func writeArgsFrom(cmd *cobra.Command) writes.Args {
	cfg := RootConfigFrom(cmd.Root())
	allow, _ := cmd.Flags().GetBool("allow-write")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	fuzzy, _ := cmd.Flags().GetBool("fuzzy")
	confirm, _ := cmd.Flags().GetString("confirm")
	key, _ := cmd.Flags().GetString("idempotency-key")
	return writes.Args{
		Args: safety.Args{
			ReadOnly:   cfg.ReadOnly,
			AllowWrite: allow,
			Confirm:    confirm,
			Fuzzy:      fuzzy,
		},
		DryRun:         dryRun,
		IdempotencyKey: key,
	}
}

// readTextArg returns text for --text/positional value, supporting "-" for stdin.
// Trailing newlines are trimmed. Empty text returns BadArgs.
func readTextArg(value string, in io.Reader) (string, error) {
	var raw string
	if value == "-" {
		buf, err := io.ReadAll(in)
		if err != nil {
			return "", err
		}
		raw = string(buf)
	} else {
		raw = value
	}
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return "", safety.NewBadArgs("text cannot be empty")
	}
	return raw, nil
}

// topicReplyTo mirrors Python _topic_reply_to: --reply-to wins; warn if both supplied.
func topicReplyTo(replyTo, topic int64) (effective int64, warnings []string) {
	if replyTo != 0 && topic != 0 {
		return replyTo, []string{"--topic ignored because --reply-to was provided"}
	}
	if replyTo != 0 {
		return replyTo, nil
	}
	return topic, nil
}

func registerWriteCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(sendCommand(cfg))
	root.AddCommand(editMsgCommand(cfg))
	root.AddCommand(forwardCommand(cfg))
	root.AddCommand(pinMsgCommand(cfg, false))
	root.AddCommand(pinMsgCommand(cfg, true))
	root.AddCommand(reactCommand(cfg))
	root.AddCommand(markReadCommand(cfg))
}

func resolveWritePaths(cmd *cobra.Command, paths AccountPathProvider) (string, string, string) {
	account := selectedAccount(cmd, paths)
	return paths.AccountPaths(account)
}

func runWrite(cmd *cobra.Command, name, telethonMethod, selector string, cfg CommandsConfig, payloadPreview map[string]any, action func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error)) error {
	dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
	wargs := writeArgsFrom(cmd)
	args := map[string]any{"chat": selector, "dry_run": wargs.DryRun}

	code := dispatch.Run(name, dispatch.Options{
		JSON:      jsonMode(cmd),
		Stdout:    cmd.OutOrStdout(),
		Stderr:    cmd.ErrOrStderr(),
		AuditPath: auditPath,
		Args:      args,
	}, func(ctx context.Context) (any, error) {
		db, err := store.Connect(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		return writes.Run(ctx, db, writes.PipelineInput{
			Cmd:            name,
			RawSelector:    selector,
			Args:           wargs,
			DBPath:         dbPath,
			AuditPath:      auditPath,
			TelethonMethod: telethonMethod,
			PayloadPreview: payloadPreview,
			Run: func(ctx context.Context, chatID int64, chatTitle string) (map[string]any, error) {
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				return action(ctx, c, chatID, chatTitle)
			},
		})
	})
	storeExitCode(cmd, code)
	return nil
}

func storeExitCode(cmd *cobra.Command, code int) {
	if cfg := rootConfigPtr(cmd.Root()); cfg != nil {
		cfg.ExitCode = code
	}
}

// emitDispatchedFailure routes a pre-pipeline error through dispatch so the
// process emits the same envelope shape as a runner failure and the same exit
// code mapping. Returns nil so cobra does not also print/exit on the error.
func emitDispatchedFailure(cmd *cobra.Command, name string, err error) error {
	code := dispatch.Run(name, dispatch.Options{
		JSON:   jsonMode(cmd),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}, func(context.Context) (any, error) { return nil, err })
	storeExitCode(cmd, code)
	return nil
}

// ---- send ----

func sendCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "send <chat> <text>",
		Short:        "Send a text message",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			rawText := args[1]
			text, err := readTextArg(rawText, os.Stdin)
			if err != nil {
				return err
			}
			replyTo, _ := cmd.Flags().GetInt64("reply-to")
			topic, _ := cmd.Flags().GetInt64("topic")
			silent, _ := cmd.Flags().GetBool("silent")
			noWeb, _ := cmd.Flags().GetBool("no-webpage")

			effectiveReply, topicWarnings := topicReplyTo(replyTo, topic)
			payload := map[string]any{
				"text":     text,
				"reply_to": effectiveReply,
				"topic_id": topic,
				"silent":   silent,
			}

			return runWrite(cmd, "send", "messages.SendMessage", selector, cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					resp, err := c.SendMessage(ctx, client.SendMessageReq{
						ChatID:    chatID,
						Text:      text,
						ReplyTo:   effectiveReply,
						TopicID:   topic,
						Silent:    silent,
						NoWebpage: noWeb,
					})
					if err != nil {
						return nil, err
					}
					out := map[string]any{
						"chat":       map[string]any{"chat_id": chatID, "title": chatTitle},
						"message_id": resp.MessageID,
						"text":       text,
						"reply_to":   effectiveReply,
						"topic_id":   topic,
						"warnings":   topicWarnings,
					}
					return out, nil
				},
			)
		},
	}
	cmd.Flags().Int64("reply-to", 0, "Reply-to message id")
	cmd.Flags().Int64("topic", 0, "Forum topic id")
	cmd.Flags().Bool("silent", false, "Send silently (no notification)")
	cmd.Flags().Bool("no-webpage", false, "Disable link preview")
	addWriteFlags(cmd)
	return cmd
}

// ---- edit-msg ----

func editMsgCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "edit-msg <chat> <message-id> <new-text>",
		Short:        "Edit a previously sent message",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			var msgID int64
			if _, err := fmt.Sscan(args[1], &msgID); err != nil {
				return safety.NewBadArgs("message-id must be an integer (got %q)", args[1])
			}
			text, err := readTextArg(args[2], os.Stdin)
			if err != nil {
				return err
			}
			payload := map[string]any{"message_id": msgID, "new_text": text}
			return runWrite(cmd, "edit-msg", "messages.EditMessage", selector, cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					if err := c.EditMessage(ctx, client.EditMessageReq{ChatID: chatID, MessageID: msgID, NewText: text}); err != nil {
						return nil, err
					}
					return map[string]any{
						"message_id": msgID,
						"new_text":   text,
					}, nil
				},
			)
		},
	}
	addWriteFlags(cmd)
	return cmd
}

// ---- forward ----

func forwardCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "forward <from-chat> <to-chat> <message-ids>",
		Short:        "Forward one or more messages between chats",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fromSel := args[0]
			toSel := args[1]
			ids, err := parseIntCSV(args[2])
			if err != nil {
				return err
			}
			topic, _ := cmd.Flags().GetInt64("topic")
			payload := map[string]any{"from_chat_selector": fromSel, "message_ids": ids, "to_chat_selector": toSel, "topic_id": topic}

			// Resolve both chats. The pipeline only resolves one selector
			// natively, so we resolve the source manually before calling
			// runWrite against the destination.
			dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
			db, err := store.Connect(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			fromChatID, _, err := resolve.ResolveChatDB(db, fromSel)
			if err != nil {
				return err
			}

			wargs := writeArgsFrom(cmd)
			code := dispatch.Run("forward", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: payload,
			}, func(ctx context.Context) (any, error) {
				return writes.Run(ctx, db, writes.PipelineInput{
					Cmd: "forward", RawSelector: toSel, Args: wargs,
					DBPath: dbPath, AuditPath: auditPath, TelethonMethod: "messages.ForwardMessages",
					PayloadPreview: payload,
					Run: func(ctx context.Context, toChatID int64, toTitle string) (map[string]any, error) {
						c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
						if err != nil {
							return nil, err
						}
						defer c.Close()
						resp, err := c.Forward(ctx, client.ForwardReq{
							FromChatID: fromChatID, MessageIDs: ids, ToChatID: toChatID, TopicID: topic,
						})
						if err != nil {
							return nil, err
						}
						return map[string]any{
							"from_chat_id":    fromChatID,
							"to_chat":         map[string]any{"chat_id": toChatID, "title": toTitle},
							"forwarded_ids":   ids,
							"new_message_ids": resp.MessageIDs,
							"topic_id":        topic,
						}, nil
					},
				})
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int64("topic", 0, "Forum topic id on destination")
	addWriteFlags(cmd)
	return cmd
}

// ---- pin / unpin ----

func pinMsgCommand(cfg CommandsConfig, unpin bool) *cobra.Command {
	name := "pin-msg"
	short := "Pin a message in a chat"
	method := "messages.UpdatePinnedMessage"
	if unpin {
		name = "unpin-msg"
		short = "Unpin a previously pinned message"
	}
	cmd := &cobra.Command{
		Use:          name + " <chat> <message-id>",
		Short:        short,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			var msgID int64
			if _, err := fmt.Sscan(args[1], &msgID); err != nil {
				return safety.NewBadArgs("message-id must be an integer (got %q)", args[1])
			}
			silent, _ := cmd.Flags().GetBool("silent")
			payload := map[string]any{"message_id": msgID, "silent": silent, "unpin": unpin}
			return runWrite(cmd, name, method, selector, cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					if err := c.Pin(ctx, client.PinReq{ChatID: chatID, MessageID: msgID, Silent: silent, Unpin: unpin}); err != nil {
						return nil, err
					}
					return map[string]any{"message_id": msgID, "unpin": unpin}, nil
				},
			)
		},
	}
	cmd.Flags().Bool("silent", false, "Pin silently (no notification)")
	addWriteFlags(cmd)
	return cmd
}

// ---- react ----

func reactCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "react <chat> <message-id> <emoji>",
		Short:        "Send a reaction to a message",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			var msgID int64
			if _, err := fmt.Sscan(args[1], &msgID); err != nil {
				return safety.NewBadArgs("message-id must be an integer (got %q)", args[1])
			}
			emoji := args[2]
			big, _ := cmd.Flags().GetBool("big")
			payload := map[string]any{"message_id": msgID, "emoji": emoji, "big": big}
			if emoji == "" {
				// Surface as a dispatch-classified BadArgs so the envelope
				// + exit code match every other write-side validation error.
				return emitDispatchedFailure(cmd, "react", safety.NewBadArgs("emoji cannot be empty; pass --remove to clear reactions"))
			}
			return runWrite(cmd, "react", "messages.SendReaction", selector, cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					if err := c.React(ctx, client.ReactReq{ChatID: chatID, MessageID: msgID, Emoji: emoji, Big: big}); err != nil {
						return nil, err
					}
					return map[string]any{"message_id": msgID, "emoji": emoji, "big": big}, nil
				},
			)
		},
	}
	cmd.Flags().Bool("big", false, "Send a big reaction (Premium)")
	addWriteFlags(cmd)
	return cmd
}

// ---- mark-read ----

func markReadCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mark-read <chat>",
		Short:        "Mark history read up to and including --up-to",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			upTo, _ := cmd.Flags().GetInt64("up-to")
			payload := map[string]any{"up_to_id": upTo}
			return runWrite(cmd, "mark-read", "messages.ReadHistory", selector, cfg, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					if err := c.MarkRead(ctx, client.MarkReadReq{ChatID: chatID, UpToID: upTo}); err != nil {
						return nil, err
					}
					return map[string]any{"up_to_id": upTo}, nil
				},
			)
		},
	}
	cmd.Flags().Int64("up-to", 0, "Mark read up to and including this message id; 0 means latest")
	addWriteFlags(cmd)
	return cmd
}

func parseIntCSV(s string) ([]int64, error) {
	if strings.TrimSpace(s) == "" {
		return nil, safety.NewBadArgs("message-ids cannot be empty")
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		var v int64
		if _, err := fmt.Sscan(p, &v); err != nil {
			return nil, safety.NewBadArgs("message-id must be an integer (got %q)", p)
		}
		out = append(out, v)
	}
	return out, nil
}
