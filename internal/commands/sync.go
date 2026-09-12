package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// syncCommand turns the existing backfill and listen primitives into a
// restart-safe local archive loop. It deliberately keeps Telegram writes out
// of scope: only the account's local SQLite cache is mutated.
func syncCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sync <chat>",
		Short:        "Synchronize cached messages and optionally follow updates",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
				return emitDispatchedFailure(cmd, "sync", err)
			}
			maxMessages, _ := cmd.Flags().GetInt("max-messages")
			follow, _ := cmd.Flags().GetBool("follow")
			once, _ := cmd.Flags().GetBool("once")
			downloadMedia, _ := cmd.Flags().GetBool("download-media")
			maxMediaSizeMB, _ := cmd.Flags().GetInt("max-media-size-mb")
			overwriteMedia, _ := cmd.Flags().GetBool("overwrite-media")
			if maxMessages <= 0 || maxMessages > client.MaxBackfillMessages {
				return emitDispatchedFailure(cmd, "sync", safety.NewBadArgs("--max-messages must be between 1 and %d", client.MaxBackfillMessages))
			}
			if once && !follow {
				return emitDispatchedFailure(cmd, "sync", safety.NewBadArgs("--once requires --follow"))
			}
			maxMediaBytes, err := downloadMaxBytes(int64(maxMediaSizeMB))
			if err != nil {
				return emitDispatchedFailure(cmd, "sync", err)
			}
			backoffMax, _ := cmd.Flags().GetFloat64("backoff-max-seconds")
			if math.IsNaN(backoffMax) || math.IsInf(backoffMax, 0) || backoffMax < 0 || backoffMax > 3600 {
				return emitDispatchedFailure(cmd, "sync", safety.NewBadArgs("--backoff-max-seconds must be between 0 and 3600"))
			}
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "sync", err)
			}
			paths, err := resolveDownloadMediaPaths(cfg.Paths, account)
			if err != nil {
				return emitDispatchedFailure(cmd, "sync", err)
			}
			auditArgs := map[string]any{"chat": args[0], "account": account, "follow": follow, "once": once, "max_messages": maxMessages, "backoff_max_seconds": backoffMax}
			code := dispatch.Run("sync", dispatch.Options{Context: cmd.Context(),
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: paths.auditPath, Args: auditArgs, DurableAudit: true,
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(paths.dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				chatID, title, err := resolve.ResolveChatDB(db, args[0])
				if err != nil {
					return nil, err
				}
				state, stateErr := store.LoadSyncState(db, account, chatID)
				if errors.Is(stateErr, sql.ErrNoRows) {
					state = store.SyncState{Account: account, ChatID: chatID}
				} else if stateErr != nil {
					return nil, stateErr
				}
				factory := cfg.ClientFactory
				if factory == nil {
					return nil, errors.New("sync client factory is not configured")
				}
				telegramClient, err := factory(ctx, paths.sessionPath, paths.dbPath)
				if err != nil {
					return nil, err
				}
				closeClient := func() error {
					if telegramClient == nil {
						return nil
					}
					err := telegramClient.Close()
					telegramClient = nil
					return err
				}
				defer closeClient()

				backfill, backfillErr := telegramClient.BackfillMessages(ctx, client.BackfillReq{
					ChatID: chatID, Limit: maxMessages, DownloadMedia: downloadMedia, AfterMessageID: state.LastMessageID,
					MediaDir: filepath.Join(paths.mediaDir, strconv.FormatInt(chatID, 10)), MaxMediaBytes: maxMediaBytes, OverwriteMedia: overwriteMedia,
				})
				if backfill.Truncated && state.LastMessageID > 0 && backfillErr == nil {
					backfillErr = fmt.Errorf("catch-up exceeds --max-messages; increase the limit before advancing history coverage")
				}
				tx, err := db.Begin()
				if err != nil {
					return nil, err
				}
				defer tx.Rollback()
				persisted, highest, persistErr := persistSyncBackfill(tx, backfill)
				if persistErr != nil {
					return nil, persistErr
				}
				if backfillErr != nil {
					if err := tx.Commit(); err != nil {
						return nil, err
					}
					return map[string]any{"chat_id": chatID, "title": title, "messages_persisted": persisted, "last_message_id": state.LastMessageID}, backfillErr
				}
				if highest > state.LastMessageID {
					state.LastMessageID = highest
				}
				state.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
				state.UpdatedAt = state.LastSyncAt
				if err := store.SaveSyncState(tx, state); err != nil {
					return nil, err
				}

				if err := tx.Commit(); err != nil {
					return nil, err
				}
				result := map[string]any{"history_truncated": backfill.Truncated, "history_next_offset_id": backfill.NextOffsetID, "chat_id": chatID, "title": title, "messages_persisted": persisted, "last_message_id": state.LastMessageID, "events": 0, "following": follow}
				if !follow {
					return result, nil
				}
				backoff := 100 * time.Millisecond
				maxDelay := time.Duration(backoffMax * float64(time.Second))
				if maxDelay <= 0 {
					maxDelay = 5 * time.Second
				}
				backoff = min(backoff, maxDelay)
				for {
					event, listenErr := telegramClient.ListenOnce(ctx)
					if listenErr != nil {
						if stopSyncReconnect(listenErr) {
							return result, listenErr
						}
						if ctx.Err() != nil {
							return result, ctx.Err()
						}
						_ = closeClient()
						for telegramClient == nil {
							if err := sleepContext(ctx, backoff); err != nil {
								return result, err
							}
							telegramClient, err = factory(ctx, paths.sessionPath, paths.dbPath)
							if err != nil {
								if stopSyncReconnect(err) {
									return result, err
								}
								telegramClient = nil
								backoff = min(backoff*2, maxDelay)
								continue
							}
							if telegramClient == nil {
								return result, fmt.Errorf("client factory returned no client")
							}
						}
						backoff = min(100*time.Millisecond, maxDelay)
						continue
					}
					backoff = min(100*time.Millisecond, maxDelay)
					if event.ChatID != chatID {
						if err := client.AcknowledgeListenEvent(ctx, telegramClient, event); err != nil {
							return result, err
						}
						continue
					}
					if err := applyLiveEvent(db, event); err != nil {
						return result, err
					}
					if event.MessageID > state.LastMessageID {
						state.LastMessageID = event.MessageID
					}
					state.LastSyncAt = time.Now().UTC().Format(time.RFC3339)
					state.UpdatedAt = state.LastSyncAt
					if err := store.SaveSyncState(db, state); err != nil {
						return result, err
					}
					if err := client.AcknowledgeListenEvent(ctx, telegramClient, event); err != nil {
						return result, err
					}
					result["events"] = result["events"].(int) + 1
					result["last_message_id"] = state.LastMessageID
					if once {
						return result, nil
					}
				}
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Bool("follow", false, "Continue listening after the initial catch-up")
	cmd.Flags().Bool("once", false, "With --follow, stop after one matching live event")
	cmd.Flags().Int("max-messages", 100, "Maximum history messages to reconcile")
	cmd.Flags().Float64("backoff-max-seconds", 30, "Maximum reconnect delay in seconds")
	cmd.Flags().Bool("download-media", false, "Download media during catch-up")
	cmd.Flags().Int("max-media-size-mb", 100, "Maximum media file size during catch-up")
	cmd.Flags().Bool("overwrite-media", false, "Overwrite existing catch-up media")
	addLocalWriteFlags(cmd)
	return cmd
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func persistSyncBackfill(db store.DBTX, result client.BackfillResult) (persisted int, highest int64, err error) {
	for _, row := range result.Messages {
		text, mediaType, mediaPath, mediaIdentity := row.Text, row.MediaType, row.MediaPath, row.MediaIdentity
		if upsertErr := store.UpsertLiveMessage(db, store.LiveMessage{
			ChatID: row.ChatID, MessageID: row.MessageID, SenderID: optEventSender(row.SenderID), Date: row.Date,
			Text: optEventString(text), IsOutgoing: row.IsOutgoing, ReplyToMsgID: optEventSender(row.ReplyToMsgID), HasMedia: row.HasMedia,
			MediaType: optEventString(mediaType), MediaPath: optEventString(mediaPath), MediaIdentity: optEventString(mediaIdentity), GroupedID: row.GroupedID,
			RawJSON: optEventString(row.RawJSON), EditDate: row.EditDate,
		}); upsertErr != nil {
			return persisted, highest, upsertErr
		}
		persisted++
		if row.MessageID > highest {
			highest = row.MessageID
		}
	}
	return persisted, highest, nil
}

func stopSyncReconnect(err error) bool {
	var credentials *safety.MissingCredentials
	var permission *safety.PermissionDenied
	var wait *safety.FloodWait
	var locked *safety.SessionLocked
	var args *safety.BadArgs
	return errors.Is(err, client.ErrRecoveryIncomplete) || errors.As(err, &credentials) || errors.As(err, &permission) || errors.As(err, &wait) || errors.As(err, &locked) || errors.As(err, &args)
}
