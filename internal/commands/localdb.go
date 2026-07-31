package commands

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerLocalDBCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(backfillCommand(cfg))
	root.AddCommand(discoverCommand(cfg))
	root.AddCommand(syncContactsCommand(cfg))
}

func addLocalWriteFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("allow-write", false, "Required for local DB writes")
	AddOutputFlags(cmd)
}

func localWriteArgs(cmd *cobra.Command) safety.Args {
	rootCfg := RootConfigFrom(cmd.Root())
	allow, _ := cmd.Flags().GetBool("allow-write")
	return safety.Args{ReadOnly: rootCfg.ReadOnly, AllowWrite: allow}
}

func backfillCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "backfill <chat>",
		Short:        "Backfill cached messages for a chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			maxMessages, _ := cmd.Flags().GetInt("max-messages")
			maxDBSizeMB, _ := cmd.Flags().GetInt("max-db-size-mb")
			throttleSeconds, _ := cmd.Flags().GetFloat64("throttle-seconds")
			downloadMedia, _ := cmd.Flags().GetBool("download-media")
			if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if err := validateBackfillLimits(maxMessages, maxDBSizeMB, throttleSeconds); err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			paths, err := resolveWritePathSet(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			dbCapBytes := int64(maxDBSizeMB) * 1024 * 1024
			throttle := time.Duration(throttleSeconds * float64(time.Second))

			// Cap, selector, and count preflight use a read-only handle. An
			// already-capped DB is rejected without opening a writable handle,
			// constructing a Telegram client, or creating an audit/session file.
			preflightDB, err := store.ConnectReadonly(paths.dbPath)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			dbSize, err := databaseSizeBytes(preflightDB)
			if err != nil {
				preflightDB.Close()
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if dbCapBytes > 0 && dbSize >= dbCapBytes {
				preflightDB.Close()
				return emitDispatchedFailure(cmd, "backfill", safety.NewBadArgs(
					"backfill database cap reached: current size %d bytes >= --max-db-size-mb %d", dbSize, maxDBSizeMB))
			}
			chatID, title, err := resolve.ResolveChatDB(preflightDB, args[0])
			if err != nil {
				preflightDB.Close()
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			current, err := countCachedMessages(preflightDB, chatID)
			preflightDB.Close()
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if current >= maxMessages {
				return emitDispatchedFailure(cmd, "backfill", safety.NewBadArgs(
					"backfill cap reached: current message count %d >= --max-messages %d", current, maxMessages))
			}
			code := dispatch.Run("backfill", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: paths.auditPath, Args: map[string]any{"chat": args[0], "max_messages": maxMessages},
			}, func(ctx context.Context) (any, error) {
				c, err := cfg.ClientFactory(ctx, paths.sessionPath, paths.dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				rows, err := c.BackfillMessages(ctx, client.BackfillReq{
					ChatID: chatID, Limit: maxMessages - current, Throttle: throttle,
				})
				if err != nil {
					return nil, err
				}
				db, err := store.Connect(paths.dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				inserted := 0
				dbCapReached := false
				for _, row := range rows {
					if current+inserted >= maxMessages {
						break
					}
					if dbCapBytes > 0 {
						dbSize, err = databaseSizeBytes(db)
						if err != nil {
							return nil, err
						}
						// Boundary semantics: an insert starts only while allocated
						// bytes are strictly below the cap. SQLite may allocate enough
						// pages for that insert to cross it; the next insert then stops.
						if dbSize >= dbCapBytes {
							dbCapReached = true
							break
						}
					}
					if row.ChatID == 0 {
						row.ChatID = chatID
					}
					if err := insertBackfillMessage(db, row); err != nil {
						return nil, err
					}
					inserted++
				}
				dbSize, err = databaseSizeBytes(db)
				if err != nil {
					return nil, err
				}
				if dbCapBytes > 0 && dbSize >= dbCapBytes {
					dbCapReached = true
				}
				skipped := len(rows) - inserted
				warnings := capWarnings(current+inserted, maxMessages)
				return map[string]any{
					"chats_processed":   1,
					"messages_inserted": inserted,
					"messages_skipped":  skipped,
					"db_size_bytes":     dbSize,
					"db_cap_reached":    dbCapReached,
					"media_downloaded":  0,
					"media_skipped":     0,
					"media_failed":      0,
					"warnings":          warnings,
					"skipped":           skipped,
					"cap_warnings":      warnings,
					"download_media":    downloadMedia,
					"per_chat":          []map[string]any{{"chat_id": chatID, "title": title, "messages_inserted": inserted}},
				}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int("max-messages", 100, "Maximum cached messages per chat")
	cmd.Flags().Int("max-db-size-mb", 0, "Maximum allocated database size in MiB (0 disables the cap)")
	cmd.Flags().Bool("download-media", false, "Download media during backfill")
	cmd.Flags().Float64("throttle-seconds", 0, "Seconds to sleep between Telegram history pages")
	addLocalWriteFlags(cmd)
	return cmd
}

func validateBackfillLimits(maxMessages, maxDBSizeMB int, throttleSeconds float64) error {
	if maxMessages <= 0 {
		return safety.NewBadArgs("--max-messages must be greater than zero")
	}
	if maxDBSizeMB < 0 {
		return safety.NewBadArgs("--max-db-size-mb must be zero or greater")
	}
	if int64(maxDBSizeMB) > math.MaxInt64/(1024*1024) {
		return safety.NewBadArgs("--max-db-size-mb is too large")
	}
	maxThrottleSeconds := float64(math.MaxInt64) / float64(time.Second)
	if math.IsNaN(throttleSeconds) || math.IsInf(throttleSeconds, 0) || throttleSeconds < 0 || throttleSeconds > maxThrottleSeconds {
		return safety.NewBadArgs("--throttle-seconds must be a finite non-negative duration")
	}
	return nil
}

// databaseSizeBytes reports SQLite's currently allocated main-database size.
// The accounting intentionally follows SQLite's page_count * page_size rather
// than filesystem length, which can vary with journaling/checkpoint state.
func databaseSizeBytes(db *sql.DB) (int64, error) {
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("read SQLite page_count: %w", err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read SQLite page_size: %w", err)
	}
	if pageCount < 0 || pageSize < 0 || (pageSize > 0 && pageCount > math.MaxInt64/pageSize) {
		return 0, fmt.Errorf("invalid SQLite page accounting: page_count=%d page_size=%d", pageCount, pageSize)
	}
	return pageCount * pageSize, nil
}

func discoverCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "discover",
		Short:        "Discover dialogs and cache chat metadata",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			if limit <= 0 {
				limit = 200
			}
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "discover", pathErr)
			}
			code := dispatch.Run("discover", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: map[string]any{"limit": limit},
			}, func(ctx context.Context) (any, error) {
				if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
					return nil, err
				}
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				dialogs, err := c.DiscoverDialogs(ctx, limit)
				if err != nil {
					return nil, err
				}
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				for _, d := range dialogs {
					upsertChatRow(db, d.ID, d.Type, d.Title, d.Username)
				}
				return map[string]any{"chats": dialogs, "discovered": len(dialogs)}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int("limit", 200, "Maximum dialogs to fetch")
	addLocalWriteFlags(cmd)
	return cmd
}

func syncContactsCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sync-contacts",
		Short:        "Sync Telegram contacts into the local DB",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "sync-contacts", pathErr)
			}
			code := dispatch.Run("sync-contacts", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath,
			}, func(ctx context.Context) (any, error) {
				if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
					return nil, err
				}
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				contacts, err := c.SyncContacts(ctx)
				if err != nil {
					return nil, err
				}
				db, err := store.Connect(dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				for _, contact := range contacts {
					if err := upsertContact(db, contact); err != nil {
						return nil, err
					}
				}
				return map[string]any{"synced": len(contacts), "contacts": contacts}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	addLocalWriteFlags(cmd)
	return cmd
}

