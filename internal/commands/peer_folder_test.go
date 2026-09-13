package commands

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestArchiveUnarchiveRouteOnePeerAndReplayByRequest(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO tg_chats(chat_id, title) VALUES (2, 'Second Chat')"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	first, code := runRoot(t, cfg, "archive", "1", "--allow-write", "--idempotency-key", "archive-key", "--json")
	if code != 0 || !strings.Contains(first, `"folder_id":1`) || !strings.Contains(first, `"archived":true`) || !strings.Contains(first, `"cache_refresh":"required"`) {
		t.Fatalf("first code=%d output=%s", code, first)
	}
	if len(fc.PeerFolderUpdates) != 1 || fc.PeerFolderUpdates[0] != (client.PeerFolderReq{ChatID: 1, FolderID: 1}) {
		t.Fatalf("peer folder updates=%#v", fc.PeerFolderUpdates)
	}
	if len(fc.Calls) != 1 || fc.Calls[0] != "SetPeerFolder" {
		t.Fatalf("calls=%#v, custom folders/pins were touched", fc.Calls)
	}

	second, code := runRoot(t, cfg, "archive", "1", "--allow-write", "--idempotency-key", "archive-key", "--json")
	if code != 0 || !strings.Contains(second, `"idempotent_replay":true`) || len(fc.PeerFolderUpdates) != 1 {
		t.Fatalf("replay code=%d output=%s updates=%#v", code, second, fc.PeerFolderUpdates)
	}
	if out, code := runRoot(t, cfg, "archive", "2", "--allow-write", "--idempotency-key", "archive-key", "--json"); code == 0 || !strings.Contains(out, "different request") || len(fc.PeerFolderUpdates) != 1 {
		t.Fatalf("changed target code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
	if out, code := runRoot(t, cfg, "unarchive", "1", "--allow-write", "--idempotency-key", "archive-key", "--json"); code == 0 || !strings.Contains(out, "already used for command") || len(fc.PeerFolderUpdates) != 1 {
		t.Fatalf("changed action code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
}

func TestArchiveDryRunReadOnlyAndFuzzyGates(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMe(db, store.MeRow{UserID: 1, DisplayName: sql.NullString{String: "Bjørn Müller", Valid: true}, CachedAt: "now"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"archive", "unarchive"} {
		out, code := runRoot(t, cfg, command, "self", "--allow-write", "--dry-run", "--json")
		if code != 0 || !strings.Contains(out, `"dry_run":true`) || len(fc.PeerFolderUpdates) != 0 {
			t.Fatalf("%s self dry-run code=%d output=%s updates=%#v", command, code, out, fc.PeerFolderUpdates)
		}
	}
	out, code := runRoot(t, cfg, "archive", "1", "--allow-write", "--dry-run", "--json")
	if code != 0 || !strings.Contains(out, `"dry_run":true`) || len(fc.PeerFolderUpdates) != 0 {
		t.Fatalf("dry-run code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
	if out, code := runRoot(t, cfg, "--read-only", "archive", "1", "--allow-write", "--json"); code != 6 || len(fc.PeerFolderUpdates) != 0 {
		t.Fatalf("read-only code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
	if out, code := runRoot(t, cfg, "archive", "Bjørn Müller", "--allow-write", "--json"); code != 2 || len(fc.PeerFolderUpdates) != 0 {
		t.Fatalf("fuzzy omission code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
	if out, code := runRoot(t, cfg, "archive", "Bjørn Müller", "--allow-write", "--fuzzy", "--json"); code != 0 || len(fc.PeerFolderUpdates) != 1 || fc.PeerFolderUpdates[0].ChatID != 1 {
		t.Fatalf("fuzzy archive code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
}

func TestArchiveUsesSelectedAccountAndPreservesIsolation(t *testing.T) {
	root := t.TempDir()
	alpha := operationAccount(t, root, "alpha", 1, "Alpha chat")
	beta := operationAccount(t, root, "beta", 1, "Beta chat")
	paths := &adversarialAccountPaths{
		current:  []string{"beta"},
		accounts: map[string]operationTestPaths{"alpha": alpha, "beta": beta},
	}
	cfg, fc, _ := setupWriteEnv(t)
	cfg.Paths = paths
	clientDBPath := ""
	cfg.ClientFactory = func(_ context.Context, _, dbPath string) (client.Client, error) {
		clientDBPath = dbPath
		return fc, nil
	}

	out, code := runRoot(t, cfg, "--account", "alpha", "archive", "1", "--allow-write", "--idempotency-key", "account-archive", "--json")
	if code != 0 {
		t.Fatalf("code=%d output=%s", code, out)
	}
	if clientDBPath != alpha.db {
		t.Fatalf("client DB path=%q want alpha %q", clientDBPath, alpha.db)
	}
	if paths.currentCalls != 0 || paths.readonlyCalls != 1 || paths.writableCalls != 0 {
		t.Fatalf("path resolution calls current=%d readonly=%d writable=%d, want 0/1/0", paths.currentCalls, paths.readonlyCalls, paths.writableCalls)
	}
	if len(fc.PeerFolderUpdates) != 1 || fc.PeerFolderUpdates[0] != (client.PeerFolderReq{ChatID: 1, FolderID: 1}) {
		t.Fatalf("peer folder updates=%#v", fc.PeerFolderUpdates)
	}
	if idempotencyCount(t, alpha.db, "account-archive") != 1 || idempotencyCount(t, beta.db, "account-archive") != 0 {
		t.Fatal("archive idempotency state leaked across accounts")
	}
}

func TestArchiveCancellationBeforeCommitIsReportedWithoutSuccess(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.PeerFolderErr = context.Canceled
	out, code := runRoot(t, cfg, "archive", "1", "--allow-write", "--json")
	if code == 0 || strings.Contains(out, `"ok":true`) || len(fc.PeerFolderUpdates) != 1 {
		t.Fatalf("code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
}

func TestArchivePersistenceFailureReportsCommittedOutcome(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER fail_peer_folder_finalize BEFORE UPDATE OF result_json ON tg_idempotency BEGIN SELECT RAISE(FAIL, 'injected finalization failure'); END"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	out, code := runRoot(t, cfg, "archive", "1", "--allow-write", "--idempotency-key", "archive-failure", "--json")
	if code == 0 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"folder_id":1`) || len(fc.PeerFolderUpdates) != 1 {
		t.Fatalf("code=%d output=%s updates=%#v", code, out, fc.PeerFolderUpdates)
	}
	db, err = store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var resultJSON string
	if err := db.QueryRow("SELECT result_json FROM tg_idempotency WHERE key='archive-failure'").Scan(&resultJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultJSON, `"pending":true`) {
		t.Fatalf("failed finalization did not retain the unresolved reservation: %s", resultJSON)
	}
}
