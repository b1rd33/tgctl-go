package commands

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// registerBackfillEntities adds `tg backfill-entities`. It calls
// messages.GetDialogs once and writes every user / channel access_hash it
// learns into tg_entities and tg_chats. This unblocks chat_id-keyed write
// commands without needing a full message backfill.
func registerBackfillEntities(root *cobra.Command, mgr *accounts.Manager) {
	cmd := &cobra.Command{
		Use:          "backfill-entities",
		Short:        "Populate the local entity cache so chat_id-keyed sends work",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			var err error
			limit, err = defaultedInt32Limit(limit, 200, "--limit")
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill-entities", err)
			}
			if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
				return emitDispatchedFailure(cmd, "backfill-entities", err)
			}
			apiID, apiHash, err := client.EnsureCredentials()
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill-entities", err)
			}
			account, err := selectedAccount(cmd, mgr)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill-entities", err)
			}
			paths, err := mgr.ResolvePaths(account)
			if err != nil {
				return emitDispatchedFailure(cmd, "backfill-entities", err)
			}
			code := dispatch.Run("backfill-entities", dispatch.Options{
				JSON:      jsonMode(cmd),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				AuditPath: paths.AuditPath,
				Args:      map[string]any{"account": account, "limit": limit},
			}, func(ctx context.Context) (any, error) {
				return runBackfillEntities(ctx, apiID, apiHash, paths.SessionPath, paths.DBPath, limit)
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Int("limit", 200, "Max dialogs to fetch in one pass (Telegram caps at ~200)")
	addLocalWriteFlags(cmd)
	root.AddCommand(cmd)
}

func runBackfillEntities(ctx context.Context, apiID int, apiHash, sessionPath, dbPath string, limit int) (map[string]any, error) {
	if apiID <= 0 || int64(apiID) > math.MaxInt32 {
		return nil, safety.NewMissingCredentials("TG_API_ID must be a positive 32-bit integer")
	}
	var err error
	limit, err = defaultedInt32Limit(limit, 200, "limit")
	if err != nil {
		return nil, err
	}
	storage := &session.FileStorage{Path: sessionPath}
	tgc := telegram.NewClient(apiID, apiHash, telegram.Options{SessionStorage: storage})

	db, err := store.Connect(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var (
		users    int
		channels int
		basic    int
	)
	err = tgc.Run(ctx, func(rctx context.Context) error {
		api := tgc.API()
		dialogs, err := api.MessagesGetDialogs(rctx, &tg.MessagesGetDialogsRequest{
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      limit,
		})
		if err != nil {
			return err
		}
		var (
			respUsers []tg.UserClass
			respChats []tg.ChatClass
		)
		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			respUsers, respChats = d.Users, d.Chats
		case *tg.MessagesDialogsSlice:
			respUsers, respChats = d.Users, d.Chats
		default:
			return fmt.Errorf("unexpected dialogs response type %T", dialogs)
		}
		for _, u := range respUsers {
			if user, ok := u.(*tg.User); ok && !user.Min {
				if err := store.UpsertEntity(db, user.ID, store.EntityUser, user.AccessHash); err != nil {
					return err
				}
				upsertChatRow(db, user.ID, "user", chatTitleFromUser(user), user.Username)
				users++
			}
		}
		for _, c := range respChats {
			switch v := c.(type) {
			case *tg.Channel:
				if !v.Min {
					if err := store.UpsertEntity(db, v.ID, store.EntityChannel, v.AccessHash); err != nil {
						return err
					}
					upsertChatRow(db, v.ID, "channel", v.Title, v.Username)
					channels++
				}
			case *tg.Chat:
				if err := store.UpsertEntity(db, v.ID, store.EntityChat, 0); err != nil {
					return err
				}
				upsertChatRow(db, v.ID, "group", v.Title, "")
				basic++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"users_cached":    users,
		"channels_cached": channels,
		"basic_groups":    basic,
		"entities_cached": users + channels + basic,
		"db_path":         dbPath,
	}, nil
}

func chatTitleFromUser(u *tg.User) string {
	first := u.FirstName
	last := u.LastName
	if first != "" && last != "" {
		return first + " " + last
	}
	if first != "" {
		return first
	}
	if last != "" {
		return last
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return ""
}

// upsertChatRow inserts or updates a row in tg_chats with the values learned
// from a dialog. An empty username is stored as NULL.
func upsertChatRow(db *sql.DB, id int64, kind, title, username string) {
	var u any
	if username != "" {
		u = username
	}
	_, _ = db.Exec(`
		INSERT INTO tg_chats(chat_id, type, title, username)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			type = excluded.type,
			title = excluded.title,
			username = excluded.username`,
		id, kind, title, u)
}
