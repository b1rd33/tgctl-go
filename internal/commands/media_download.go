package commands

import (
	"context"
	"errors"
	"fmt"
	"math"
	"mime"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

const bytesPerMiB int64 = 1024 * 1024

type downloadMediaPaths struct {
	dbPath, sessionPath, auditPath, mediaDir string
}

type accountPathsSnapshotProvider interface {
	Paths(account string) (accounts.Paths, error)
}

func downloadMediaCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "download-media <chat> <message-id>",
		Short:        "Download media attached to a message",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "download-media"
			allowWrite, _ := cmd.Flags().GetBool("allow-write")
			fuzzy, _ := cmd.Flags().GetBool("fuzzy")
			rootCfg := RootConfigFrom(cmd.Root())
			gateArgs := safety.Args{ReadOnly: rootCfg.ReadOnly, AllowWrite: allowWrite, Fuzzy: fuzzy}
			if err := safety.RequireWriteAllowed(gateArgs); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if err := safety.RequireExplicitOrFuzzy(gateArgs, args[0]); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}

			messageID, err := parseDownloadMessageID(args[1])
			if err != nil {
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

			outputPolicy := "explicit"
			if outputDir == "" {
				outputPolicy = "default"
			}
			auditArgs := map[string]any{
				"chat":          args[0],
				"message_id":    messageID,
				"max_size_mb":   maxSizeMB,
				"output":        outputDir,
				"output_policy": outputPolicy,
				"overwrite":     overwrite,
			}
			recoveryExtras := map[string]any{}
			code := dispatch.Run(name, dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				AuditPath: paths.auditPath, Args: auditArgs, DurableAudit: true,
				CommittedExtras: recoveryExtras,
			}, func(ctx context.Context) (any, error) {
				db, err := store.Connect(paths.dbPath)
				if err != nil {
					return nil, err
				}
				chatID, _, resolveErr := resolveWriteTarget(cfg.Paths, db, args[0])
				resolveCloseErr := db.Close()
				if resolveErr != nil || resolveCloseErr != nil {
					return nil, errors.Join(resolveErr, resolveCloseErr)
				}
				auditArgs["resolved_chat_id"] = chatID
				requestedOutput := outputDir
				if requestedOutput == "" {
					requestedOutput = filepath.Join(paths.mediaDir, strconv.FormatInt(chatID, 10))
				}
				validationRoot, err := filepath.Abs(filepath.Clean(requestedOutput))
				if err != nil {
					return nil, fmt.Errorf("resolve output directory: %w", err)
				}

				if cfg.ClientFactory == nil {
					return nil, fmt.Errorf("download-media client factory is not configured")
				}
				telegramClient, err := cfg.ClientFactory(ctx, paths.sessionPath, paths.dbPath)
				if err != nil {
					return nil, err
				}
				resp, downloadErr := telegramClient.DownloadMedia(ctx, client.DownloadMediaReq{
					ChatID: chatID, MessageID: messageID, OutputDir: requestedOutput,
					MaxBytes: maxBytes, Overwrite: overwrite,
				})
				clientCloseErr := telegramClient.Close()
				if downloadErr != nil {
					return nil, errors.Join(downloadErr, clientCloseErr)
				}
				artifact, validationErr := validateDownloadMediaResponse(resp, chatID, messageID, validationRoot, overwrite)
				if validationErr != nil {
					validationErr = errors.Join(validationErr, clientCloseErr)
					if !resp.Skipped {
						return nil, safety.NewCommittedWrite("media download may have committed but returned an invalid response; do not retry blindly", validationErr)
					}
					return nil, validationErr
				}
				artifactMetadata := downloadArtifactMetadata(resp, artifact)
				for key, value := range artifactMetadata {
					auditArgs[key] = value
					recoveryExtras[key] = value
				}
				if clientCloseErr != nil {
					if !resp.Skipped {
						return nil, safety.NewCommittedWriteWithExtras("media download committed but client finalization failed; do not retry blindly", clientCloseErr, recoveryExtras)
					}
					return nil, fmt.Errorf("finalize skipped media client: %w", clientCloseErr)
				}

				persistDB, persistOpenErr := store.Connect(paths.dbPath)
				if persistOpenErr != nil {
					if !resp.Skipped {
						return nil, safety.NewCommittedWriteWithExtras("media download committed but cache open failed; do not retry blindly", persistOpenErr, recoveryExtras)
					}
					return nil, persistOpenErr
				}
				persistErr := store.StoreMessageMediaPath(persistDB, chatID, messageID, resp.MessageDate, resp.MediaType, artifact.Path)
				persistCloseErr := persistDB.Close()
				if persistErr != nil {
					joined := errors.Join(persistErr, persistCloseErr)
					if !resp.Skipped {
						return nil, safety.NewCommittedWriteWithExtras("media download committed but cache persistence failed; do not retry blindly", joined, recoveryExtras)
					}
					return nil, fmt.Errorf("persist skipped media cache entry: %w", joined)
				}
				if persistCloseErr != nil {
					return nil, safety.NewCommittedWriteWithExtras("media artifact and cache committed but database finalization failed; do not retry blindly", persistCloseErr, recoveryExtras)
				}
				return resp, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Bool("allow-write", false, "Required because the download updates the local media cache")
	cmd.Flags().Bool("fuzzy", false, "Allow title-based chat selectors")
	cmd.Flags().Int64("max-size-mb", 100, "Maximum download size in MiB (0 = unlimited)")
	cmd.Flags().String("output", "", "Output directory (default account media/<chat-id>/)")
	cmd.Flags().Bool("overwrite", false, "Overwrite an existing media file")
	AddOutputFlags(cmd)
	return cmd
}

func downloadArtifactMetadata(resp client.DownloadMediaResp, artifact media.DownloadedArtifact) map[string]any {
	return map[string]any{
		"artifact_path": artifact.Path, "artifact_bytes": artifact.Size,
		"media_type": resp.MediaType, "mime_type": resp.MIMEType,
		"filename": artifact.Filename, "skipped": resp.Skipped,
	}
}

func resolveDownloadMediaPaths(paths AccountPathProvider, account string) (downloadMediaPaths, error) {
	if snapshotter, ok := paths.(accountPathsSnapshotProvider); ok {
		resolved, err := snapshotter.Paths(account)
		if err != nil {
			return downloadMediaPaths{}, err
		}
		return downloadMediaPaths{
			dbPath: resolved.DBPath, sessionPath: resolved.SessionPath,
			auditPath: resolved.AuditPath, mediaDir: resolved.MediaDir,
		}, nil
	}
	dbPath, sessionPath, auditPath, err := paths.AccountPaths(account)
	if err != nil {
		return downloadMediaPaths{}, err
	}
	return downloadMediaPaths{
		dbPath: dbPath, sessionPath: sessionPath, auditPath: auditPath,
		mediaDir: filepath.Join(filepath.Dir(dbPath), "media"),
	}, nil
}

func parseDownloadMessageID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 || id > math.MaxInt32 {
		return 0, safety.NewBadArgs("message-id must be an integer between 1 and %d (got %q)", math.MaxInt32, raw)
	}
	return id, nil
}

