package commands

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerLiveCommands(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{
		Use:          "listen",
		Short:        "Listen for live Telegram updates",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			once, _ := cmd.Flags().GetBool("once")
			onlyDMs, _ := cmd.Flags().GetBool("only-dms")
			onlyGroups, _ := cmd.Flags().GetBool("only-groups")
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "listen", pathErr)
			}
			code := dispatch.Run("listen", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath,
			}, func(ctx context.Context) (any, error) {
				if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
					return nil, err
				}
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				emit := func(event client.ListenEvent) error {
					if err := cacheListenEvent(dbPath, event); err != nil {
						return fmt.Errorf("persist live update: %w", err)
					}
					env := output.Success("listen.event", event, output.NewRequestID(), nil)
					output.Emit(env, output.EmitOptions{JSON: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
					return nil
				}
				if once {
					for {
						event, err := c.ListenOnce(ctx)
						if err != nil {
							return nil, err
						}
						if !shouldEmitListenEvent(event, onlyDMs, onlyGroups) {
							continue
						}
						if err := emit(event); err != nil {
							return nil, err
						}
						return map[string]any{"events": 1}, nil
					}
				}
				for {
					event, err := c.ListenOnce(ctx)
					if err != nil {
						return nil, err
					}
					if !shouldEmitListenEvent(event, onlyDMs, onlyGroups) {
						continue
					}
					if err := emit(event); err != nil {
						return nil, err
					}
					time.Sleep(100 * time.Millisecond)
				}
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Bool("once", false, "Exit after one filter-matching update")
	cmd.Flags().Bool("only-dms", false, "Emit only 1-on-1 user messages; skip groups/channels")
	cmd.Flags().Bool("only-groups", false, "Emit only group/channel messages; skip 1-on-1 DMs")
	cmd.MarkFlagsMutuallyExclusive("only-dms", "only-groups")
	addLocalWriteFlags(cmd)
	root.AddCommand(cmd)
}

// shouldEmitListenEvent decides whether a listen event passes the user's
// --only-dms / --only-groups filters.
//
// Telegram routes incoming chat events through two update_kinds:
//   - "new_message"     : DMs (chat_id == positive user_id) AND basic
//     groups (chat_id < 0). Basic groups are rare in
//     modern Telegram; almost every group is a supergroup.
//   - "channel_message" : channels and supergroups (chat_id always positive,
//     the channel's id space).
//
// A 1-on-1 DM is uniquely identified by update_kind == "new_message" AND
// chat_id > 0. Anything else with a chat target is a group/channel/supergroup.
//
// Events without a chat target (status changes, etc.) have ChatID == 0; they
// always pass through regardless of filter so callers can still observe them.
func shouldEmitListenEvent(event client.ListenEvent, onlyDMs, onlyGroups bool) bool {
	if !onlyDMs && !onlyGroups {
		return true
	}
	if event.ChatID == 0 {
		return true
	}
	isDM := event.UpdateKind == "new_message" && event.ChatID > 0
	if onlyDMs {
		return isDM
	}
	return !isDM
}

func cacheListenEvent(dbPath string, event client.ListenEvent) error {
	if event.ChatID == 0 || event.MessageID == 0 {
		return nil
	}
	db, err := store.Connect(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return applyLiveEvent(db, event)
}

func applyLiveEvent(db *sql.DB, event client.ListenEvent) error {
	if event.ChatID == 0 || event.MessageID == 0 {
		return nil
	}
	if event.Deleted {
		return store.MarkLiveMessagesDeleted(db, event.ChatID, []int64{event.MessageID})
	}
	text := event.Text
	date := event.Date
	if date == "" {
		date = time.Now().UTC().Format(time.RFC3339)
	}
	return store.UpsertLiveMessage(db, store.LiveMessage{
		ChatID: event.ChatID, MessageID: event.MessageID, SenderID: optEventSender(event.SenderID),
		Date: date, Text: &text, GroupedID: event.GroupedID, HasMedia: event.MediaType != "",
		MediaType: optEventString(event.MediaType), Deleted: event.Deleted,
	})
}

func optEventSender(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func optEventString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
