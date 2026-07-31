package accounts

import (
	"errors"
	"os"
	"path/filepath"
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
	wantDB := filepath.Join(dir, "accounts", "alice", "telegram.sqlite")
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
	wantDir := filepath.Join(dir, "accounts", "alice")
	if p.AccountDir != wantDir {
		t.Fatalf("AccountDir = %q, want %q", p.AccountDir, wantDir)
	}
	if _, err := os.Stat(wantDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Paths created account directory; Stat err = %v", err)
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
