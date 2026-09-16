package commands

import (
	"context"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/audit"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/idempotency"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
)

const maxFolderTitleRunes = 12

func registerFolderCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(foldersListCommand(cfg))
	root.AddCommand(folderShowCommand(cfg))
	root.AddCommand(folderCreateCommand(cfg))
	root.AddCommand(folderEditCommand(cfg))
	root.AddCommand(folderDeleteCommand(cfg))
	root.AddCommand(folderMembershipCommand(cfg, true))
	root.AddCommand(folderMembershipCommand(cfg, false))
	root.AddCommand(foldersReorderCommand(cfg))
	root.AddCommand(peerFolderCommand(cfg, true))
	root.AddCommand(peerFolderCommand(cfg, false))
	root.AddCommand(chatPinnedListCommand(cfg))
}

func peerFolderCommand(cfg CommandsConfig, archive bool) *cobra.Command {
	name := "unarchive"
	folderID := 0
	if archive {
		name = "archive"
		folderID = 1
	}
	cmd := &cobra.Command{
		Use:          name + " <chat>",
		Short:        map[bool]string{true: "Archive one chat", false: "Move one chat to the inbox"}[archive],
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := args[0]
			paths, target, account, err := prepareResolvedPeerWriteTarget(cmd, cfg.Paths, selector)
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			payload := map[string]any{
				"account":   account,
				"chat_id":   target.ChatID,
				"folder_id": folderID,
				"archived":  archive,
			}
			return runWriteResolvedTargetDurable(cmd, name, "folders.EditPeerFolders", selector, cfg, paths, payload, &target,
				map[string]any{"chat_id": target.ChatID, "folder_id": folderID, "archived": archive},
				func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
					if err := c.SetPeerFolder(ctx, client.PeerFolderReq{ChatID: chatID, FolderID: folderID}); err != nil {
						return nil, err
					}
					return map[string]any{
						"chat":          ChatRef{ChatID: chatID, Title: chatTitle},
						"folder_id":     folderID,
						"archived":      archive,
						"cache_refresh": "required",
					}, nil
				})
		},
	}
	cmd.Flags().Bool("allow-write", false, "Required for any Telegram-side write")
	cmd.Flags().Bool("dry-run", false, "Print payload preview without contacting Telegram")
	cmd.Flags().Bool("fuzzy", false, "Allow title-based selectors for write commands")
	cmd.Flags().String("idempotency-key", "", "Per-account replay-safe key")
	AddOutputFlags(cmd)
	return cmd
}

func prepareResolvedPeerWriteTarget(cmd *cobra.Command, paths AccountPathProvider, selector string) (resolvedWritePaths, writes.ConfirmedTarget, string, error) {
	if err := safety.RequireWriteAllowed(writeArgsFrom(cmd).Args); err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	if err := safety.RequireExplicitOrFuzzy(writeArgsFrom(cmd).Args, selector); err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	account, err := selectedAccount(cmd, paths)
	if err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	dbPath, sessionPath, auditPath, err := accountPathsForMode(paths, account, true)
	if err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	resolved := resolvedWritePaths{dbPath: dbPath, sessionPath: sessionPath, auditPath: auditPath}
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	defer db.Close()
	chatID, title, err := resolveWriteTarget(paths, db, selector)
	if err != nil {
		return resolvedWritePaths{}, writes.ConfirmedTarget{}, "", err
	}
	return resolved, writes.ConfirmedTarget{
		ChatID:            chatID,
		ChatTitle:         title,
		ConfirmationSlot:  "chat_id",
		ConfirmationValue: strconv.FormatInt(chatID, 10),
	}, account, nil
}

func foldersListCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folders-list",
		Short:        "List dialog folders",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, _ := cmd.Flags().GetString("query")
			return runFolderRead(cmd, cfg, "folders-list", func(ctx context.Context, c client.Client) (any, error) {
				folders, err := c.ListFolders(ctx)
				if err != nil {
					return nil, err
				}
				if query != "" {
					filtered := folders[:0]
					for _, f := range folders {
						if strings.Contains(strings.ToLower(f.Title), strings.ToLower(query)) {
							filtered = append(filtered, f)
						}
					}
					folders = filtered
				}
				return map[string]any{"folders": folderSummaries(folders)}, nil
			})
		},
	}
	cmd.Flags().String("query", "", "Filter by title")
	AddOutputFlags(cmd)
	return cmd
}

func folderShowCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folder-show <id>",
		Short:        "Show one dialog folder",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseNonNegativeDecimal(args[0], "folder id")
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-show", err)
			}
			return runFolderRead(cmd, cfg, "folder-show", func(ctx context.Context, c client.Client) (any, error) {
				folders, err := c.ListFolders(ctx)
				if err != nil {
					return nil, err
				}
				for _, f := range folders {
					if f.ID == id {
						return map[string]any{"folder": folderSummary(f)}, nil
					}
				}
				return nil, safety.NewBadArgs("folder id %d not found", id)
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func folderCreateCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folder-create <name>",
		Short:        "Create a dialog folder",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			include, _ := cmd.Flags().GetString("include-chats")
			exclude, _ := cmd.Flags().GetString("exclude-chats")
			emoji, _ := cmd.Flags().GetString("emoji")
			title := strings.TrimSpace(args[0])
			if title == "" {
				return emitDispatchedFailure(cmd, "folder-create", safety.NewBadArgs("folder title cannot be empty"))
			}
			if len([]rune(title)) > maxFolderTitleRunes {
				return emitDispatchedFailure(cmd, "folder-create", safety.NewBadArgs(
					"folder title %q is %d chars; Telegram caps DialogFilter titles at %d",
					title, len([]rune(title)), maxFolderTitleRunes,
				))
			}
			inc, err := optionalIntCSV(include)
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-create", err)
			}
			exc, err := optionalIntCSV(exclude)
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-create", err)
			}
			payload := map[string]any{"title": title, "include_chats": inc, "exclude_chats": exc, "emoji": emoji}
			return runFolderWrite(cmd, cfg, "folder-create", "messages.UpdateDialogFilter", payload, func(ctx context.Context, c client.Client) (map[string]any, error) {
				existing, err := c.ListFolders(ctx)
				if err != nil {
					return nil, err
				}
				id := nextFolderID(existing)
				req := client.FolderUpdateReq{ID: id, Title: title, Emoji: emoji, IncludeChatIDs: inc, ExcludeChatIDs: exc}
				if err := c.UpdateFolder(ctx, req); err != nil {
					return nil, err
				}
				return map[string]any{"folder_id": id, "title": title, "include_peer_count": len(inc), "exclude_peer_count": len(exc)}, nil
			})
		},
	}
	cmd.Flags().String("include-chats", "", "Comma-separated chat ids to include")
	cmd.Flags().String("exclude-chats", "", "Comma-separated chat ids to exclude")
	cmd.Flags().String("emoji", "", "Folder emoji")
	addWriteFlags(cmd)
	return cmd
}

func folderEditCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folder-edit <id>",
		Short:        "Edit a dialog folder",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseNonNegativeDecimal(args[0], "folder id")
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-edit", err)
			}
			if id == 0 {
				return emitDispatchedFailure(cmd, "folder-edit", safety.NewBadArgs("folder id 0 is reserved and cannot be edited"))
			}
			title, _ := cmd.Flags().GetString("name")
			emoji, _ := cmd.Flags().GetString("emoji")
			add, _ := cmd.Flags().GetString("add")
			remove, _ := cmd.Flags().GetString("remove")
			if title == "" && emoji == "" && add == "" && remove == "" {
				return emitDispatchedFailure(cmd, "folder-edit", safety.NewBadArgs("nothing to edit"))
			}
			if title != "" && len([]rune(title)) > maxFolderTitleRunes {
				return emitDispatchedFailure(cmd, "folder-edit", safety.NewBadArgs(
					"folder title %q is %d chars; Telegram caps DialogFilter titles at %d",
					title, len([]rune(title)), maxFolderTitleRunes,
				))
			}
			addIDs, err := optionalIntCSV(add)
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-edit", err)
			}
			return runFolderWrite(cmd, cfg, "folder-edit", "messages.UpdateDialogFilter", map[string]any{"folder_id": id}, func(ctx context.Context, c client.Client) (map[string]any, error) {
				req := client.FolderUpdateReq{ID: id, Title: title, Emoji: emoji, IncludeChatIDs: addIDs}
				if err := c.UpdateFolder(ctx, req); err != nil {
					return nil, err
				}
				return map[string]any{"folder_id": id, "edited": true}, nil
			})
		},
	}
	cmd.Flags().String("name", "", "New folder name")
	cmd.Flags().String("add", "", "Comma-separated chat ids to add")
	cmd.Flags().String("remove", "", "Comma-separated chat ids to remove")
	cmd.Flags().String("emoji", "", "Folder emoji")
	addWriteFlags(cmd)
	return cmd
}

func folderDeleteCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folder-delete <id>",
		Short:        "Delete a dialog folder",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseNonNegativeDecimal(args[0], "folder id")
			if err != nil {
				return emitDispatchedFailure(cmd, "folder-delete", err)
			}
			if id == 0 {
				return emitDispatchedFailure(cmd, "folder-delete", safety.NewBadArgs("folder id 0 is reserved and cannot be deleted"))
			}
			if err := requireTypedWriteConfirm(cmd, id, "folder_id"); err != nil {
				return emitDispatchedFailure(cmd, "folder-delete", err)
			}
			return runFolderWrite(cmd, cfg, "folder-delete", "messages.UpdateDialogFilter", map[string]any{"folder_id": id}, func(ctx context.Context, c client.Client) (map[string]any, error) {
				if err := c.DeleteFolder(ctx, id); err != nil {
					return nil, err
				}
				return map[string]any{"folder_id": id, "deleted": true}, nil
			})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func folderMembershipCommand(cfg CommandsConfig, add bool) *cobra.Command {
	name := "folder-add-chat"
	if !add {
		name = "folder-remove-chat"
	}
	cmd := &cobra.Command{
		Use:          name + " <id> <chat>",
		Short:        "Mutate folder chat membership",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseNonNegativeDecimal(args[0], "folder id")
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			chatID, err := parseSignedInt64(args[1], "chat id")
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if chatID == 0 {
				return emitDispatchedFailure(cmd, name, safety.NewBadArgs("chat id must not be zero"))
			}
			if id == 0 {
				return emitDispatchedFailure(cmd, name, safety.NewBadArgs("folder id 0 is reserved and cannot be edited"))
			}
			return runFolderWrite(cmd, cfg, name, "messages.UpdateDialogFilter", map[string]any{"folder_id": id, "chat_id": chatID}, func(ctx context.Context, c client.Client) (map[string]any, error) {
				req := client.FolderUpdateReq{ID: id}
				if add {
					req.IncludeChatIDs = []int64{chatID}
				} else {
					req.ExcludeChatIDs = []int64{chatID}
				}
				if err := c.UpdateFolder(ctx, req); err != nil {
					return nil, err
				}
				return map[string]any{"folder_id": id, "chat_id": chatID, "added": add}, nil
			})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func foldersReorderCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "folders-reorder <id-csv>",
		Short:        "Reorder dialog folders",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parsePositiveInt32CSV(args[0], "folder-id")
			if err != nil {
				return emitDispatchedFailure(cmd, "folders-reorder", err)
			}
			return runFolderWrite(cmd, cfg, "folders-reorder", "messages.UpdateDialogFiltersOrder", map[string]any{"folder_ids": ids}, func(ctx context.Context, c client.Client) (map[string]any, error) {
				if err := c.ReorderFolders(ctx, ids); err != nil {
					return nil, err
				}
				return map[string]any{"folder_ids": ids, "reordered": true}, nil
			})
		},
	}
	addWriteFlags(cmd)
	return cmd
}

func chatPinnedListCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "chat-pinned-list <chat>",
		Short:        "List up to 100 pinned messages in a chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, pathErr := resolvePaths(cmd, cfg.Paths)
			if pathErr != nil {
				return emitDispatchedFailure(cmd, "chat-pinned-list", pathErr)
			}
			code := dispatch.Run("chat-pinned-list", dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: paths.audit}, func(ctx context.Context) (any, error) {
				db, err := connectReadDB(paths)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				chatID, _, err := resolve.ResolveChatDB(db, args[0])
				if err != nil {
					return nil, err
				}
				c, err := openReadClient(ctx, cfg, paths)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				dialogs, err := c.ListPinnedMessages(ctx, chatID)
				if err != nil {
					return nil, err
				}
				return map[string]any{"pinned": dialogs, "limit": 100, "source": "telegram", "may_have_more": len(dialogs) == 100}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func runFolderRead(cmd *cobra.Command, cfg CommandsConfig, name string, runner func(context.Context, client.Client) (any, error)) error {
	paths, pathErr := resolvePaths(cmd, cfg.Paths)
	if pathErr != nil {
		return emitDispatchedFailure(cmd, name, pathErr)
	}
	code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: paths.audit}, func(ctx context.Context) (any, error) {
		c, err := openReadClient(ctx, cfg, paths)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		return runner(ctx, c)
	})
	storeExitCode(cmd, code)
	return nil
}

func runFolderWrite(cmd *cobra.Command, cfg CommandsConfig, name, method string, payload map[string]any, runner func(context.Context, client.Client) (map[string]any, error)) error {
	dbPath, sessionPath, auditPath, pathErr := resolveWritePaths(cmd, cfg.Paths)
	if pathErr != nil {
		return emitDispatchedFailure(cmd, name, pathErr)
	}
	wargs := writeArgsFrom(cmd)
	code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath, Args: payload}, func(ctx context.Context) (any, error) {
		if err := safety.RequireWriteAllowed(wargs.Args); err != nil {
			return nil, err
		}
		db, err := store.Connect(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		if cached, err := idempotency.Lookup(db, wargs.IdempotencyKey, name); err != nil {
			return nil, err
		} else if cached != nil {
			if data, ok := cached["data"].(map[string]any); ok {
				out := map[string]any{}
				for k, v := range data {
					out[k] = v
				}
				out["idempotent_replay"] = true
				return out, nil
			}
			return cached, nil
		}
		if wargs.DryRun {
			out := map[string]any{"dry_run": true}
			for k, v := range payload {
				out[k] = v
			}
			return out, nil
		}
		if auditPath != "" {
			_ = audit.Pre(auditPath, audit.PreEntry{Cmd: name, RequestID: dispatch.RequestIDFrom(ctx), RPCMethod: method, PayloadPreview: payload})
		}
		c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		data, err := runner(ctx, c)
		if err != nil {
			return nil, err
		}
		if wargs.IdempotencyKey != "" {
			env := map[string]any{"ok": true, "command": name, "request_id": dispatch.RequestIDFrom(ctx), "data": data, "warnings": []string{}}
			if err := idempotency.Record(db, wargs.IdempotencyKey, name, dispatch.RequestIDFrom(ctx), env); err != nil {
				return nil, err
			}
		}
		return data, nil
	})
	storeExitCode(cmd, code)
	return nil
}

func folderSummaries(folders []client.FolderInfo) []map[string]any {
	out := make([]map[string]any, len(folders))
	for i, f := range folders {
		out[i] = folderSummary(f)
	}
	return out
}

func folderSummary(f client.FolderInfo) map[string]any {
	return map[string]any{
		"folder_id":          f.ID,
		"title":              f.Title,
		"emoticon":           f.Emoji,
		"is_default":         f.IsDefault || f.ID == 0,
		"include_peer_count": len(f.IncludeChatIDs),
		"exclude_peer_count": len(f.ExcludeChatIDs),
		"include_peers":      f.IncludeChatIDs,
		"exclude_peers":      f.ExcludeChatIDs,
	}
}

func nextFolderID(folders []client.FolderInfo) int64 {
	maxID := int64(1)
	for _, f := range folders {
		if f.ID > maxID {
			maxID = f.ID
		}
	}
	return maxID + 1
}

func optionalIntCSV(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return parseSignedNonZeroCSV(value, "chat-id")
}
