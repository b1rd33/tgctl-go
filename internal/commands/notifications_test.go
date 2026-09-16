package commands

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/spf13/cobra"
)

func TestParseMutePlanModesAndBounds(t *testing.T) {
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.FixedZone("EEST", 3*60*60))
	tests := []struct {
		name     string
		args     []string
		wantMode string
		wantUnix int
		wantErr  string
	}{
		{name: "duration", args: []string{"--for", "8h"}, wantMode: "duration", wantUnix: int(now.Add(8 * time.Hour).Unix())},
		{name: "absolute timezone", args: []string{"--until", "2026-09-16T20:00:00+03:00"}, wantMode: "until", wantUnix: 1789578000},
		{name: "forever", args: []string{"--forever"}, wantMode: "forever", wantUnix: foreverMuteUntil},
		{name: "missing", wantErr: "exactly one"},
		{name: "conflicting", args: []string{"--for", "1h", "--forever"}, wantErr: "exactly one"},
		{name: "zero", args: []string{"--for", "0s"}, wantErr: "greater than zero"},
		{name: "past", args: []string{"--until", "2026-09-15T09:00:00+03:00"}, wantErr: "future"},
		{name: "malformed", args: []string{"--until", "tomorrow"}, wantErr: "RFC3339"},
		{name: "overflow", args: []string{"--until", "2038-01-19T03:14:07Z"}, wantErr: "timestamp range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "mute"}
			cmd.Flags().Duration("for", 0, "")
			cmd.Flags().String("until", "", "")
			cmd.Flags().Bool("forever", false, "")
			if err := cmd.Flags().Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			got, err := parseMutePlan(cmd, now)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.Mode != tt.wantMode || got.MuteUntil != tt.wantUnix {
				t.Fatalf("plan=%#v err=%v", got, err)
			}
		})
	}
}

func TestRelativeMuteFingerprintDoesNotDependOnComputedDeadline(t *testing.T) {
	first := &cobra.Command{Use: "mute"}
	first.Flags().String("idempotency-fingerprint", "", "")
	second := &cobra.Command{Use: "mute"}
	second.Flags().String("idempotency-fingerprint", "", "")
	setNotificationFingerprint(first, "mute", "1", mutePlan{Mode: "duration", MuteUntil: 100, Spec: "8h0m0s"})
	setNotificationFingerprint(second, "mute", "1", mutePlan{Mode: "duration", MuteUntil: 200, Spec: "8h0m0s"})
	a, _ := first.Flags().GetString("idempotency-fingerprint")
	b, _ := second.Flags().GetString("idempotency-fingerprint")
	if a == "" || a != b {
		t.Fatalf("relative request fingerprints differ: %q != %q", a, b)
	}
}

func TestMuteUnmuteRouteOnePeerAndReplay(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)

	first, code := runRoot(t, cfg, "mute", "1", "--forever", "--allow-write", "--idempotency-key", "mute-key", "--json")
	if code != 0 || !strings.Contains(first, `"muted":true`) || !strings.Contains(first, `"mode":"forever"`) || !strings.Contains(first, `"cache_refresh":"required"`) {
		t.Fatalf("first code=%d output=%s", code, first)
	}
	if len(fc.PeerNotifyUpdates) != 1 || fc.PeerNotifyUpdates[0] != (client.PeerNotifySettingsReq{ChatID: 1, MuteUntil: foreverMuteUntil}) {
		t.Fatalf("updates=%#v", fc.PeerNotifyUpdates)
	}
	second, code := runRoot(t, cfg, "mute", "1", "--forever", "--allow-write", "--idempotency-key", "mute-key", "--json")
	if code != 0 || !strings.Contains(second, `"idempotent_replay":true`) || len(fc.PeerNotifyUpdates) != 1 {
		t.Fatalf("replay code=%d output=%s updates=%#v", code, second, fc.PeerNotifyUpdates)
	}
	if out, code := runRoot(t, cfg, "mute", "1", "--for", "1h", "--allow-write", "--idempotency-key", "mute-key", "--json"); code == 0 || !strings.Contains(out, "different request") {
		t.Fatalf("changed request code=%d output=%s", code, out)
	}
	if out, code := runRoot(t, cfg, "unmute", "1", "--allow-write", "--idempotency-key", "unmute-key", "--json"); code != 0 || !strings.Contains(out, `"muted":false`) {
		t.Fatalf("unmute code=%d output=%s", code, out)
	}
	if len(fc.PeerNotifyUpdates) != 2 || fc.PeerNotifyUpdates[1] != (client.PeerNotifySettingsReq{ChatID: 1, MuteUntil: 0}) {
		t.Fatalf("updates=%#v", fc.PeerNotifyUpdates)
	}
}

