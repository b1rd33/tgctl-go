package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// MeLiveRunner connects to Telegram via gotd and refreshes tg_me.
func MeLiveRunner(ctx context.Context, dbPath, sessionPath string) (any, error) {
	apiID, apiHash, err := client.EnsureCredentials()
	if err != nil {
		return nil, err
	}
	gc, err := client.New(ctx, apiID, apiHash, sessionPath)
	if err != nil {
		return nil, err
	}
	defer gc.Close()

	me, err := gc.GetMe(ctx)
	if err != nil {
		return nil, err
	}
	db, err := store.Connect(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	row := store.MeRow{
		UserID:      me.ID,
		Username:    nullStringOf(me.Username),
		Phone:       nullStringOf(me.Phone),
		FirstName:   nullStringOf(me.FirstName),
		LastName:    nullStringOf(me.LastName),
		DisplayName: nullStringOf(me.DisplayName),
		IsBot:       boolInt(me.IsBot),
		CachedAt:    timeNow(),
	}
	if err := store.UpsertMe(db, row); err != nil {
		return nil, err
	}
	loaded, err := store.LoadMe(db)
	if err != nil {
		return nil, err
	}
	return mePayload(loaded, "live", sessionPath), nil
}

// MeOfflineRunner returns the cached self-user envelope data, or
// *resolve.NotFound when the cache is empty. Connecting and closing the DB is
// the runner's responsibility so the dispatch chokepoint stays uniform.
func MeOfflineRunner(_ context.Context, dbPath, sessionPath string) (any, error) {
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	row, err := store.LoadMe(db)
	if err == sql.ErrNoRows {
		return nil, resolve.NewNotFound("No cached self user info. Run 'tg me' once before using --offline.")
	}
	if err != nil {
		return nil, err
	}
	return mePayload(row, "cache", sessionPath), nil
}

func mePayload(row *store.MeRow, source, sessionPath string) map[string]any {
	var raw any
	if row.RawJSON.Valid && row.RawJSON.String != "" {
		_ = json.Unmarshal([]byte(row.RawJSON.String), &raw)
	}
	return map[string]any{
		"source":       source,
		"user_id":      row.UserID,
		"username":     nullString(row.Username),
		"phone":        nullString(row.Phone),
		"first_name":   nullString(row.FirstName),
		"last_name":    nullString(row.LastName),
		"display_name": nullString(row.DisplayName),
		"is_bot":       row.IsBot != 0,
		"cached_at":    row.CachedAt,
		"session_path": sessionPath,
		"raw_json":     raw,
	}
}

func nullString(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func meHumanFormatter(stdout interface{ Write([]byte) (int, error) }) func(any) {
	return func(data any) {
		m, ok := data.(map[string]any)
		if !ok {
			return
		}
		username := "(no username)"
		if u, ok := m["username"].(string); ok && u != "" {
			username = "@" + u
		}
		fmt.Fprintf(stdout, "%v (%s) id %v\n", m["display_name"], username, m["user_id"])
		fmt.Fprintf(stdout, "Source: %v  Cached: %v\n", m["source"], m["cached_at"])
	}
}

func registerAuth(root *cobra.Command, paths AuthPathProvider) {
	me := &cobra.Command{
		Use:          "me",
		Short:        "Print authenticated user info",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			offline, _ := cmd.Flags().GetBool("offline")
			cfg := RootConfigFrom(cmd.Root())
			account := cfg.Account
			if account == "" {
				account = "default"
			}
			dbPath, sessionPath, auditPath := paths.AccountPaths(account)
			code := dispatch.Run("me", dispatch.Options{
				JSON:           jsonMode(cmd),
				Stdout:         cmd.OutOrStdout(),
				Stderr:         cmd.ErrOrStderr(),
				HumanFormatter: meHumanFormatter(cmd.OutOrStdout()),
				AuditPath:      auditPath,
				Args:           map[string]any{"offline": offline},
			}, func(ctx context.Context) (any, error) {
				if offline {
					return MeOfflineRunner(ctx, dbPath, sessionPath)
				}
				return MeLiveRunner(ctx, dbPath, sessionPath)
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	me.Flags().Bool("offline", false, "Read cached self user info without connecting to Telegram")
	AddOutputFlags(me)
	root.AddCommand(me)
}

// AuthPathProvider lets tests inject account paths without depending on the
// real filesystem layout. Phase 15 will provide the production implementation.
type AuthPathProvider interface {
	AccountPaths(account string) (db, session, audit string)
}
