package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

func registerSetup(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:          "setup",
		Short:        "Create or update a private .env with Telegram app credentials",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := safety.RequireWritesNotReadOnly(safety.Args{ReadOnly: RootConfigFrom(cmd.Root()).ReadOnly}); err != nil {
				return emitDispatchedFailure(cmd, "setup", err)
			}
			envPath, _ := cmd.Flags().GetString("env-file")
			if strings.TrimSpace(envPath) == "" {
				return emitDispatchedFailure(cmd, "setup", safety.NewBadArgs("--env-file cannot be blank"))
			}
			envPath, err := filepath.Abs(filepath.Clean(envPath))
			if err != nil {
				return emitDispatchedFailure(cmd, "setup", err)
			}
			apiID, _ := cmd.Flags().GetString("api-id")
			apiHash, _ := cmd.Flags().GetString("api-hash")
			if apiID == "" || apiHash == "" {
				apiID, apiHash, err = promptCredentials(cmd)
				if err != nil {
					return emitDispatchedFailure(cmd, "setup", err)
				}
			}
			if _, _, err := validateSetupCredentials(apiID, apiHash); err != nil {
				return emitDispatchedFailure(cmd, "setup", err)
			}
			code := dispatch.Run("setup", dispatch.Options{JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(context.Context) (any, error) {
				if err := writeEnvCredentials(envPath, apiID, apiHash); err != nil {
					return nil, err
				}
				return map[string]any{"env_file": envPath, "api_id_set": true, "api_hash_set": true}, nil
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().String("api-id", "", "Telegram app API ID (never printed)")
	cmd.Flags().String("api-hash", "", "Telegram app API hash (never printed)")
	cmd.Flags().String("env-file", ".env", "Environment file to create or update")
	AddOutputFlags(cmd)
	root.AddCommand(cmd)
}

func validateSetupCredentials(rawID, apiHash string) (int, string, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
	if err != nil || parsed <= 0 || parsed > 1<<31-1 {
		return 0, "", safety.NewBadArgs("--api-id must be a positive 32-bit integer")
	}
	apiHash = strings.TrimSpace(apiHash)
	if apiHash == "" {
		return 0, "", safety.NewBadArgs("--api-hash cannot be blank")
	}
	return int(parsed), apiHash, nil
}

func promptCredentials(cmd *cobra.Command) (string, string, error) {
	in := bufio.NewReader(os.Stdin)
	fmt.Fprint(cmd.ErrOrStderr(), "Telegram API ID: ")
	rawID, err := in.ReadString('\n')
	if err != nil {
		return "", "", err
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Telegram API hash: ")
	var rawHash string
	if term.IsTerminal(int(syscall.Stdin)) {
		b, readErr := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(cmd.ErrOrStderr())
		if readErr != nil {
			return "", "", readErr
		}
		rawHash = string(b)
	} else {
		rawHash, err = in.ReadString('\n')
		if err != nil {
			return "", "", err
		}
	}
	return strings.TrimSpace(rawID), strings.TrimSpace(rawHash), nil
}

func writeEnvCredentials(path, apiID, apiHash string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	lines := strings.SplitAfter(text, "\n")
	if text == "" {
		lines = nil
	}
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		lines[len(lines)-1] += "\n"
	}
	seenID, seenHash := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		suffix := "\n"
		if !strings.HasSuffix(line, "\n") {
			suffix = ""
		}
		switch key {
		case "TG_API_ID":
			lines[i] = "TG_API_ID=" + strings.TrimSpace(apiID) + suffix
			seenID = true
		case "TG_API_HASH":
			lines[i] = "TG_API_HASH=" + strings.TrimSpace(apiHash) + suffix
			seenHash = true
		}
	}
	if !seenID {
		lines = append(lines, "TG_API_ID="+strings.TrimSpace(apiID)+"\n")
	}
	if !seenHash {
		lines = append(lines, "TG_API_HASH="+strings.TrimSpace(apiHash)+"\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		return err
	}
	return f.Sync()
}
