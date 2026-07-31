package commands

import (
	"context"
	"database/sql"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerExtraReadCommands(root *cobra.Command, paths AccountPathProvider) {
	root.AddCommand(statsCommand(paths))
	root.AddCommand(contactsCommand(paths))
	root.AddCommand(unreadCommand(paths))
}

func statsCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "stats",
		Short:        "Show local cache statistics",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDispatchedRead(cmd, "stats", nil, paths, func(ctx context.Context, p readPaths) (any, error) {
				db, err := store.Connect(p.db)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				return map[string]any{
					"chats":    countTable(db, "tg_chats"),
					"messages": countTable(db, "tg_messages"),
					"contacts": countTable(db, "tg_contacts"),
				}, nil
			})
		},
	}
	AddOutputFlags(cmd)
	return cmd
}

func contactsCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "contacts",
		Short:        "List cached contacts",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runDispatchedRead(cmd, "contacts", map[string]any{"limit": limit}, paths, func(ctx context.Context, p readPaths) (any, error) {
				db, err := store.Connect(p.db)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				rows, err := db.Query(`SELECT user_id, phone, first_name, last_name, username, is_mutual FROM tg_contacts ORDER BY first_name, username LIMIT ?`, positiveLimit(limit, 100))
				if err != nil {
					return nil, err
				}
				defer rows.Close()
				var out []map[string]any
				for rows.Next() {
					var id, mutual sql.NullInt64
					var phone, first, last, username sql.NullString
					if err := rows.Scan(&id, &phone, &first, &last, &username, &mutual); err != nil {
						return nil, err
					}
					out = append(out, map[string]any{
						"user_id": id.Int64, "phone": phone.String, "first_name": first.String,
						"last_name": last.String, "username": username.String, "is_mutual": mutual.Int64 != 0,
					})
				}
				return map[string]any{"contacts": out}, rows.Err()
			})
		},
	}
	cmd.Flags().Int("limit", 100, "Maximum contacts")
	AddOutputFlags(cmd)
	return cmd
}

func unreadCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "unread",
		Short:        "List recently cached incoming messages",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runDispatchedRead(cmd, "unread", map[string]any{"limit": limit}, paths, func(ctx context.Context, p readPaths) (any, error) {
				db, err := store.Connect(p.db)
				if err != nil {
					return nil, err
				}
				defer db.Close()
				rows, err := db.Query(`SELECT chat_id, message_id, date, text FROM tg_messages WHERE COALESCE(is_outgoing,0)=0 AND COALESCE(deleted,0)=0 ORDER BY date DESC LIMIT ?`, positiveLimit(limit, 50))
				if err != nil {
					return nil, err
				}
				defer rows.Close()
				var out []map[string]any
				for rows.Next() {
					var chatID, msgID int64
					var date string
					var text sql.NullString
					if err := rows.Scan(&chatID, &msgID, &date, &text); err != nil {
						return nil, err
					}
					out = append(out, map[string]any{"chat_id": chatID, "message_id": msgID, "date": date, "text": text.String})
				}
				return map[string]any{"messages": out}, rows.Err()
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Maximum messages")
	AddOutputFlags(cmd)
	return cmd
}

func countTable(db *sql.DB, table string) int {
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
	return n
}
