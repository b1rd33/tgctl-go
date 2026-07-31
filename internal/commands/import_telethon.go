package commands

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/gotd/td/session"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// registerImportTelethon adds `tg import-telethon-session <telethon-session-path>`.
//
// Telethon stores its MTProto state in a SQLite DB (the file users normally
// know as `<name>.session`). The auth key inside is identical in protocol
// terms to gotd/td's: 256 bytes, used the same way against Telegram. We can
// reuse it without re-running the SMS-code flow.
//
// Conversion: read sessions row → derive AuthKeyID = last 8 bytes of
// SHA1(AuthKey) → write the gotd jsonData envelope to the current account's
// session path.
func registerImportTelethon(root *cobra.Command, mgr *accounts.Manager) {
	cmd := &cobra.Command{
		Use:          "import-telethon-session <path>",
		Short:        "Adopt a Python tgctl/Telethon session as the current Go account's session",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			account, err := selectedAccount(cmd, mgr)
			if err != nil {
				return emitDispatchedFailure(cmd, "import-telethon-session", err)
			}
			paths, err := mgr.ResolvePaths(account)
			if err != nil {
				return emitDispatchedFailure(cmd, "import-telethon-session", err)
			}

			code := dispatch.Run("import-telethon-session", dispatch.Options{
				JSON:      jsonMode(cmd),
				Stdout:    cmd.OutOrStdout(),
				Stderr:    cmd.ErrOrStderr(),
				AuditPath: paths.AuditPath,
				Args:      map[string]any{"source": src, "account": account},
			}, func(_ context.Context) (any, error) {
				return convertTelethonSession(src, paths.SessionPath)
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}

func convertTelethonSession(srcPath, dstPath string) (any, error) {
	if _, err := os.Stat(srcPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, safety.NewBadArgs("telethon session not found at %s", srcPath)
		}
		return nil, err
	}

	uri := "file:" + srcPath + "?mode=ro"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open telethon session: %w", err)
	}
	defer db.Close()

	var (
		dcID    int
		addr    string
		port    int
		authKey []byte
	)
	row := db.QueryRow("SELECT dc_id, server_address, port, auth_key FROM sessions LIMIT 1")
	if err := row.Scan(&dcID, &addr, &port, &authKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, safety.NewBadArgs("telethon session has no sessions row at %s", srcPath)
		}
		return nil, fmt.Errorf("read telethon sessions: %w", err)
	}
	if len(authKey) != 256 {
		return nil, fmt.Errorf("unexpected auth_key length %d (want 256) — is %s really a Telethon session?", len(authKey), srcPath)
	}

	keyID := authKeyID(authKey)
	data := session.Data{
		DC:        dcID,
		Addr:      fmt.Sprintf("%s:%d", addr, port),
		AuthKey:   authKey,
		AuthKeyID: keyID,
	}

	envelope := struct {
		Version int          `json:"Version"`
		Data    session.Data `json:"Data"`
	}{Version: 1, Data: data}
	buf, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(parentDir(dstPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dstPath, buf, 0o600); err != nil {
		return nil, err
	}

	return map[string]any{
		"source":          srcPath,
		"destination":     dstPath,
		"dc":              dcID,
		"addr":            fmt.Sprintf("%s:%d", addr, port),
		"auth_key_id_hex": fmt.Sprintf("%x", keyID),
		"auth_key_bytes":  len(authKey),
	}, nil
}

func authKeyID(authKey []byte) []byte {
	h := sha1.Sum(authKey)
	return h[len(h)-8:]
}

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
