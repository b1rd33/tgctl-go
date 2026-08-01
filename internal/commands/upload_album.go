package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
)

// uploadAlbumCommand sends a Telegram media group. The command deliberately
// keeps its preview/audit surface metadata-only: source paths, captions, and
// content hashes are used internally for idempotency but are never emitted.
func uploadAlbumCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "upload-album <chat> <file>...",
		Short:        "Upload a 2–10 item photo/video album",
		Args:         cobra.RangeArgs(3, 11),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "upload-album"
			caption, _ := cmd.Flags().GetString("caption")
			replyTo, _ := cmd.Flags().GetInt64("reply-to")
			if err := validateOptionalPositiveInt32(replyTo, "--reply-to"); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			silent, _ := cmd.Flags().GetBool("silent")
			supportsStreaming, _ := cmd.Flags().GetBool("supports-streaming")
			maxSizeMB, _ := cmd.Flags().GetInt64("max-size-mb")
			maxBytes, err := media.MaxBytesFromMiB(maxSizeMB)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}

			// Gate before touching files. This keeps a rejected write from
			// contacting Telegram or creating local state.
			if err := safety.RequireWriteAllowed(writeArgsFrom(cmd).Args); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			dbPath, sessionPath, auditPath, err := accountPathsForMode(cfg.Paths, account, writeArgsFrom(cmd).DryRun)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			paths := resolvedWritePaths{dbPath: dbPath, sessionPath: sessionPath, auditPath: auditPath}

			items, identities, mediaTypes, err := validateAlbumFiles(args[1:], maxBytes, caption, supportsStreaming)

			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			// Resolve once through the read-only cache. Supplying this immutable
			// target to writes.Run avoids a selector/account TOCTOU race and makes
			// the chat id part of the idempotency fingerprint.
			resolved, err := resolveAlbumTarget(cfg.Paths, paths, args[0])
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			fingerprint, err := albumFingerprint(account, resolved.target.ChatID, identities, caption, replyTo, silent, supportsStreaming)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if err := cmd.Flags().Set("idempotency-fingerprint", fingerprint); err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}

			payload := map[string]any{
				"item_count":         len(items),
				"media_types":        mediaTypes,
				"caption_present":    caption != "",
				"reply_to":           replyTo,
				"silent":             silent,
				"supports_streaming": supportsStreaming,
			}
			return runWriteResolvedTarget(cmd, name, "messages.SendMultiMedia", args[0], cfg, paths, payload, &resolved.target,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					resp, err := c.UploadAlbum(ctx, client.UploadAlbumReq{
						ChatID: chatID, Items: items, Caption: caption, ReplyTo: replyTo,
						Silent: silent, SupportsStreaming: supportsStreaming, MaxBytes: maxBytes,
						MaxSizeMB: maxSizeMB,
					})
					if err != nil {
						return nil, err
					}
					if len(resp.MessageIDs) != len(items) {
						return nil, fmt.Errorf("album response returned %d message ids for %d items", len(resp.MessageIDs), len(items))
					}
					rows := make([]store.UploadedMedia, len(items))
					for i, item := range items {
						mediaType := item.Kind
						if i < len(resp.Items) && resp.Items[i].MediaType != "" {
							mediaType = resp.Items[i].MediaType
						}
						rows[i] = store.UploadedMedia{MessageID: resp.MessageIDs[i], Text: item.Caption, MediaType: mediaType, MediaPath: item.Path}
					}
					db, err := store.Connect(paths.dbPath)
					if err != nil {
						return nil, err
					}
					recordErr := store.RecordUploadedAlbum(db, chatID, rows)
					closeErr := db.Close()
					if recordErr != nil {
						return nil, recordErr
					}
					if closeErr != nil {
						return nil, closeErr
					}
					out := map[string]any{
						"chat":        map[string]any{"chat_id": chatID, "title": chatTitle},
						"message_ids": resp.MessageIDs,
						"item_count":  len(resp.MessageIDs),
					}
					if resp.GroupedID != 0 {
						out["grouped_id"] = resp.GroupedID
					}
					return out, nil
				})
		},
	}
	cmd.Flags().String("caption", "", "Album caption (placed on the first item)")
	cmd.Flags().Int64("reply-to", 0, "Reply-to message id")
	cmd.Flags().Bool("silent", false, "Send silently")
	cmd.Flags().Int64("max-size-mb", 100, "Maximum size per item in MiB")
	cmd.Flags().Bool("supports-streaming", false, "Mark video items as streamable")
	cmd.Flags().String("idempotency-fingerprint", "", "internal album request fingerprint")
	_ = cmd.Flags().MarkHidden("idempotency-fingerprint")
	addWriteFlags(cmd)
	return cmd
}

type albumTarget struct{ target writes.ConfirmedTarget }

func resolveAlbumTarget(paths AccountPathProvider, resolved resolvedWritePaths, selector string) (albumTarget, error) {
	db, err := store.ConnectReadonly(resolved.dbPath)
	if err != nil {
		return albumTarget{}, err
	}
	defer db.Close()
	id, title, err := resolveWriteTarget(paths, db, selector)
	if err != nil {
		return albumTarget{}, err
	}
	return albumTarget{target: writes.ConfirmedTarget{ChatID: id, ChatTitle: title}}, nil
}

func validateAlbumFiles(paths []string, maxBytes int64, caption string, streaming bool) ([]client.UploadAlbumItem, []albumFileIdentity, []string, error) {
	items := make([]client.UploadAlbumItem, len(paths))
	identities := make([]albumFileIdentity, len(paths))
	mediaTypes := make([]string, len(paths))
	for i, raw := range paths {
		abs, err := media.SafeUserPath(raw)
		if err != nil {
			return nil, nil, nil, safety.NewBadArgs("album item %d has an invalid path", i)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return nil, nil, nil, safety.NewBadArgs("album item %d is not a regular file", i)
		}
		if info.Size() > maxBytes {
			return nil, nil, nil, safety.NewBadArgs("album item %d exceeds --max-size-mb", i)
		}
		kind, err := media.DetectType(abs)
		if err != nil {
			return nil, nil, nil, safety.NewBadArgs("album item %d cannot be inspected", i)
		}
		switch kind {
		case "photo", "image":
			kind = "photo"
		case "video", "video_note":
			kind = "video"
		default:
			return nil, nil, nil, safety.NewBadArgs("album item %d is not a photo or video", i)
		}
		hash, err := hashFile(abs)
		if err != nil {
			return nil, nil, nil, safety.NewBadArgs("album item %d cannot be fingerprinted", i)
		}
		itemCaption := ""
		if i == 0 {
			itemCaption = caption
		}
		items[i] = client.UploadAlbumItem{Path: abs, Kind: kind, MediaType: kind, Caption: itemCaption, SupportsStreaming: streaming && kind == "video"}
		identities[i] = albumFileIdentity{Path: filepath.Clean(abs), Size: info.Size(), SHA256: hash}
		mediaTypes[i] = kind
	}
	return items, identities, mediaTypes, nil
}

type albumFileIdentity struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func albumFingerprint(account string, chatID int64, files []albumFileIdentity, caption string, replyTo int64, silent, streaming bool) (string, error) {
	value := struct {
		Account string              `json:"account"`
		ChatID  int64               `json:"chat_id"`
		Files   []albumFileIdentity `json:"files"`
		Caption string              `json:"caption"`
		ReplyTo int64               `json:"reply_to"`
		Silent  bool                `json:"silent"`
		Stream  bool                `json:"supports_streaming"`
	}{account, chatID, files, caption, replyTo, silent, streaming}
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
