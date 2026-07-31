package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/accounts"
)

func TestAccountsRemoveUsesTypedConfirmationContract(t *testing.T) {
	tests := []struct {
		name    string
		confirm string
		want    int
		removed bool
	}{
		{name: "missing", want: 7},
		{name: "mismatch", confirm: "other", want: 2},
		{name: "trimmed match", confirm: " work ", want: 0, removed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := t.TempDir()
			mgr := accounts.New(rootDir)
			accountDir, err := mgr.Add("work")
			if err != nil {
				t.Fatal(err)
			}
			root := NewRootCommand()
			registerAccountCommands(root, mgr)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			args := []string{"accounts-remove", "work", "--json"}
			if tt.confirm != "" {
				args = append(args, "--confirm", tt.confirm)
			}
			root.SetArgs(args)
			if code := ExecuteRoot(root); code != tt.want {
				t.Fatalf("exit code = %d, want %d", code, tt.want)
			}
			_, statErr := os.Stat(accountDir)
			if tt.removed {
				if !os.IsNotExist(statErr) {
					t.Fatalf("account was not removed: %v", statErr)
				}
			} else if statErr != nil {
				t.Fatalf("account changed: %v", statErr)
			}
			assertPathMissing(t, filepath.Join(rootDir, "accounts", accounts.CurrentFile))
		})
	}
}
