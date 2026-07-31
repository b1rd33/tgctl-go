package commands

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
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

			// The cap preflight is deliberately schema-agnostic and read-only. It
			// runs before migrations, client construction, audit, or session I/O.
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
			preflightDB.Close()

			// A permitted backfill uses the normal writable connection here so
			// supported legacy schemas are migrated before any column-dependent
			// selector/count query. No Telegram request has happened yet.
			chatID, title, current, dbSize, err := prepareBackfillDB(paths.dbPath, args[0])
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if dbCapBytes > 0 && dbSize >= dbCapBytes {
				return emitDispatchedFailure(cmd, "backfill", safety.NewBadArgs(
					"backfill database cap reached after migration: current size %d bytes >= --max-db-size-mb %d", dbSize, maxDBSizeMB))
			}
			if current >= maxMessages {
				return emitDispatchedFailure(cmd, "backfill", safety.NewBadArgs(
					"backfill cap reached: current message count %d >= --max-messages %d", current, maxMessages))
			}
			code := dispatch.Run("backfill", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: paths.auditPath, Args: map[string]any{
					"chat": args[0], "max_messages": maxMessages, "max_db_size_mb": maxDBSizeMB,
					"throttle_seconds": throttleSeconds, "download_media": downloadMedia,
				},
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
				backfillDBMu.Lock()
				defer backfillDBMu.Unlock()
				db, err := store.Connect(paths.dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				inserted, dbCapReached, dbSize, err := insertBackfillRowsAtomic(
					ctx, db, chatID, maxMessages, dbCapBytes, rows,
				)
				if err != nil {
					return nil, err
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
	cmd.Flags().Int("max-messages", 100, "Maximum cached messages per chat (maximum 10000)")
	cmd.Flags().Int("max-db-size-mb", 0, "Maximum main database plus WAL allocation in MiB (0 disables the cap)")
	cmd.Flags().Bool("download-media", false, "Download media during backfill")
	cmd.Flags().Float64("throttle-seconds", 0, "Seconds to sleep between Telegram history pages")
	addLocalWriteFlags(cmd)
	return cmd
}

func validateBackfillLimits(maxMessages, maxDBSizeMB int, throttleSeconds float64) error {
	if maxMessages <= 0 {
		return safety.NewBadArgs("--max-messages must be greater than zero")
	}
	if maxMessages > client.MaxBackfillMessages {
		return safety.NewBadArgs("--max-messages must not exceed %d", client.MaxBackfillMessages)
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

// databaseSizeBytes reports the storage governed by --max-db-size-mb: SQLite's
// logical main allocation (page_count * page_size) plus the WAL file bytes.
// The -shm file is excluded because it is transient coordination metadata, not
// database or recovery-log data allocation.
func databaseSizeBytes(db *sql.DB) (int64, error) {
	return databaseSizeBytesContext(context.Background(), db)
}

type sqliteQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func databaseSizeBytesContext(ctx context.Context, db sqliteQueryRower) (int64, error) {
	var pageCount, pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("read SQLite page_count: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read SQLite page_size: %w", err)
	}
	if pageCount < 0 || pageSize < 0 || (pageSize > 0 && pageCount > math.MaxInt64/pageSize) {
		return 0, fmt.Errorf("invalid SQLite page accounting: page_count=%d page_size=%d", pageCount, pageSize)
	}
	mainBytes := pageCount * pageSize
	var seq int
	var name, path string
	if err := db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		return 0, fmt.Errorf("read SQLite database path: %w", err)
	}
	walBytes := int64(0)
	if info, err := os.Stat(path + "-wal"); err == nil {
		walBytes = info.Size()
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("stat SQLite WAL: %w", err)
	}
	if walBytes < 0 || mainBytes > math.MaxInt64-walBytes {
		return 0, fmt.Errorf("invalid SQLite WAL accounting: main=%d wal=%d", mainBytes, walBytes)
	}
	return mainBytes + walBytes, nil
}

var backfillDBMu sync.Mutex

func prepareBackfillDB(dbPath, selector string) (chatID int64, title string, count int, size int64, retErr error) {
	backfillDBMu.Lock()
	defer backfillDBMu.Unlock()
	db, err := store.Connect(dbPath)
	if err != nil {
		return 0, "", 0, 0, err
	}
	defer db.Close()
	chatID, title, err = resolve.ResolveChatDB(db, selector)
	if err != nil {
		return 0, "", 0, 0, err
	}
	count, err = countCachedMessages(db, chatID)
	if err != nil {
		return 0, "", 0, 0, err
	}
	size, err = databaseSizeBytes(db)
	if err != nil {
		return 0, "", 0, 0, err
	}
	return chatID, title, count, size, nil
}

type sqliteContextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// insertBackfillRowsAtomic performs no network work. BEGIN IMMEDIATE makes the
// count check and inserts one SQLite writer critical section. For a byte cap,
// WAL is first checkpointed and the short transaction uses DELETE journaling:
// page_count then describes the exact candidate main allocation, and rolling
// back a savepoint truncates an oversized candidate instead of ratifying an
// arbitrary one-row overshoot. WAL mode is restored before returning.
func insertBackfillRowsAtomic(
	ctx context.Context,
	db *sql.DB,
	chatID int64,
	maxMessages int,
	dbCapBytes int64,
	rows []client.BackfillMessage,
) (inserted int, capReached bool, finalSize int64, retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, false, 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=10000"); err != nil {
		return 0, false, 0, err
	}

	restoreWAL := false
	if dbCapBytes > 0 {
		var busy, logFrames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
			return 0, false, 0, fmt.Errorf("checkpoint SQLite WAL: %w", err)
		}
		if busy != 0 {
			return 0, false, 0, fmt.Errorf("checkpoint SQLite WAL: database is busy")
		}
		var mode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
			return 0, false, 0, fmt.Errorf("switch SQLite journal mode for capped write: %w", err)
		}
		if !strings.EqualFold(mode, "delete") {
			return 0, false, 0, fmt.Errorf("switch SQLite journal mode for capped write: got %q", mode)
		}
		restoreWAL = true
	}
	defer func() {
		if restoreWAL {
			var mode string
			if err := conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=WAL").Scan(&mode); err != nil && retErr == nil {
				retErr = fmt.Errorf("restore SQLite WAL mode: %w", err)
			} else if err == nil && !strings.EqualFold(mode, "wal") && retErr == nil {
				retErr = fmt.Errorf("restore SQLite WAL mode: got %q", mode)
			}
		}
		if retErr == nil {
			finalSize, retErr = databaseSizeBytesContext(context.Background(), conn)
			if retErr == nil && dbCapBytes > 0 && finalSize >= dbCapBytes {
				capReached = true
			}
		}
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, false, 0, fmt.Errorf("begin atomic backfill: %w", err)
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var current int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND (deleted=0 OR deleted IS NULL)", chatID,
	).Scan(&current); err != nil {
		return 0, false, 0, err
	}
	if dbCapBytes > 0 {
		size, err := databaseSizeBytesContext(ctx, conn)
		if err != nil {
			return 0, false, 0, err
		}
		if size >= dbCapBytes {
			capReached = true
		}
	}
	for _, sourceRow := range rows {
		if current >= maxMessages || capReached {
			break
		}
		row := sourceRow
		if row.ChatID == 0 {
			row.ChatID = chatID
		}
		if _, err := conn.ExecContext(ctx, "SAVEPOINT backfill_candidate"); err != nil {
			return inserted, capReached, 0, err
		}
		if err := insertBackfillMessageContext(ctx, conn, row); err != nil {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK TO backfill_candidate")
			_, _ = conn.ExecContext(context.Background(), "RELEASE backfill_candidate")
			return inserted, capReached, 0, err
		}
		if dbCapBytes > 0 {
			size, err := databaseSizeBytesContext(ctx, conn)
			if err != nil {
				return inserted, capReached, 0, err
			}
			if size > dbCapBytes {
				if _, err := conn.ExecContext(ctx, "ROLLBACK TO backfill_candidate"); err != nil {
					return inserted, true, 0, err
				}
				if _, err := conn.ExecContext(ctx, "RELEASE backfill_candidate"); err != nil {
					return inserted, true, 0, err
				}
				capReached = true
				break
			}
		}
		if _, err := conn.ExecContext(ctx, "RELEASE backfill_candidate"); err != nil {
			return inserted, capReached, 0, err
		}
		inserted++
		current++
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return inserted, capReached, 0, err
	}
	transactionOpen = false
	return inserted, capReached, 0, nil
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
	return insertBackfillMessageContext(context.Background(), db, row)
}

func insertBackfillMessageContext(ctx context.Context, db sqliteContextExecer, row client.BackfillMessage) error {
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
	_, err := db.ExecContext(ctx, `
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
