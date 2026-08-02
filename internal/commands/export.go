package commands

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type exportRecord struct {
	ChatID       int64  `json:"chat_id"`
	MessageID    int64  `json:"message_id"`
	SenderID     *int64 `json:"sender_id,omitempty"`
	Date         string `json:"date"`
	IsOutgoing   bool   `json:"is_outgoing"`
	ReplyToMsgID *int64 `json:"reply_to_msg_id,omitempty"`
	Text         string `json:"text,omitempty"`
	GroupedID    int64  `json:"grouped_id,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	MediaPath    string `json:"media_path,omitempty"`
}

func exportCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "export <chat>",
		Short:        "Export cached Telegram history locally",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("format")
			format = strings.ToLower(strings.TrimSpace(format))
			if format != "jsonl" && format != "csv" && format != "html" {
				return emitDispatchedFailure(cmd, "export", safety.NewBadArgs("--format must be jsonl, csv, or html"))
			}
			limit, _ := cmd.Flags().GetInt("limit")
			if limit < 0 {
				return emitDispatchedFailure(cmd, "export", safety.NewBadArgs("--limit must not be negative"))
			}
			outputPath, _ := cmd.Flags().GetString("output")
			if outputPath == "" {
				outputPath = "-"
			}
			includeMedia, _ := cmd.Flags().GetBool("include-media")
			account, err := selectedAccount(cmd, cfg.Paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "export", err)
			}
			paths, err := resolveDownloadMediaPaths(cfg.Paths, account)
			if err != nil {
				return emitDispatchedFailure(cmd, "export", err)
			}
			jsonOutput := jsonMode(cmd)
			var humanFormatter func(any)
			if outputPath == "-" {
				// Raw stdout exports already own the human output stream; avoid
				// appending dispatch's metadata object after the archive bytes.
				humanFormatter = func(any) {}
			}
			code := dispatch.Run("export", dispatch.Options{
				JSON: jsonOutput, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
				HumanFormatter: humanFormatter,
			}, func(ctx context.Context) (any, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				db, err := store.ConnectReadonly(paths.dbPath)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				chatID, title, err := resolve.ResolveChatDB(db, args[0])
				if err != nil {
					return nil, err
				}
				rows, err := store.ExportMessages(db, store.ExportOptions{ChatID: chatID, Since: flagString(cmd, "since"), Until: flagString(cmd, "until"), Limit: limit})
				if err != nil {
					return nil, err
				}
				records := make([]exportRecord, 0, len(rows))
				for _, row := range rows {
					records = append(records, makeExportRecord(row, includeMedia, paths.mediaDir))
				}
				content := ""
				if outputPath == "-" {
					if jsonOutput {
						var buffer bytes.Buffer
						if err := writeExport(&buffer, format, records); err != nil {
							return nil, err
						}
						content = buffer.String()
					} else if err := writeExport(cmd.OutOrStdout(), format, records); err != nil {
						return nil, err
					}
				} else if err := writeExportAtomic(outputPath, format, records); err != nil {
					return nil, err
				}
				result := map[string]any{"chat_id": chatID, "title": title, "format": format, "rows": len(records), "output": outputPath, "include_media": includeMedia}
				if content != "" {
					result["content"] = content
				}
				return result, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().String("format", "jsonl", "Export format: jsonl, csv, or html")
	cmd.Flags().String("output", "-", "Output file, or - for stdout")
	cmd.Flags().String("since", "", "Inclusive lower date/time bound")
	cmd.Flags().String("until", "", "Inclusive upper date/time bound")
	cmd.Flags().Int("limit", 0, "Maximum rows (0 means all cached rows)")
	cmd.Flags().Bool("include-media", false, "Include media paths relative to the account media root")
	AddOutputFlags(cmd)
	return cmd
}

func flagString(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func makeExportRecord(row store.Message, includeMedia bool, mediaRoot string) exportRecord {
	record := exportRecord{ChatID: row.ChatID, MessageID: row.MessageID, SenderID: row.SenderID, Date: row.Date, IsOutgoing: row.IsOutgoing, ReplyToMsgID: row.ReplyToMsgID, GroupedID: row.GroupedID}
	if row.Text != nil {
		record.Text = *row.Text
	}
	if row.MediaType != nil {
		record.MediaType = *row.MediaType
	}
	if includeMedia && row.MediaPath != nil {
		record.MediaPath = relativeMediaPath(mediaRoot, *row.MediaPath)
	}
	return record
}

func relativeMediaPath(root, path string) string {
	if path == "" {
		return ""
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return ""
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func writeExport(w io.Writer, format string, records []exportRecord) error {
	switch format {
	case "jsonl":
		encoder := json.NewEncoder(w)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		writer := csv.NewWriter(w)
		if err := writer.Write([]string{"chat_id", "message_id", "sender_id", "date", "is_outgoing", "reply_to_msg_id", "text", "grouped_id", "media_type", "media_path"}); err != nil {
			return err
		}
		for _, record := range records {
			if err := writer.Write([]string{fmt.Sprint(record.ChatID), fmt.Sprint(record.MessageID), pointerInt64(record.SenderID), record.Date, fmt.Sprint(record.IsOutgoing), pointerInt64(record.ReplyToMsgID), record.Text, fmt.Sprint(record.GroupedID), record.MediaType, record.MediaPath}); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	case "html":
		if _, err := io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><table><thead><tr><th>ID</th><th>Date</th><th>Text</th><th>Grouped ID</th><th>Media</th></tr></thead><tbody>"); err != nil {
			return err
		}
		for _, record := range records {
			if _, err := fmt.Fprintf(w, "<tr><td>%d</td><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>", record.MessageID, html.EscapeString(record.Date), html.EscapeString(record.Text), record.GroupedID, html.EscapeString(record.MediaPath)); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "</tbody></table>\n")
		return err
	default:
		return errors.New("unsupported export format")
	}
}

func pointerInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}

func writeExportAtomic(path, format string, records []exportRecord) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("export output already exists: %s", abs)
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tgctl-export-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := writeExport(tmp, format, records); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("export output appeared during publish: %s", abs)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return err
	}
	published = true
	return nil
}