func TestMutePreflightDryRunReadOnlyAndFuzzy(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMe(db, store.MeRow{UserID: 1, DisplayName: sql.NullString{String: "Bjørn Müller", Valid: true}, CachedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if out, code := runRoot(t, cfg, "mute", "self", "--for", "1h", "--allow-write", "--dry-run", "--json"); code != 0 || !strings.Contains(out, `"dry_run":true`) || len(fc.PeerNotifyUpdates) != 0 {
		t.Fatalf("dry-run code=%d output=%s updates=%#v", code, out, fc.PeerNotifyUpdates)
	}
	if out, code := runRoot(t, cfg, "--read-only", "mute", "1", "--forever", "--allow-write", "--json"); code != 6 || len(fc.PeerNotifyUpdates) != 0 {
		t.Fatalf("read-only code=%d output=%s", code, out)
	}
	if out, code := runRoot(t, cfg, "mute", "Bjørn Müller", "--forever", "--allow-write", "--json"); code != 2 || len(fc.PeerNotifyUpdates) != 0 {
		t.Fatalf("fuzzy omission code=%d output=%s", code, out)
	}
	if out, code := runRoot(t, cfg, "mute", "Bjørn Müller", "--forever", "--allow-write", "--fuzzy", "--json"); code != 0 || len(fc.PeerNotifyUpdates) != 1 {
		t.Fatalf("fuzzy code=%d output=%s updates=%#v", code, out, fc.PeerNotifyUpdates)
	}
	if out, code := runRoot(t, cfg, "mute", "1", "--allow-write", "--json"); code != 2 || len(fc.PeerNotifyUpdates) != 1 {
		t.Fatalf("invalid mode reached Telegram: code=%d output=%s updates=%#v", code, out, fc.PeerNotifyUpdates)
	}
}

func TestMuteUsesSelectedAccountAndReportsFailures(t *testing.T) {
	root := t.TempDir()
	alpha := operationAccount(t, root, "alpha", 1, "Alpha")
	beta := operationAccount(t, root, "beta", 1, "Beta")
	paths := &adversarialAccountPaths{current: []string{"beta"}, accounts: map[string]operationTestPaths{"alpha": alpha, "beta": beta}}
	cfg, fc, _ := setupWriteEnv(t)
	cfg.Paths = paths
	clientDB := ""
	cfg.ClientFactory = func(_ context.Context, _, dbPath string) (client.Client, error) {
		clientDB = dbPath
		return fc, nil
	}
	out, code := runRoot(t, cfg, "--account", "alpha", "mute", "1", "--forever", "--allow-write", "--idempotency-key", "account-mute", "--json")
	if code != 0 || clientDB != alpha.db || idempotencyCount(t, alpha.db, "account-mute") != 1 || idempotencyCount(t, beta.db, "account-mute") != 0 {
		t.Fatalf("code=%d output=%s clientDB=%q", code, out, clientDB)
	}

	fc.PeerNotifyErr = context.Canceled
	out, code = runRoot(t, cfg, "--account", "alpha", "mute", "1", "--for", "1h", "--allow-write", "--json")
	if code == 0 || strings.Contains(out, `"ok":true`) {
		t.Fatalf("cancellation code=%d output=%s", code, out)
	}
}

func TestMutePersistenceFailureReportsCommittedOutcome(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER fail_mute_finalize BEFORE UPDATE OF result_json ON tg_idempotency BEGIN SELECT RAISE(FAIL, 'injected finalization failure'); END"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, code := runRoot(t, cfg, "mute", "1", "--forever", "--allow-write", "--idempotency-key", "mute-failure", "--json")
	if code == 0 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"mute_until_unix":2147483647`) || len(fc.PeerNotifyUpdates) != 1 {
		t.Fatalf("code=%d output=%s updates=%#v", code, out, fc.PeerNotifyUpdates)
	}
}
