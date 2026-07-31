package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func TestBackfillEntitiesReadOnlyWinsBeforeCredentialsAndPaths(t *testing.T) {
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerBackfillEntities(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "backfill-entities", "--allow-write", "--json"})
	if code := ExecuteRoot(root); code != 6 {
		t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestAccountMutationsRejectReadOnlyWithoutFilesystemChanges(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		rootDir := t.TempDir()
		mgr := accounts.New(rootDir)
		root := NewRootCommand()
		registerAccountCommands(root, mgr)
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"--read-only", "accounts-add", "work", "--json"})
		if code := ExecuteRoot(root); code != 6 {
			t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
		}
		assertPathMissing(t, filepath.Join(rootDir, "accounts"))
	})

	t.Run("use", func(t *testing.T) {
		rootDir := t.TempDir()
		mgr := accounts.New(rootDir)
		if _, err := mgr.Add("work"); err != nil {
			t.Fatal(err)
		}
		root := NewRootCommand()
		registerAccountCommands(root, mgr)
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"--read-only", "accounts-use", "work", "--json"})
		if code := ExecuteRoot(root); code != 6 {
			t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
		}
		assertPathMissing(t, filepath.Join(rootDir, "accounts", accounts.CurrentFile))
	})

	t.Run("remove", func(t *testing.T) {
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
		root.SetArgs([]string{"--read-only", "accounts-remove", "work", "--confirm", "work", "--json"})
		if code := ExecuteRoot(root); code != 6 {
			t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
		}
		if _, err := os.Stat(accountDir); err != nil {
			t.Fatalf("account directory changed: %v", err)
		}
	})
}

func TestLoginReadOnlyWinsBeforeCredentialsAndPaths(t *testing.T) {
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerLogin(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "login", "--json"})
	if code := ExecuteRoot(root); code != 6 {
		t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestImportTelethonReadOnlyWinsBeforeSourceLookupAndPaths(t *testing.T) {
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerImportTelethon(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "import-telethon-session", filepath.Join(rootDir, "missing.session"), "--json"})
	if code := ExecuteRoot(root); code != 6 {
		t.Fatalf("exit code = %d, want WRITE_DISALLOWED=6", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestDoctorReadOnlyDoesNotCreateAccountPaths(t *testing.T) {
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerDoctor(root, mgr)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "doctor", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestMeLiveReadOnlyFetchesWithoutChangingCache(t *testing.T) {
	dbPath, sessionPath := setupCacheDB(t)
	before, err := MeOfflineRunner(context.Background(), dbPath, sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	fetched := client.User{ID: 123, Username: "live", DisplayName: "Live User"}
	data, err := meLiveRunner(context.Background(), dbPath, sessionPath, false, func(context.Context, string, bool) (client.User, error) {
		return fetched, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := data.(map[string]any)["user_id"]; got != int64(123) {
		t.Fatalf("live user_id = %v, want 123", got)
	}
	after, err := MeOfflineRunner(context.Background(), dbPath, sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.(map[string]any)["user_id"], before.(map[string]any)["user_id"]; got != want {
		t.Fatalf("offline cache changed: got user_id %v, want %v", got, want)
	}
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_me WHERE user_id = ?", fetched.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fetched user was cached in read-only mode")
	}
}

func TestMeCommandReadOnlyUsesNoCacheOrAuditWrites(t *testing.T) {
	dbPath, sessionPath := setupCacheDB(t)
	auditPath := filepath.Join(filepath.Dir(dbPath), "audit.log")
	fetchCalls := 0
	gotReadOnly := false
	root := NewRootCommand()
	registerAuthWithFetcher(root, stubPaths{db: dbPath, session: sessionPath, audit: auditPath}, func(_ context.Context, _ string, readOnly bool) (client.User, error) {
		fetchCalls++
		gotReadOnly = readOnly
		return client.User{ID: 123, Username: "live", DisplayName: "Live User"}, nil
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "me", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d, want 1", fetchCalls)
	}
	if !gotReadOnly {
		t.Fatal("production fetch was not told to use read-only session storage")
	}
	offline, err := MeOfflineRunner(context.Background(), dbPath, sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := offline.(map[string]any)["user_id"]; got != int64(99) {
		t.Fatalf("offline cache changed: user_id = %v, want 99", got)
	}
	assertPathMissing(t, auditPath)
}

func TestMeCommandReadOnlyDoesNotCreateAccountOrSessionPaths(t *testing.T) {
	rootDir := t.TempDir()
	mgr := accounts.New(rootDir)
	root := NewRootCommand()
	registerAuthWithFetcher(root, mgr, func(context.Context, string, bool) (client.User, error) {
		return client.User{ID: 123, DisplayName: "Live User"}, nil
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--read-only", "me", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	assertPathMissing(t, filepath.Join(rootDir, "accounts"))
}

func TestMeCommandWritableUsesPersistentSessionMode(t *testing.T) {
	dbPath, sessionPath := setupCacheDB(t)
	readOnlyMode := true
	root := NewRootCommand()
	registerAuthWithFetcher(root, stubPaths{db: dbPath, session: sessionPath, audit: filepath.Join(filepath.Dir(dbPath), "audit.log")}, func(_ context.Context, _ string, readOnly bool) (client.User, error) {
		readOnlyMode = readOnly
		return client.User{ID: 123, DisplayName: "Live User"}, nil
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"me", "--json"})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if readOnlyMode {
		t.Fatal("normal live me unexpectedly used read-only session mode")
	}
}
