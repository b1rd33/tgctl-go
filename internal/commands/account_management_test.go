package commands

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/client"
	"strings"
	"testing"
)

func TestAccountManagementUsesSelectedAccount(t *testing.T) {
	for _, args := range [][]string{{"contact-add", "1", "--first-name", "Test", "--confirm", "1"}, {"contact-remove", "1", "--confirm", "1"}, {"logout", "--confirm", "alpha"}} {
		t.Run(args[0], func(t *testing.T) {
			root := t.TempDir()
			alpha := operationAccount(t, root, "alpha", 1, "A")
			beta := operationAccount(t, root, "beta", 1, "B")
			cfg, fc, _ := setupWriteEnv(t)
			cfg.Paths = &adversarialAccountPaths{current: []string{"beta"}, accounts: map[string]operationTestPaths{"alpha": alpha, "beta": beta}}
			cfg.ClientFactory = func(_ context.Context, session, db string) (client.Client, error) {
				if db != alpha.db || session != alpha.session {
					t.Fatal("wrong account client")
				}
				return fc, nil
			}
			command := append([]string{"--account", "alpha"}, args...)
			command = append(command, "--allow-write", "--idempotency-key", "isolation", "--json")
			if out, code := runRoot(t, cfg, command...); code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			if idempotencyCount(t, alpha.db, "isolation") != 1 || idempotencyCount(t, beta.db, "isolation") != 0 {
				t.Fatal("cross-account state")
			}
		})
	}
}

func TestContactAndLogoutGatesAndReplay(t *testing.T) {
	for _, base := range [][]string{{"contact-add", "1", "--first-name", "Test", "--confirm", "1"}, {"contact-remove", "1", "--confirm", "1"}, {"logout", "--account", "default", "--confirm", "default"}} {
		t.Run(base[0], func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			if out, code := runRoot(t, cfg, append(append([]string{}, base...), "--json")...); code != 6 || len(fc.Calls) != 0 {
				t.Fatalf("ungated: %d %s", code, out)
			}
			if out, code := runRoot(t, cfg, append(append([]string{}, base...), "--read-only", "--allow-write", "--json")...); code != 6 || len(fc.Calls) != 0 {
				t.Fatalf("read-only: %d %s", code, out)
			}
			if out, code := runRoot(t, cfg, append(append([]string{}, base...), "--allow-write", "--dry-run", "--json")...); code != 0 || len(fc.Calls) != 0 {
				t.Fatalf("dry-run: %d %s", code, out)
			}
			bad := append([]string{}, base...)
			bad[len(bad)-1] = "wrong"
			if out, code := runRoot(t, cfg, append(bad, "--allow-write", "--json")...); code == 0 || len(fc.Calls) != 0 {
				t.Fatalf("wrong confirm: %d %s", code, out)
			}
			args := append(append([]string{}, base...), "--allow-write", "--idempotency-key", "test", "--json")
			if out, code := runRoot(t, cfg, args...); code != 0 {
				t.Fatalf("write: %d %s", code, out)
			}
			count := len(fc.Calls)
			if out, code := runRoot(t, cfg, args...); code != 0 || !strings.Contains(out, `"idempotent_replay":true`) || len(fc.Calls) != count {
				t.Fatalf("replay: %d %s", code, out)
			}
			if len(fc.AddedContacts) > 0 && fc.AddedContacts[0].SharePhone {
				t.Fatal("phone shared implicitly")
			}
		})
	}
}

func TestContactSharePhoneAndCommittedCloseFailure(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.CloseErr = errors.New("private detail")
	args := []string{"contact-add", "1", "--first-name", "Test", "--share-phone", "--confirm", "1", "--allow-write", "--idempotency-key", "contact", "--json"}
	out, code := runRoot(t, cfg, args...)
	if code == 0 || !strings.Contains(out, `"committed":true`) || strings.Contains(out, "private detail") || len(fc.AddedContacts) != 1 || !fc.AddedContacts[0].SharePhone {
		t.Fatalf("code=%d out=%s", code, out)
	}
	runRoot(t, cfg, args...)
	if len(fc.AddedContacts) != 1 {
		t.Fatal("committed failure retried")
	}
}
