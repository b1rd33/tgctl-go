package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerMediaCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(uploadCommand(cfg, "upload-photo", "photo", "Upload a photo"))
	root.AddCommand(uploadCommand(cfg, "upload-voice", "voice", "Upload an OGG/Opus voice message"))
	root.AddCommand(uploadCommand(cfg, "upload-video", "video", "Upload a video"))
	root.AddCommand(uploadCommand(cfg, "upload-document", "document", "Upload a document"))
}

func uploadCommand(cfg CommandsConfig, name, kind, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          name + " <chat> <file>",
		Short:        short,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			caption, _ := cmd.Flags().GetString("caption")
			replyTo, _ := cmd.Flags().GetInt64("reply-to")
			silent, _ := cmd.Flags().GetBool("silent")
			filename, _ := cmd.Flags().GetString("filename")
			maxSize, _ := cmd.Flags().GetInt64("max-size-mb")
			supportsStreaming, _ := cmd.Flags().GetBool("supports-streaming")

			path, err := media.ValidateExpected(args[1], kind, maxSize)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			mediaType, err := media.DetectType(path)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if kind != "document" {
				mediaType = kind
			}
			payload := map[string]any{
				"file_path":          path,
				"media_type":         mediaType,
				"caption":            caption,
				"reply_to":           replyTo,
				"silent":             silent,
				"filename":           filename,
				"supports_streaming": supportsStreaming,
			}
			resolvedPaths, pathErr := resolveWritePathSet(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, name, pathErr)
			}
			return runWriteResolved(cmd, name, "messages.SendMedia", selector, cfg, resolvedPaths, payload,
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					resp, err := c.UploadFile(ctx, client.UploadFileReq{
						ChatID: chatID, Path: path, Kind: kind, Caption: caption,
						ReplyTo: replyTo, Silent: silent, Filename: filename,
						SupportsStreaming: supportsStreaming,
					})
					if err != nil {
						return nil, err
					}
					if db, err := store.Connect(resolvedPaths.dbPath); err == nil {
						_ = store.RecordUploadedMedia(db, chatID, resp.MessageID, caption, mediaType, path)
						_ = db.Close()
					}
					return map[string]any{
						"chat":       map[string]any{"chat_id": chatID, "title": chatTitle},
						"message_id": resp.MessageID,
						"media_type": mediaType,
						"media_path": path,
						"caption":    caption,
					}, nil
				})
		},
	}
	cmd.Flags().String("caption", "", "Media caption")
	cmd.Flags().Int64("reply-to", 0, "Reply-to message id")
	cmd.Flags().Bool("silent", false, "Send silently")
	cmd.Flags().Int64("max-size-mb", 100, "Maximum upload size in MiB")
	if kind == "document" {
		cmd.Flags().String("filename", "", "Override uploaded filename")
	} else {
		cmd.Flags().String("filename", "", "Override uploaded filename")
		_ = cmd.Flags().MarkHidden("filename")
	}
	if kind == "video" {
		cmd.Flags().Bool("supports-streaming", false, "Mark video as streamable")
	} else {
		cmd.Flags().Bool("supports-streaming", false, "Mark video as streamable")
		_ = cmd.Flags().MarkHidden("supports-streaming")
	}
	addWriteFlags(cmd)
	return cmd
}
