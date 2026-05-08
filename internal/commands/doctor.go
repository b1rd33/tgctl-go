package commands

import (
	"context"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// Version is the build version string. Set via -ldflags at release time.
var Version = "dev"

func registerDoctor(root *cobra.Command, m *accounts.Manager) {
	cmd := &cobra.Command{
		Use:          "doctor",
		Short:        "Diagnose tgctl-go setup",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			code := dispatch.Run("doctor", dispatch.Options{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			}, func(_ context.Context) (any, error) {
				return doctorReport(m), nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}

func doctorReport(m *accounts.Manager) map[string]any {
	credsErr := ""
	if _, _, err := client.EnsureCredentials(); err != nil {
		credsErr = err.Error()
	}
	currentName := m.Current()
	paths, _ := m.ResolvePaths(currentName)

	dbExists := false
	dbSchemaOK := false
	if _, err := os.Stat(paths.DBPath); err == nil {
		dbExists = true
		if db, err := store.ConnectReadonly(paths.DBPath); err == nil {
			var name string
			if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='tg_chats'").Scan(&name); err == nil {
				dbSchemaOK = name == "tg_chats"
			}
			db.Close()
		}
	}

	sessionExists := false
	if _, err := os.Stat(paths.SessionPath); err == nil {
		sessionExists = true
	}
	sessionLockHeld := false
	if _, err := os.Stat(paths.SessionPath + ".lock"); err == nil {
		sessionLockHeld = true
	}

	return map[string]any{
		"version":           Version,
		"go_runtime":        runtime.Version(),
		"os":                runtime.GOOS,
		"arch":              runtime.GOARCH,
		"credentials_ok":    credsErr == "",
		"credentials_error": credsErr,
		"current_account":   currentName,
		"account_paths": map[string]any{
			"account_dir":  paths.AccountDir,
			"db_path":      paths.DBPath,
			"session_path": paths.SessionPath,
			"audit_path":   paths.AuditPath,
			"media_dir":    paths.MediaDir,
		},
		"db_exists":            dbExists,
		"db_schema_ok":         dbSchemaOK,
		"session_exists":       sessionExists,
		"session_lock_present": sessionLockHeld,
		"client_kind":          "stub", // gotd wiring lands later
	}
}
