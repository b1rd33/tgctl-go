package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
			maxMediaSizeMB, _ := cmd.Flags().GetInt("max-media-size-mb")
			overwriteMedia, _ := cmd.Flags().GetBool("overwrite-media")
			if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if err := validateBackfillLimits(maxMessages, maxDBSizeMB, maxMediaSizeMB); err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			throttle, err := backfillThrottleDuration(throttleSeconds)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			mediaPaths, err := resolveDownloadMediaPaths(cfg.Paths, account)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			paths := resolvedWritePaths{
				dbPath: mediaPaths.dbPath, sessionPath: mediaPaths.sessionPath, auditPath: mediaPaths.auditPath,
			}
			dbCapBytes := int64(maxDBSizeMB) * 1024 * 1024
			maxMediaBytes := int64(maxMediaSizeMB) * 1024 * 1024

			// The cap preflight is deliberately schema-agnostic and read-only. It
			// runs before migrations, client construction, audit, or session I/O.
			dbSize, err := readBackfillDBSizePreflight(paths.dbPath)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill", err)
			}
			if dbCapBytes > 0 && dbSize >= dbCapBytes {
				return emitDispatchedFailure(cmd, "backfill", safety.NewBadArgs(
					"backfill database cap reached: current size %d bytes >= --max-db-size-mb %d", dbSize, maxDBSizeMB))
			}

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
			mediaDir := ""
			if downloadMedia {
				mediaDir, err = filepath.Abs(filepath.Clean(filepath.Join(mediaPaths.mediaDir, fmt.Sprint(chatID))))
				if err != nil {
					return emitDispatchedFailure(cmd, "backfill", fmt.Errorf("resolve backfill media directory: %w", err))
				}
			}
			auditArgs := map[string]any{
				"chat": args[0], "max_messages": maxMessages, "max_db_size_mb": maxDBSizeMB,
				"throttle_seconds": throttleSeconds, "download_media": downloadMedia,
				"max_media_size_mb": maxMediaSizeMB, "overwrite_media": overwriteMedia,
				"media_dir_policy": "account-chat",
			}
			recoveryExtras := map[string]any{}
			code := dispatch.Run("backfill", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: paths.auditPath, Args: auditArgs, DurableAudit: true, CommittedExtras: recoveryExtras,
			}, func(ctx context.Context) (any, error) {
				c, err := cfg.ClientFactory(ctx, paths.sessionPath, paths.dbPath)
				if err != nil {
					return nil, err
				}
				result, err := c.BackfillMessages(ctx, client.BackfillReq{
					ChatID: chatID, Limit: maxMessages - current, Throttle: throttle,
					DownloadMedia: downloadMedia, MediaDir: mediaDir, MaxMediaBytes: maxMediaBytes,
					OverwriteMedia: overwriteMedia,
				})
				recountBackfillMedia(&result)
				setBackfillRecoveryExtras(recoveryExtras, result, mediaDir)
				setBackfillAuditResult(auditArgs, result)
				clientCloseErr := c.Close()
				if operationErr := errors.Join(err, clientCloseErr); operationErr != nil {
					return nil, wrapBackfillMediaCommit(operationErr, result, mediaDir, "backfill media committed before Telegram finalization failed")
				}
				unlock, err := lockBackfillDBPath(paths.dbPath)
				if err != nil {
					return nil, wrapBackfillMediaCommit(err, result, mediaDir, "backfill media committed before database locking failed")
				}
				defer unlock()
				db, err := store.Connect(paths.dbPath)
				if err != nil {
					return nil, wrapBackfillMediaCommit(err, result, mediaDir, "backfill media committed before database open failed")
				}
				inserted, dbCapReached, dbSize, finalCount, capSkippedMedia, err := insertBackfillRowsAtomic(
					ctx, db, chatID, maxMessages, dbCapBytes, result.Messages,
				)
				if err != nil {
					return nil, wrapBackfillMediaCommit(errors.Join(err, db.Close()), result, mediaDir, "backfill media committed before database insertion finalized")
				}
				if closeErr := db.Close(); closeErr != nil {
					return nil, safety.NewCommittedWriteWithExtras("backfill database committed but close failed", closeErr, mergedBackfillExtras(closeErr, recoveryExtras))
				}
				skipped := len(result.Messages) - inserted
				capOnlyWarnings := capWarnings(finalCount, maxMessages)
				if capSkippedMedia > 0 {
					capOnlyWarnings = append(capOnlyWarnings, capSkippedMediaWarning(capSkippedMedia))
				}
				warnings := append([]string{}, result.Warnings...)
				warnings = append(warnings, capOnlyWarnings...)
				auditArgs["warning_count"] = len(warnings)
				auditArgs["warnings"] = boundedBackfillWarnings(warnings, 20)
				auditArgs["cap_skipped_media"] = capSkippedMedia
				return map[string]any{
					"chats_processed":   1,
					"messages_inserted": inserted,
					"messages_skipped":  skipped,
					"db_size_bytes":     dbSize,
					"db_cap_reached":    dbCapReached,
					"media_downloaded":  result.MediaDownloaded,
					"media_skipped":     result.MediaSkipped,
					"media_failed":      result.MediaFailed,
					"warnings":          warnings,
					"skipped":           skipped,
					"cap_warnings":      capOnlyWarnings,
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
	cmd.Flags().Int("max-media-size-mb", 100, "Maximum size per downloaded media file in MiB (0 disables the limit)")
	cmd.Flags().Bool("overwrite-media", false, "Overwrite existing backfill media files")
	cmd.Flags().Float64("throttle-seconds", 0, "Seconds to sleep between Telegram history pages")
	addLocalWriteFlags(cmd)
	return cmd
}

func validateBackfillLimits(maxMessages, maxDBSizeMB, maxMediaSizeMB int) error {
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
	if maxMediaSizeMB < 0 {
		return safety.NewBadArgs("--max-media-size-mb must be zero or greater")
	}
	if int64(maxMediaSizeMB) > math.MaxInt64/(1024*1024) {
		return safety.NewBadArgs("--max-media-size-mb is too large")
	}
	return nil
}

func backfillThrottleDuration(seconds float64) (time.Duration, error) {
	// float64(math.MaxInt64) rounds upward to 1<<63. Step down to the largest
	// representable seconds value whose nanosecond product still fits int64.
	maxSafeSeconds := math.Nextafter(float64(math.MaxInt64)/float64(time.Second), 0)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > maxSafeSeconds {
		return 0, safety.NewBadArgs("--throttle-seconds must be a finite non-negative duration no greater than %g", maxSafeSeconds)
	}
	d := time.Duration(seconds * float64(time.Second))
	if seconds > 0 && d <= 0 {
		return 0, safety.NewBadArgs("--throttle-seconds overflows time.Duration")
	}
	return d, nil
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

type backfillPathLock struct {
	mu   sync.Mutex
	refs int
}

var backfillPathLockRegistry = struct {
	sync.Mutex
	locks map[string]*backfillPathLock
}{locks: make(map[string]*backfillPathLock)}

func normalizedBackfillDBPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	normalized := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
		normalized = filepath.Clean(resolved)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return normalized, nil
}

func lockBackfillDBPath(path string) (func(), error) {
	key, err := normalizedBackfillDBPath(path)
	if err != nil {
		return nil, err
	}
	backfillPathLockRegistry.Lock()
	entry := backfillPathLockRegistry.locks[key]
	if entry == nil {
		entry = &backfillPathLock{}
		backfillPathLockRegistry.locks[key] = entry
	}
	entry.refs++
	backfillPathLockRegistry.Unlock()
	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			backfillPathLockRegistry.Lock()
			entry.refs--
			if entry.refs == 0 {
				delete(backfillPathLockRegistry.locks, key)
			}
			backfillPathLockRegistry.Unlock()
		})
	}, nil
}