func downloadMaxBytes(maxSizeMB int64) (int64, error) {
	if maxSizeMB < 0 {
		return 0, safety.NewBadArgs("--max-size-mb cannot be negative")
	}
	if maxSizeMB > math.MaxInt64/bytesPerMiB {
		return 0, safety.NewBadArgs("--max-size-mb is too large")
	}
	return maxSizeMB * bytesPerMiB, nil
}

func validateDownloadMediaResponse(resp client.DownloadMediaResp, chatID, messageID int64, outputRoot string, overwrite bool) (media.DownloadedArtifact, error) {
	if resp.ChatID != chatID || resp.MessageID != messageID {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned mismatched identity: got chat_id=%d message_id=%d, want chat_id=%d message_id=%d", resp.ChatID, resp.MessageID, chatID, messageID)
	}
	allowedMediaTypes := map[string]bool{
		"photo": true, "video": true, "video_note": true, "voice": true,
		"audio": true, "sticker": true, "animation": true, "document": true,
	}
	if !allowedMediaTypes[resp.MediaType] {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned an invalid media type")
	}
	parsedMIME, _, err := mime.ParseMediaType(strings.TrimSpace(resp.MIMEType))
	if err != nil || parsedMIME == "" {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned an invalid MIME type")
	}
	if strings.TrimSpace(resp.Filename) == "" || resp.Filename != filepath.Base(resp.Filename) || media.SanitizeDownloadName(resp.Filename) != resp.Filename {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned an unsafe filename")
	}
	if resp.Bytes < 0 {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned a negative byte count")
	}
	if resp.Skipped && overwrite {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned skipped=true for an overwrite request")
	}
	if strings.TrimSpace(resp.Path) == "" || !filepath.IsAbs(resp.Path) {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned a non-absolute media path")
	}
	cleanPath := filepath.Clean(resp.Path)
	if filepath.Base(cleanPath) != resp.Filename {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned filename/path mismatch")
	}
	artifact, err := media.InspectDownloadedArtifact(outputRoot, cleanPath)
	if err != nil {
		return media.DownloadedArtifact{}, fmt.Errorf("validate downloaded media artifact: %w", err)
	}
	if artifact.Filename != resp.Filename || artifact.Size != resp.Bytes {
		return media.DownloadedArtifact{}, fmt.Errorf("download-media client returned artifact metadata inconsistent with the filesystem")
	}
	return artifact, nil
}
