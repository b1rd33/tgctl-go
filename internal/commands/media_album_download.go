package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// downloadAlbumItem is intentionally metadata-only. In particular, the
// cache path and Telegram caption are never copied into the CLI response or
// audit record.
type downloadAlbumItem struct {
	Position  int    `json:"position"`
	MessageID int64  `json:"message_id"`
	GroupedID int64  `json:"grouped_id,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Status    string `json:"status"`
	Bytes     int64  `json:"bytes,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type downloadAlbumResult struct {
	ChatID          int64               `json:"chat_id"`
	AnchorMessageID int64               `json:"anchor_message_id,omitempty"`
	GroupedID       int64               `json:"grouped_id,omitempty"`
	ItemCount       int                 `json:"item_count"`
	Downloaded      int                 `json:"downloaded"`
	Skipped         int                 `json:"skipped"`
	Failed          int                 `json:"failed"`
	Partial         bool                `json:"partial"`
	DryRun          bool                `json:"dry_run,omitempty"`
	Items           []downloadAlbumItem `json:"items"`
}

func downloadAlbumCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "download-album <chat> [message-id]",
		Short:        "Download one cached Telegram media group",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			const name = "download-album"
			wargs := writeArgsFrom(cmd)
			if err := safety.RequireWriteAllowed(wargs.Args); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if err := safety.RequireExplicitOrFuzzy(wargs.Args, args[0]); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			messageFlag, _ := cmd.Flags().GetInt64("message-id")
			groupedID, _ := cmd.Flags().GetInt64("grouped-id")
			messageID, err := parseAlbumAnchor(args, messageFlag, groupedID)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if err := validateGroupedIDFlag(groupedID); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			maxSizeMB, _ := cmd.Flags().GetInt64("max-size-mb")
			maxBytes, err := downloadMaxBytes(maxSizeMB)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			outputDir, _ := cmd.Flags().GetString("output")
			if outputDir != "" && strings.TrimSpace(outputDir) == "" {
				return emitDispatchedFailure(cmd, name, safety.NewBadArgs("--output cannot be blank"))
			}
			overwrite, _ := cmd.Flags().GetBool("overwrite")
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			paths, err := resolveDownloadMediaPaths(cfg.Paths, account)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			auditArgs := map[string]any{
				"chat": args[0], "message_id": messageID, "grouped_id": groupedID,
				"max_size_mb": maxSizeMB, "overwrite": overwrite,
				"output_policy": map[bool]string{true: "default", false: "explicit"}[outputDir == ""],
				"dry_run":       wargs.DryRun,
			}
			if groupedID != 0 {
				delete(auditArgs, "message_id")
			}
			auditPath := paths.auditPath
			if wargs.DryRun {
				// Dry-run is metadata-only and must not append an audit record.
				auditPath = ""
			}
			recoveryExtras := map[string]any{}
			code := dispatch.Run(name, dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: auditPath, Args: auditArgs, CommittedExtras: recoveryExtras,
			}, func(ctx context.Context) (any, error) {
				readDB, err := openAlbumDB(paths.dbPath, wargs.DryRun)
				if err != nil {
					return nil, err
				}
				chatID, _, err := resolveWriteTarget(cfg.Paths, readDB, args[0])
				if err != nil {
					_ = readDB.Close()
					return nil, err
				}
				var rows []store.Message
				if groupedID != 0 {
					rows, err = store.ListAlbum(readDB, chatID, groupedID, false)
				} else {
					rows, err = store.GetAlbum(readDB, chatID, messageID, false)
				}
				if closeErr := readDB.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					return nil, err
				}
				if len(rows) == 0 {
					return nil, safety.NewBadArgs("no cached album messages found")
				}
				if groupedID == 0 && rows[0].GroupedID == 0 {
					return nil, safety.NewBadArgs("not-an-album: anchor message is not part of an album")
				}
				result := downloadAlbumResult{ChatID: chatID, AnchorMessageID: messageID, GroupedID: groupedID, ItemCount: len(rows), DryRun: wargs.DryRun, Items: make([]downloadAlbumItem, 0, len(rows))}
				if result.GroupedID == 0 {
					result.GroupedID = rows[0].GroupedID
				}
				for i, row := range rows {
					item := downloadAlbumItem{Position: i, MessageID: row.MessageID, GroupedID: row.GroupedID}
					if row.MediaType != nil {
						item.MediaType = *row.MediaType
					}
					if !row.HasMedia {
						item.Status = "missing_media"
						result.Skipped++
						result.Partial = true
						result.Items = append(result.Items, item)
						continue
					}
					if wargs.DryRun {
						item.Status = "planned"
						result.Items = append(result.Items, item)
						continue
					}
					result.Items = append(result.Items, item)
				}
				if wargs.DryRun {
					return result, nil
				}
				if cfg.ClientFactory == nil {
					return nil, fmt.Errorf("download-album client factory is not configured")
				}
				requestedOutput := outputDir
				if requestedOutput == "" {
					requestedOutput = filepath.Join(paths.mediaDir, strconv.FormatInt(chatID, 10))
				}
				validationRoot, err := filepath.Abs(filepath.Clean(requestedOutput))
				if err != nil {
					return nil, fmt.Errorf("resolve output directory: %w", err)
				}
				pending := false
				for i, row := range rows {
					if overwrite {
						if row.HasMedia {
							pending = true
						}
						continue
					}
					if !row.HasMedia {
						continue
					}
					if row.MediaPath == nil || strings.TrimSpace(*row.MediaPath) == "" || row.MediaIdentity == nil {
						pending = true
						continue
					}
					artifact, cacheErr := media.InspectDownloadedArtifact(validationRoot, *row.MediaPath)
					if cacheErr != nil {
						pending = true
						continue
					}
					result.Items[i].Status = "cached"
					result.Items[i].Bytes = artifact.Size
					result.Items[i].Skipped = true
					result.Skipped++
				}
				if !pending {
					return result, nil
				}
				telegramClient, err := cfg.ClientFactory(ctx, paths.sessionPath, paths.dbPath)
				if err != nil {
					return nil, err
				}
				cacheDB, err := store.Connect(paths.dbPath)
				if err != nil {
					_ = telegramClient.Close()
					return nil, err
				}
				defer cacheDB.Close()
				canceled := false
				cacheFinalizationFailed := false
				for i, row := range rows {
					if !row.HasMedia || result.Items[i].Status == "cached" {
						continue
					}
					resp, downloadErr := telegramClient.DownloadMedia(ctx, client.DownloadMediaReq{
						ChatID: chatID, MessageID: row.MessageID, OutputDir: requestedOutput,
						MaxBytes: maxBytes, Overwrite: overwrite,
					})
					if downloadErr != nil {
						var committed *client.CommittedMediaDownloadError
						if errors.As(downloadErr, &committed) {
							if artifact, validationErr := validateDownloadMediaResponse(committed.Response, chatID, row.MessageID, validationRoot, overwrite); validationErr == nil {
								persistErr := store.StoreMessageMediaPath(cacheDB, chatID, row.MessageID, rowDate(row), committed.Response.MediaType, artifact.Path)
								if persistErr != nil {
									cacheFinalizationFailed = true
								}
								result.Items[i].Status = "committed"
								result.Items[i].Bytes = artifact.Size
								if persistErr != nil {
									result.Items[i].ErrorCode = "CACHE_FINALIZATION"
									result.Failed++
								}
								result.Partial = true
								continue
							}
						}
						result.Items[i].Status = "failed"
						result.Items[i].ErrorCode = albumDownloadErrorCode(downloadErr)
						result.Failed++
						result.Partial = true
						if errors.Is(downloadErr, context.Canceled) || errors.Is(downloadErr, context.DeadlineExceeded) {
							canceled = true
							break
						}
						continue
					}
					artifact, validationErr := validateDownloadMediaResponse(resp, chatID, row.MessageID, validationRoot, overwrite)
					if validationErr != nil {
						result.Items[i].Status = "failed"
						result.Items[i].ErrorCode = "INVALID_RESPONSE"
						result.Failed++
						result.Partial = true
						continue
					}
					if persistErr := store.StoreMessageMediaPath(cacheDB, chatID, row.MessageID, rowDate(row), resp.MediaType, artifact.Path); persistErr != nil {
						cacheFinalizationFailed = true
						result.Items[i].Status = "committed"
						result.Items[i].ErrorCode = "CACHE_FINALIZATION"
						result.Failed++
						result.Partial = true
						continue
					}
					result.Items[i].Status = "downloaded"
					result.Items[i].Bytes = artifact.Size
					result.Items[i].Skipped = resp.Skipped
					if resp.Skipped {
						result.Items[i].Status = "skipped"
						result.Skipped++
					} else {
						result.Downloaded++
					}
				}
				if closeErr := telegramClient.Close(); closeErr != nil {
					result.Partial = true
				}
				if canceled {
					return nil, context.Canceled
				}
				if cacheFinalizationFailed {
					recoveryExtras["chat_id"] = chatID
					recoveryExtras["item_count"] = result.ItemCount
					recoveryExtras["failed"] = result.Failed
					return nil, safety.NewCommittedWriteWithExtras("album media committed but local cache finalization failed; do not retry blindly", errors.New("local cache finalization failed"), recoveryExtras)
				}
				return result, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int64("message-id", 0, "Anchor message id (or pass it as the second argument)")
	cmd.Flags().Int64("grouped-id", 0, "Telegram grouped_id to download")
	cmd.Flags().Int64("max-size-mb", 100, "Maximum download size per item in MiB (0 = unlimited)")
	cmd.Flags().String("output", "", "Output directory (default account media/<chat-id>/)")
	cmd.Flags().Bool("overwrite", false, "Overwrite existing media files")
	addDownloadAlbumWriteFlags(cmd)
	return cmd
}

func addDownloadAlbumWriteFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("allow-write", false, "Required for album media/cache writes")
	cmd.Flags().Bool("dry-run", false, "Print album metadata without contacting Telegram")
	cmd.Flags().Bool("fuzzy", false, "Allow title-based chat selectors")
	AddOutputFlags(cmd)
}

func openAlbumDB(path string, dryRun bool) (*sql.DB, error) {
	if dryRun {
		return store.ConnectReadonly(path)
	}
	return store.Connect(path)
}

func parseAlbumAnchor(args []string, messageFlag, groupedID int64) (int64, error) {
	if messageFlag < 0 {
		return 0, safety.NewBadArgs("--message-id must be positive")
	}
	if groupedID < 0 {
		return 0, safety.NewBadArgs("--grouped-id must be positive")
	}
	if len(args) == 2 && messageFlag != 0 {
		return 0, safety.NewBadArgs("message id must be supplied either positionally or with --message-id")
	}
	if len(args) == 2 {
		id, err := parseDownloadMessageID(args[1])
		if err != nil {
			return 0, err
		}
		if groupedID != 0 {
			return 0, safety.NewBadArgs("choose an anchor message or grouped_id, not both")
		}
		return id, nil
	}
	if messageFlag != 0 && groupedID != 0 {
		return 0, safety.NewBadArgs("choose an anchor message or grouped_id, not both")
	}
	if messageFlag != 0 {
		if messageFlag > 2147483647 {
			return 0, safety.NewBadArgs("message-id must be an integer between 1 and 2147483647")
		}
		return messageFlag, nil
	}
	if groupedID != 0 {
		return 0, nil
	}
	return 0, safety.NewBadArgs("provide an anchor message id or --grouped-id")
}

func validateGroupedIDFlag(groupedID int64) error {
	if groupedID < 0 {
		return safety.NewBadArgs("--grouped-id must be positive")
	}
	return nil
}

func rowDate(row store.Message) time.Time {
	if parsed, err := time.Parse(time.RFC3339, row.Date); err == nil {
		return parsed
	}
	return time.Time{}
}

func albumDownloadErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "CANCELED"
	case errors.Is(err, media.ErrLimitExceeded):
		return "LIMIT"
	default:
		return "TRANSFER"
	}
}