var backfillDBClose = func(db *sql.DB) error { return db.Close() }

func closeBackfillPreflightDB(db *sql.DB, operationErr error) error {
	closeErr := backfillDBClose(db)
	if closeErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, fmt.Errorf("close backfill preflight database: %w", closeErr))
}

func prepareBackfillDB(dbPath, selector string) (chatID int64, title string, count int, size int64, retErr error) {
	unlock, err := lockBackfillDBPath(dbPath)
	if err != nil {
		return 0, "", 0, 0, err
	}
	defer unlock()
	db, err := store.Connect(dbPath)
	if err != nil {
		return 0, "", 0, 0, err
	}
	chatID, title, err = resolve.ResolveChatDB(db, selector)
	if err != nil {
		return 0, "", 0, 0, closeBackfillPreflightDB(db, err)
	}
	count, err = countCachedMessages(db, chatID)
	if err != nil {
		return 0, "", 0, 0, closeBackfillPreflightDB(db, err)
	}
	size, err = databaseSizeBytes(db)
	if err != nil {
		return 0, "", 0, 0, closeBackfillPreflightDB(db, err)
	}
	if err := closeBackfillPreflightDB(db, nil); err != nil {
		return 0, "", 0, 0, err
	}
	return chatID, title, count, size, nil
}

