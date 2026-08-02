package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestValidateNameRejectsBadCharacters(t *testing.T) {
	bad := []string{"", "../escape", "with?mark", "with/slash", "with\\back", "_leading", "-leading", ".dot", "with.dot", "spaces are bad"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) accepted, want error", n)
		}
	}
}

func TestValidateNameAcceptsGood(t *testing.T) {
	good := []string{"default", "work", "a", "A", "0", "alice_2", "team-1"}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
}

func TestAddAccountCreatesDirAndMedia(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	d, err := m.Add("work")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "media")); err != nil {
		t.Fatalf("media not created: %v", err)
	}
}

func TestListAccountsSortedAndFiltered(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	for _, n := range []string{"zeta", "alpha", "beta"} {
		if _, err := m.Add(n); err != nil {
			t.Fatalf("Add %s: %v", n, err)
		}
	}
	// Drop a junk file at accounts root to ensure it gets filtered out.
	_ = os.WriteFile(filepath.Join(dir, "accounts", ".DS_Store"), []byte{}, 0o600)
	got, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "beta", "zeta"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestUseAndCurrent(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if _, err := m.Add("work"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Use("work"); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if got := m.Current(); got != "work" {
		t.Fatalf("Current = %q, want work", got)
	}
}

func TestConcurrentUseReadersNeverObserveDefaultOrMalformedSelector(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	for _, name := range []string{"a", "b"} {
		if _, err := m.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Use("a"); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 16)
	var wg sync.WaitGroup
	for writer := 0; writer < 4; writer++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for i := 0; i < 250; i++ {
				name := []string{"a", "b"}[(i+offset)%2]
				if err := m.Use(name); err != nil {
					errCh <- err
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 4000; i++ {
				got := m.Current()
				if got != "a" && got != "b" {
					errCh <- errors.New("observed torn selector: " + got)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	// Windows does not expose POSIX permission bits through os.FileMode; the
	// selector is protected by the inherited ACL of the private test root.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, AccountsDirName, CurrentFile))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("current selector mode=%#o want 0600", got)
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, AccountsDirName, ".current.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary selectors leaked: %v", matches)
	}
}

func TestUseUnknownReturnsAccountNotFound(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	err := m.Use("missing")
	var anf *AccountNotFound
	if !errors.As(err, &anf) {
		t.Fatalf("err = %v, want *AccountNotFound", err)
	}
}

func TestRemoveCurrentResetsSelector(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	_, _ = m.Add("work")
	_ = m.Use("work")
	if err := m.Remove("work"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := m.Current(); got != "default" {
		t.Fatalf("Current after remove = %q, want default", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, AccountsDirName, CurrentFile))
	if err != nil {
		t.Fatalf("read reset selector: %v", err)
	}
	if string(b) != DefaultAccount {
		t.Fatalf("reset selector=%q want %q", b, DefaultAccount)
	}
}

func TestCurrentDefaultsWhenNoSelector(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if got := m.Current(); got != "default" {
		t.Fatalf("Current = %q, want default", got)
	}
}

func TestResolvePathsCreatesAccountDir(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	p, err := m.ResolvePaths("alice")
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if _, err := os.Stat(p.AccountDir); err != nil {
		t.Fatalf("dir missing: %v", err)
	}
	wantDB := filepath.Join(m.Root, "accounts", "alice", "telegram.sqlite")
	if p.DBPath != wantDB {
		t.Fatalf("DBPath = %q, want %q", p.DBPath, wantDB)
	}
}

func TestPathsDoesNotCreateAccountDir(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	p, err := m.Paths("alice")
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	wantDir := filepath.Join(m.Root, "accounts", "alice")
	if p.AccountDir != wantDir {
		t.Fatalf("AccountDir = %q, want %q", p.AccountDir, wantDir)
	}
	if _, err := os.Stat(wantDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Paths created account directory; Stat err = %v", err)
	}
}

func TestAccountPathsPropagatesInvalidName(t *testing.T) {
	m := New(t.TempDir())
	_, _, _, err := m.AccountPaths("bad/name")
	if err == nil {
		t.Fatal("AccountPaths accepted invalid account name")
	}
}

func TestMigrationMovesRootLevelFiles(t *testing.T) {
	dir := t.TempDir()
	// Seed root-level files.
	_ = os.WriteFile(filepath.Join(dir, "telegram.sqlite"), []byte("db"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "tg.session"), []byte("ses"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "audit.log"), []byte("audit"), 0o600)

	m := New(dir)
	moved, err := m.MaybeMigrateDefaultFromRoot()
	if err != nil {
		t.Fatalf("migration: %v", err)
	}
	if !moved {
		t.Fatalf("migration reported no move")
	}
	if _, err := os.Stat(filepath.Join(dir, "accounts", "default", "telegram.sqlite")); err != nil {
		t.Fatalf("DB not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "telegram.sqlite")); err == nil {
		t.Fatalf("source DB still exists after move")
	}
}

func TestMigrationNoOpWhenDefaultExists(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	_, _ = m.Add("default")
	_ = os.WriteFile(filepath.Join(dir, "telegram.sqlite"), []byte("db"), 0o600)
	moved, err := m.MaybeMigrateDefaultFromRoot()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if moved {
		t.Fatalf("expected no move when default already exists")
	}
}