func countCachedMessages(db *sql.DB, chatID int64) (int, error) {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND (deleted=0 OR deleted IS NULL)", chatID).Scan(&n)
	return n, err
}

func capWarnings(count, max int) []string {
	if max > 0 && count*100 >= max*80 {
		return []string{fmt.Sprintf("message cap warning: cached %d of --max-messages %d (>=80%%)", count, max)}
	}
	return []string{}
}

func insertBackfillMessage(db *sql.DB, row client.BackfillMessage) error {
	var sender, reply any
	if row.SenderID != 0 {
		sender = row.SenderID
	}
	if row.ReplyToMsgID != 0 {
		reply = row.ReplyToMsgID
	}
	date := row.Date
	if date == "" {
		date = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(`
		INSERT INTO tg_messages(chat_id, message_id, sender_id, date, text, is_outgoing,
			reply_to_msg_id, has_media, media_type, media_path, raw_json, deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			sender_id=excluded.sender_id, date=excluded.date, text=excluded.text,
			is_outgoing=excluded.is_outgoing, reply_to_msg_id=excluded.reply_to_msg_id,
			has_media=excluded.has_media, media_type=excluded.media_type,
			media_path=excluded.media_path, raw_json=excluded.raw_json, deleted=0`,
		row.ChatID, row.MessageID, sender, date, nullIfEmpty(row.Text), localDBBoolInt(row.IsOutgoing),
		reply, localDBBoolInt(row.HasMedia), nullIfEmpty(row.MediaType), nullIfEmpty(row.MediaPath), nullIfEmpty(row.RawJSON))
	return err
}

func upsertContact(db *sql.DB, c client.ContactInfo) error {
	_, err := db.Exec(`
		INSERT INTO tg_contacts(user_id, phone, first_name, last_name, username, is_mutual, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			phone=excluded.phone, first_name=excluded.first_name, last_name=excluded.last_name,
			username=excluded.username, is_mutual=excluded.is_mutual, synced_at=excluded.synced_at`,
		c.UserID, nullIfEmpty(c.Phone), nullIfEmpty(c.FirstName), nullIfEmpty(c.LastName),
		nullIfEmpty(c.Username), localDBBoolInt(c.IsMutual), time.Now().UTC().Format(time.RFC3339))
	return err
}

func localDBBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
