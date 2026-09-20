package commands

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/spf13/cobra"
)

func registerMediaHashCommands(root *cobra.Command, cfg CommandsConfig) {
	for _, name := range []string{"media-hash", "media-index", "media-find", "media-similar"} {
		c := &cobra.Command{Use: name + " <file>", Short: "Compute a local file's exact SHA-256", Args: cobra.ExactArgs(1)}
		if name == "media-index" {
			c.Use = name + " <chat>"
			c.Short = "Index a bounded page of downloaded cached media by SHA-256"
			c.Flags().Bool("allow-write", false, "Allow local hash index updates")
			c.Flags().Bool("fuzzy", false, "Allow title-based chat selectors")
			c.Flags().Int64("after-id", 0, "Continue after this message ID")
		}
		if name == "media-find" {
			c.Use = name + " <sha256>"
			c.Short = "Find cached Telegram media by exact SHA-256"
		}
		if name == "media-similar" {
			c.Use = name + " <dhash>"
			c.Short = "Find approximate visual matches in the local index"
			c.Flags().Int("distance", 6, "Maximum differing bits (0–64); lower is stricter")
		}
		if name == "media-hash" || name == "media-index" {
			c.Flags().Bool("visual", false, "Also compute JPEG/PNG visual hashes (20 MiB / 24 MP cap)")
		}
		if name != "media-hash" {
			c.Flags().Int("limit", 100, "Maximum records (1–1000)")
		}
		if name == "media-hash" || name == "media-index" {
			c.Flags().Int64("max-size-mb", 100, "Maximum file size in MiB (0 = unlimited)")
		}
		AddOutputFlags(c)
		c.RunE = func(cmd *cobra.Command, args []string) error {
			if name == "media-index" {
				fuzzy, _ := cmd.Flags().GetBool("fuzzy")
				if err := safety.RequireExplicitOrFuzzy(safety.Args{Fuzzy: fuzzy}, args[0]); err != nil {
					return emitDispatchedFailure(cmd, name, err)
				}
				allow, _ := cmd.Flags().GetBool("allow-write")
				if err := safety.RequireWriteAllowed(safety.Args{AllowWrite: allow, ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
					return emitDispatchedFailure(cmd, name, err)
				}
			}
			code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(ctx context.Context) (any, error) {
				account, err := selectedAccount(cmd, cfg.Paths)
				if err != nil {
					return nil, err
				}
				limit := 100
				if name != "media-hash" {
					limit, _ = cmd.Flags().GetInt("limit")
				}
				if limit < 1 || limit > 1000 {
					return nil, safety.NewBadArgs("--limit must be between 1 and 1000")
				}
				mb, _ := cmd.Flags().GetInt64("max-size-mb")
				maxBytes, err := media.MaxBytesFromMiB(mb)
				if err != nil {
					return nil, err
				}
				visual, _ := cmd.Flags().GetBool("visual")
				if name == "media-hash" {
					if visual {
						return media.HashVisualFile(ctx, args[0], maxBytes)
					}
					hash, size, err := media.HashFile(ctx, args[0], maxBytes)
					return map[string]any{"sha256": hash, "bytes": size, "algorithm": "sha256"}, err
				}
				digest := strings.ToLower(args[0])
				distance := 6
				if name == "media-similar" {
					distance, _ = cmd.Flags().GetInt("distance")
					if _, e := store.ParseVisualHash(digest); e != nil {
						return nil, safety.NewBadArgs("%s", e)
					}
					if distance < 0 || distance > 64 {
						return nil, safety.NewBadArgs("--distance must be between 0 and 64")
					}
				}
				if name == "media-find" {
					raw, e := hex.DecodeString(digest)
					if e != nil || len(raw) != 32 {
						return nil, safety.NewBadArgs("SHA-256 must contain exactly 64 hexadecimal characters")
					}
				}
				path, _, _, err := accountPathsForMode(cfg.Paths, account, true)
				if err != nil {
					return nil, err
				}
				db, err := store.ConnectReadonly(path)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				if name == "media-similar" {
					matches, incomplete, truncated, e := store.FindSimilarMedia(ctx, db, digest, distance, limit)
					return map[string]any{"matches": matches, "algorithm": media.VisualAlgorithm, "approximate": true, "scan_incomplete": incomplete, "truncated": truncated, "source": "local_index", "warning": "Visual matches are candidates, not proof of identical content; crops, rotation, EXIF orientation and different colors may produce missed or false matches."}, e
				}
				if name == "media-find" {
					matches, err := store.FindMediaHash(ctx, db, digest, limit+1)
					truncated := len(matches) > limit
					if truncated {
						matches = matches[:limit]
					}
					return map[string]any{"matches": matches, "source": "local_index", "limit": limit, "truncated": truncated}, err
				}
				after, _ := cmd.Flags().GetInt64("after-id")
				if after < 0 {
					return nil, safety.NewBadArgs("--after-id cannot be negative")
				}
				chatID, _, err := resolveWriteTarget(cfg.Paths, db, args[0])
				if err != nil {
					return nil, err
				}
				rows, err := db.QueryContext(ctx, `SELECT message_id,media_path,COALESCE(media_id,'') FROM tg_messages WHERE chat_id=? AND message_id>? AND deleted=0 AND has_media=1 AND COALESCE(media_path,'')!='' ORDER BY message_id LIMIT ?`, chatID, after, limit+1)
				if err != nil {
					return nil, err
				}
				type candidate struct {
					id             int64
					path, identity string
				}
				items := []candidate{}
				for rows.Next() {
					var v candidate
					if err := rows.Scan(&v.id, &v.path, &v.identity); err != nil {
						rows.Close()
						return nil, err
					}
					items = append(items, v)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, err
				}
				more := len(items) > limit
				if more {
					items = items[:limit]
				}
				writable, err := store.Connect(path)
				if err != nil {
					return nil, err
				}
				defer writable.Close()
				if err := store.EnsureMediaHashIndex(writable); err != nil {
					return nil, err
				}
				indexed := 0
				visualIndexed := 0
				failures := []map[string]any{}
				last := after
				for _, item := range items {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					hash, size, e := media.HashFile(ctx, item.path, maxBytes)
					if e != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
						failures = append(failures, map[string]any{"message_id": item.id, "reason": "file_unreadable_changed_or_over_limit"})
						last = item.id
						continue
					}
					if e = store.IndexMediaHash(ctx, writable, chatID, item.id, hash, size, item.path, item.identity); e != nil {
						return nil, e
					}
					indexed++
					if visual {
						v, visualErr := media.HashVisualFile(ctx, item.path, maxBytes)
						if visualErr == nil && v.SHA256 != hash {
							visualErr = fmt.Errorf("file changed between hash operations")
						}
						if visualErr == nil {
							visualErr = store.StoreVisualHash(ctx, writable, hash, v.DHash)
						}
						if visualErr != nil {
							if ctx.Err() != nil {
								return nil, ctx.Err()
							}
							failures = append(failures, map[string]any{"message_id": item.id, "reason": "visual_unsupported_changed_or_over_limit"})
						} else {
							visualIndexed++
						}
					}
					last = item.id
				}
				return map[string]any{"indexed": indexed, "visual_indexed": visualIndexed, "failures": failures, "has_more": more, "next_after_id": last, "source": "downloaded_cache", "telegram_calls": 0}, nil
			})
			storeExitCode(cmd, code)
			return nil
		}
		root.AddCommand(c)
	}
}
