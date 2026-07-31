package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type meFetcher func(context.Context, string, bool) (client.User, error)

// fetchMeLive connects to Telegram without opening the SQLite cache.
func fetchMeLive(ctx context.Context, sessionPath string, readOnly bool) (client.User, error) {
	apiID, apiHash, err := client.EnsureCredentials()
	if err != nil {
		return client.User{}, err
	}
	var gc *client.GotdClient
	if readOnly {
		gc, err = client.NewReadonly(ctx, apiID, apiHash, sessionPath)
	} else {
		gc, err = client.New(ctx, apiID, apiHash, sessionPath, "")
	}
	if err != nil {
		return client.User{}, err
	}
	defer gc.Close()
	return gc.GetMe(ctx)
}

func meLiveRunner(ctx context.Context, dbPath, sessionPath string, cache bool, fetch meFetcher) (any, error) {
	me, err := fetch(ctx, sessionPath, !cache)
	if err != nil {
		return nil, err
	}
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
	if cache {
		db, err := store.Connect(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		if err := store.UpsertMe(db, row); err != nil {
			return nil, err
		}
		loaded, err := store.LoadMe(db)
		if err != nil {
			return nil, err
		}
		return mePayload(loaded, "live", sessionPath), nil
	}
	return mePayload(&row, "live", sessionPath), nil
}

// MeLiveRunner connects to Telegram and refreshes tg_me.
func MeLiveRunner(ctx context.Context, dbPath, sessionPath string) (any, error) {
	return meLiveRunner(ctx, dbPath, sessionPath, true, fetchMeLive)
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

func registerAuth(root *cobra.Command, paths AccountPathProvider) {
	registerAuthWithFetcher(root, paths, fetchMeLive)
}

func registerAuthWithFetcher(root *cobra.Command, paths AccountPathProvider, fetch meFetcher) {
	me := &cobra.Command{
		Use:          "me",
		Short:        "Print authenticated user info",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			offline, _ := cmd.Flags().GetBool("offline")
			readOnly := RootConfigFrom(cmd.Root()).ReadOnly || os.Getenv("TG_READONLY") == "1"
			account, err := selectedAccount(cmd, paths)
			if err != nil {
				return emitDispatchedFailure(cmd, "me", err)
			}
			dbPath, sessionPath, auditPath, err := accountPathsForMode(paths, account, readOnly)
			if err != nil {
				return emitDispatchedFailure(cmd, "me", err)
			}
			if readOnly {
				auditPath = ""
			}
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
				return meLiveRunner(ctx, dbPath, sessionPath, !readOnly, fetch)
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	me.Flags().Bool("offline", false, "Read cached self user info without connecting to Telegram")
	AddOutputFlags(me)
	root.AddCommand(me)
}

type readonlyAccountPathProvider interface {
	AccountPathsReadonly(account string) (db, session, audit string, err error)
}

func accountPathsForMode(paths AccountPathProvider, account string, readOnly bool) (db, session, audit string, err error) {
	if readOnly {
		if p, ok := paths.(readonlyAccountPathProvider); ok {
			return p.AccountPathsReadonly(account)
		}
	}
	return paths.AccountPaths(account)
}

// AccountPathProvider lets tests inject account paths and account selection
// without depending on the real filesystem layout.
type AccountPathProvider interface {
	AccountPaths(account string) (db, session, audit string, err error)
	Current() string
}

// selectedAccount is the single command-layer precedence policy for account
// selection: explicit flag, environment, persisted current account, default.
func selectedAccount(cmd *cobra.Command, paths AccountPathProvider) (string, error) {
	account := RootConfigFrom(cmd.Root()).Account
	if account == "" {
		account = os.Getenv("TG_ACCOUNT")
	}
	if account == "" {
		account = paths.Current()
	}
	if account == "" {
		account = accounts.DefaultAccount
	}
	if err := accounts.ValidateName(account); err != nil {
		return "", err
	}
	return account, nil
}
