package commands

import (
	"context"
	"database/sql"
	"math"

	"github.com/gotd/td/tg"
	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
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
			code := dispatch.Run("backfill-entities", dispatch.Options{Context: cmd.Context(),
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
	if limit <= 0 {
		limit = 200
	}
	if limit > 10000 {
		return nil, safety.NewBadArgs("limit exceeds 10000")
	}
	c, err := client.New(ctx, apiID, apiHash, sessionPath, dbPath)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	dialogs, err := c.DiscoverDialogs(ctx, limit)
	if err != nil {
		return nil, err
	}
	users, channels, groups := 0, 0, 0
	for _, d := range dialogs {
		switch d.Type {
		case "user":
			users++
		case "channel", "supergroup":
			channels++
		case "group":
			groups++
		}
	}
	return map[string]any{"users_cached": users, "channels_cached": channels, "basic_groups": groups, "entities_cached": len(dialogs), "limit": limit, "may_have_more": len(dialogs) == limit}, nil
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