func readBackfillDBSizePreflight(dbPath string) (size int64, retErr error) {
	unlock, err := lockBackfillDBPath(dbPath)
	if err != nil {
		return 0, err
	}
	defer unlock()
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := db.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	return databaseSizeBytes(db)
}

type sqliteContextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// insertBackfillRowsAtomic performs no network work. Uncapped writes use BEGIN
// IMMEDIATE. For a byte cap, one dedicated connection holds SQLite's exclusive
// lock from the WAL checkpoint through the DELETE-journal transaction and WAL
// restoration. page_count then describes the exact candidate main allocation,
// and rolling back a savepoint truncates an oversized candidate instead of
// ratifying an arbitrary one-row overshoot.
func insertBackfillRowsAtomic(
	ctx context.Context,
	db *sql.DB,
	chatID int64,
	maxMessages int,
	dbCapBytes int64,
	rows []client.BackfillMessage,
) (inserted int, capReached bool, finalSize int64, finalCount int, capSkippedMedia int, retErr error) {
	return insertBackfillRowsAtomicWithHooks(ctx, db, chatID, maxMessages, dbCapBytes, rows, backfillAtomicHooks{})
}

type backfillAtomicHooks struct {
	beforeCommit func() error
	afterCommit  func() error
}

func insertBackfillRowsAtomicWithHooks(
	ctx context.Context,
	db *sql.DB,
	chatID int64,
	maxMessages int,
	dbCapBytes int64,
	rows []client.BackfillMessage,
	hooks backfillAtomicHooks,
) (inserted int, capReached bool, finalSize int64, finalCount int, capSkippedMedia int, retErr error) {
	// sql.Conn.Close normally returns the driver connection to the pool. An
	// exclusive-locking connection must instead be physically closed before the
	// operation boundary, otherwise SQLite can retain its file locks while idle.
	db.SetMaxIdleConns(0)
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, false, 0, 0, 0, err
	}
	committed := false
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			wrapped := fmt.Errorf("close dedicated SQLite backfill connection: %w", closeErr)
			if committed {
				var committedErr *safety.CommittedWrite
				if errors.As(retErr, &committedErr) {
					retErr = safety.NewCommittedWrite(committedErr.Msg, errors.Join(committedErr.Err, wrapped))
				} else {
					retErr = safety.NewCommittedWrite("backfill rows committed but SQLite finalization failed", errors.Join(retErr, wrapped))
				}
			} else {
				retErr = errors.Join(retErr, wrapped)
			}
		}
	}()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=500"); err != nil {
		return 0, false, 0, 0, 0, err
	}

	transactionOpen := false
	exclusiveLocking := false
	deleteJournal := false
	rollbackTransaction := func() error {
		if !transactionOpen {
			return nil
		}
		_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
		if rollbackErr == nil {
			transactionOpen = false
			return nil
		}
		return fmt.Errorf("rollback SQLite backfill transaction: %w", rollbackErr)
	}
	restoreConnection := func() error {
		var cleanupErr error
		if deleteJournal {
			var mode string
			if err := conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore SQLite WAL mode: %w", err))
			} else if !strings.EqualFold(mode, "wal") {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore SQLite WAL mode: got %q", mode))
			} else {
				deleteJournal = false
			}
		}
		if exclusiveLocking {
			var mode string
			if err := conn.QueryRowContext(context.Background(), "PRAGMA locking_mode=NORMAL").Scan(&mode); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore SQLite normal locking mode: %w", err))
			} else if !strings.EqualFold(mode, "normal") {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore SQLite normal locking mode: got %q", mode))
			} else {
				exclusiveLocking = false
			}
		}
		return cleanupErr
	}
	failBeforeCommit := func(primary error) error {
		return errors.Join(primary, rollbackTransaction(), restoreConnection())
	}
	failAfterCommit := func(primary error) error {
		joined := errors.Join(primary, restoreConnection())
		if joined == nil {
			return nil
		}
		return safety.NewCommittedWrite("backfill rows committed but SQLite finalization failed", joined)
	}

	if dbCapBytes > 0 {
		var lockingMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA locking_mode=EXCLUSIVE").Scan(&lockingMode); err != nil {
			return 0, false, 0, 0, 0, fmt.Errorf("enable SQLite exclusive locking: %w", err)
		}
		if !strings.EqualFold(lockingMode, "exclusive") {
			return 0, false, 0, 0, 0, fmt.Errorf("enable SQLite exclusive locking: got %q", lockingMode)
		}
		exclusiveLocking = true
		// Touch the database in an EXCLUSIVE transaction, then roll it back.
		// With locking_mode=EXCLUSIVE the dedicated connection retains the lock
		// across the following no-transaction PRAGMAs and mode transitions.
		if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("acquire SQLite exclusive lock: %w", err))
		}
		transactionOpen = true
		var schemaRows int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema").Scan(&schemaRows); err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("touch SQLite under exclusive lock: %w", err))
		}
		if err := rollbackTransaction(); err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(err)
		}
		var busy, logFrames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("checkpoint SQLite WAL: %w", err))
		}
		if busy != 0 {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("checkpoint SQLite WAL: database is busy"))
		}
		var mode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("switch SQLite journal mode for capped write: %w", err))
		}
		if !strings.EqualFold(mode, "delete") {
			return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("switch SQLite journal mode for capped write: got %q", mode))
		}
		deleteJournal = true
	}

	begin := "BEGIN IMMEDIATE"
	if dbCapBytes > 0 {
		begin = "BEGIN EXCLUSIVE"
	}
	if _, err := conn.ExecContext(ctx, begin); err != nil {
		return 0, false, 0, 0, 0, failBeforeCommit(fmt.Errorf("begin atomic backfill: %w", err))
	}
	transactionOpen = true
	var current int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND (deleted=0 OR deleted IS NULL)", chatID,
	).Scan(&current); err != nil {
		return 0, false, 0, 0, 0, failBeforeCommit(err)
	}
	if dbCapBytes > 0 {
		size, err := databaseSizeBytesContext(ctx, conn)
		if err != nil {
			return 0, false, 0, 0, 0, failBeforeCommit(err)
		}
		if size >= dbCapBytes {
			capReached = true
		}
	}
	for rowIndex, sourceRow := range rows {
		if capReached {
			capSkippedMedia = countBackfillMediaPaths(rows[rowIndex:])
			break
		}
		row := sourceRow
		if row.ChatID == 0 {
			row.ChatID = chatID
		}
		var alreadyActive int
		if err := conn.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM tg_messages
				WHERE chat_id=? AND message_id=? AND (deleted=0 OR deleted IS NULL)
			)`, row.ChatID, row.MessageID).Scan(&alreadyActive); err != nil {
			return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(err)
		}
		if current >= maxMessages && alreadyActive == 0 {
			if strings.TrimSpace(row.MediaPath) != "" {
				capSkippedMedia++
			}
			continue
		}
		if _, err := conn.ExecContext(ctx, "SAVEPOINT backfill_candidate"); err != nil {
			return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(err)
		}
		if err := insertBackfillMessageContext(ctx, conn, row); err != nil {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK TO backfill_candidate")
			_, releaseErr := conn.ExecContext(context.Background(), "RELEASE backfill_candidate")
			return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(errors.Join(
				err,
				wrapSQLiteCleanupError("rollback SQLite candidate savepoint", rollbackErr),
				wrapSQLiteCleanupError("release SQLite candidate savepoint", releaseErr),
			))
		}
		if dbCapBytes > 0 {
			size, err := databaseSizeBytesContext(ctx, conn)
			if err != nil {
				return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(err)
			}
			if size > dbCapBytes {
				_, rollbackErr := conn.ExecContext(ctx, "ROLLBACK TO backfill_candidate")
				_, releaseErr := conn.ExecContext(ctx, "RELEASE backfill_candidate")
				if rollbackErr != nil || releaseErr != nil {
					return inserted, true, 0, 0, capSkippedMedia, failBeforeCommit(errors.Join(
						wrapSQLiteCleanupError("rollback oversized SQLite candidate", rollbackErr),
						wrapSQLiteCleanupError("release oversized SQLite candidate", releaseErr),
					))
				}
				capReached = true
				capSkippedMedia = countBackfillMediaPaths(rows[rowIndex:])
				break
			}
		}
		if _, err := conn.ExecContext(ctx, "RELEASE backfill_candidate"); err != nil {
			return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(err)
		}
		inserted++
		if alreadyActive == 0 {
			current++
		}
	}
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tg_messages WHERE chat_id=? AND (deleted=0 OR deleted IS NULL)", chatID,
	).Scan(&finalCount); err != nil {
		return inserted, capReached, 0, 0, capSkippedMedia, failBeforeCommit(err)
	}
	if hooks.beforeCommit != nil {
		if err := hooks.beforeCommit(); err != nil {
			return inserted, capReached, 0, finalCount, capSkippedMedia, failBeforeCommit(err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return inserted, capReached, 0, finalCount, capSkippedMedia, failBeforeCommit(fmt.Errorf("commit SQLite backfill transaction: %w", err))
	}
	transactionOpen = false
	committed = true
	var postCommitErr error
	if hooks.afterCommit != nil {
		postCommitErr = hooks.afterCommit()
	}
	if err := failAfterCommit(postCommitErr); err != nil {
		return inserted, capReached, 0, finalCount, capSkippedMedia, err
	}
	finalSize, err = databaseSizeBytesContext(context.Background(), conn)
	if err != nil {
		return inserted, capReached, 0, finalCount, capSkippedMedia, safety.NewCommittedWrite("backfill rows committed but final size accounting failed", err)
	}
	if dbCapBytes > 0 && finalSize >= dbCapBytes {
		capReached = true
	}
	return inserted, capReached, finalSize, finalCount, capSkippedMedia, nil
}

func countBackfillMediaPaths(rows []client.BackfillMessage) int {
	count := 0
	for _, row := range rows {
		if strings.TrimSpace(row.MediaPath) != "" {
			count++
		}
	}
	return count
}

func wrapSQLiteCleanupError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

func discoverCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "discover",
		Short:        "Discover dialogs and cache chat metadata",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			var err error
			limit, err = defaultedInt32Limit(limit, 200, "--limit")
			if err != nil {
				return emitDispatchedFailure(cmd, "discover", err)
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

func capSkippedMediaWarning(count int) string {
	if count == 1 {
		return "media file for 1 message skipped by a backfill cap was preserved"
	}
	return fmt.Sprintf("media files for %d messages skipped by a backfill cap were preserved", count)
}

const maxBackfillRecoveryOutcomes = 20

func recountBackfillMedia(result *client.BackfillResult) {
	if len(result.MediaOutcomes) == 0 {
		if result.Warnings == nil {
			result.Warnings = []string{}
		}
		return
	}
	result.MediaDownloaded, result.MediaSkipped, result.MediaFailed = 0, 0, 0
	for _, outcome := range result.MediaOutcomes {
		switch outcome.Status {
		case client.BackfillMediaDownloaded:
			result.MediaDownloaded++
		case client.BackfillMediaSkipped, client.BackfillMediaUnsupported:
			result.MediaSkipped++
		case client.BackfillMediaFailed, client.BackfillMediaMalformed:
			result.MediaFailed++
		}
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
}

func setBackfillRecoveryExtras(dst map[string]any, result client.BackfillResult, mediaRoot string) {
	committed := make([]client.BackfillMediaOutcome, 0, len(result.MediaOutcomes))
	diagnostic := make([]client.BackfillMediaOutcome, 0, len(result.MediaOutcomes))
	for _, outcome := range result.MediaOutcomes {
		if isCommittedBackfillOutcome(outcome) {
			committed = append(committed, outcome)
		} else {
			diagnostic = append(diagnostic, outcome)
		}
	}
	dst["artifact_count"] = len(committed)
	dst["media_downloaded"] = result.MediaDownloaded
	dst["media_skipped"] = result.MediaSkipped
	dst["media_failed"] = result.MediaFailed
	outcomes := make([]map[string]any, 0, min(len(result.MediaOutcomes), maxBackfillRecoveryOutcomes))
	ordered := append(committed, diagnostic...)
	for _, outcome := range ordered {
		if len(outcomes) >= maxBackfillRecoveryOutcomes {
			break
		}
		item := map[string]any{
			"chat_id": outcome.ChatID, "message_id": outcome.MessageID,
			"status": string(outcome.Status), "media_type": outcome.MediaType,
			"media_id": safeBackfillIdentity(outcome.MediaIdentity), "bytes": outcome.Bytes,
			"error_code": outcome.ErrorCode,
		}
		if backfillRecoveryPathAllowed(outcome.MediaPath, mediaRoot) {
			item["media_path"] = filepath.Clean(outcome.MediaPath)
		}
		outcomes = append(outcomes, item)
	}
	dst["media_outcomes"] = outcomes
	if len(result.MediaOutcomes) > len(outcomes) {
		dst["media_outcomes_truncated"] = true
	}
}

func isCommittedBackfillOutcome(outcome client.BackfillMediaOutcome) bool {
	return outcome.Committed || outcome.Status == client.BackfillMediaDownloaded
}

func backfillCommittedArtifactCount(result client.BackfillResult) int {
	count := 0
	for _, outcome := range result.MediaOutcomes {
		if isCommittedBackfillOutcome(outcome) {
			count++
		}
	}
	return count
}

func backfillRecoveryPathAllowed(path, root string) bool {
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func safeBackfillIdentity(identity string) string {
	if len(identity) > 80 {
		return ""
	}
	for _, r := range identity {
		if !(r == ':' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
			return ""
		}
	}
	return identity
}

func setBackfillAuditResult(args map[string]any, result client.BackfillResult) {
	args["media_downloaded"] = result.MediaDownloaded
	args["media_skipped"] = result.MediaSkipped
	args["media_failed"] = result.MediaFailed
	args["client_warning_count"] = len(result.Warnings)
	args["client_warnings"] = boundedBackfillWarnings(result.Warnings, 20)
}

func boundedBackfillWarnings(warnings []string, limit int) []string {
	if limit < 0 {
		limit = 0
	}
	if len(warnings) < limit {
		limit = len(warnings)
	}
	return append([]string{}, warnings[:limit]...)
}

func wrapBackfillMediaCommit(err error, result client.BackfillResult, mediaRoot, message string) error {
	if err == nil || backfillCommittedArtifactCount(result) == 0 {
		return err
	}
	extras := map[string]any{}
	setBackfillRecoveryExtras(extras, result, mediaRoot)
	return safety.NewCommittedWriteWithExtras(message, errors.New("backfill finalization failed after media commit"), mergedBackfillExtras(err, extras))
}

func mergedBackfillExtras(err error, extras map[string]any) map[string]any {
	merged := map[string]any{}
	var committed *safety.CommittedWrite
	if errors.As(err, &committed) {
		for key, value := range committed.ClassificationExtras() {
			merged[key] = value
		}
	}
	for key, value := range extras {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	return merged
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
			reply_to_msg_id, has_media, media_type, media_path, media_id, raw_json, deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			sender_id=excluded.sender_id, date=excluded.date, text=excluded.text,
			is_outgoing=excluded.is_outgoing, reply_to_msg_id=excluded.reply_to_msg_id,
			has_media=excluded.has_media,
			media_type=CASE
				WHEN ? IN ('downloaded','skipped') THEN excluded.media_type
				WHEN ?='failed' AND excluded.media_id IS NOT NULL AND tg_messages.media_id=excluded.media_id THEN tg_messages.media_type
				WHEN ?='' AND excluded.has_media=1 AND excluded.media_type IS NULL THEN tg_messages.media_type
				ELSE excluded.media_type END,
			media_path=CASE
				WHEN ? IN ('downloaded','skipped') THEN excluded.media_path
				WHEN ?='failed' AND excluded.media_id IS NOT NULL AND tg_messages.media_id=excluded.media_id THEN tg_messages.media_path
				WHEN ?='' AND excluded.has_media=1 AND excluded.media_path IS NULL THEN tg_messages.media_path
				ELSE excluded.media_path END,
			media_id=CASE
				WHEN ? IN ('downloaded','skipped') THEN excluded.media_id
				WHEN ?='failed' AND excluded.media_id IS NOT NULL AND tg_messages.media_id=excluded.media_id THEN tg_messages.media_id
				WHEN ?='' THEN excluded.media_id
				ELSE NULL END,
			raw_json=excluded.raw_json, deleted=0`,
		row.ChatID, row.MessageID, sender, date, nullIfEmpty(row.Text), localDBBoolInt(row.IsOutgoing),
		reply, localDBBoolInt(row.HasMedia), nullIfEmpty(row.MediaType), nullIfEmpty(row.MediaPath), nullIfEmpty(row.MediaIdentity), nullIfEmpty(row.RawJSON),
		row.MediaDisposition, row.MediaDisposition, row.MediaDisposition,
		row.MediaDisposition, row.MediaDisposition, row.MediaDisposition,
		row.MediaDisposition, row.MediaDisposition, row.MediaDisposition)
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
