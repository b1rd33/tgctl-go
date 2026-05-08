// Package accounts manages per-account directory layout and selection.
//
// Each account lives at ROOT/accounts/<NAME>/ with isolated tg.session,
// telegram.sqlite, audit.log, and media/. The current account selector is
// ROOT/accounts/.current containing just the account name. Mirrors
// tgcli.accounts in the Python reference.
package accounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

const (
	AccountsDirName = "accounts"
	CurrentFile     = ".current"
	DefaultAccount  = "default"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// AccountNotFound is returned when an account name does not correspond to an
// existing directory.
type AccountNotFound struct{ Name string }

func (e *AccountNotFound) Error() string { return fmt.Sprintf("account %q does not exist", e.Name) }

// Manager binds the accounts surface to a concrete root directory. The root
// is the project root (the directory that holds `accounts/`).
type Manager struct {
	Root string
}

// New returns a Manager pinned to the given root directory.
func New(root string) *Manager { return &Manager{Root: root} }

func (m *Manager) accountsRoot() string { return filepath.Join(m.Root, AccountsDirName) }

func (m *Manager) currentPath() string { return filepath.Join(m.accountsRoot(), CurrentFile) }

// ValidateName returns nil if name matches the public character class.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return safety.NewBadArgs(
			"account name %q invalid; must match [A-Za-z0-9][A-Za-z0-9_-]{0,63}", name,
		)
	}
	return nil
}

// AccountDir returns the path to <root>/accounts/<name>, creating the
// directory and its `media/` subdirectory when create=true.
func (m *Manager) AccountDir(name string, create bool) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	d := filepath.Join(m.accountsRoot(), name)
	if create {
		if err := os.MkdirAll(filepath.Join(d, "media"), 0o700); err != nil {
			return "", err
		}
	}
	return d, nil
}

// Add creates the account directory and returns its path.
func (m *Manager) Add(name string) (string, error) {
	return m.AccountDir(name, true)
}

// List returns every account dir under <root>/accounts/, sorted by name.
func (m *Manager) List() ([]string, error) {
	root := m.accountsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && nameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Current returns the currently selected account, falling back to "default".
func (m *Manager) Current() string {
	b, err := os.ReadFile(m.currentPath())
	if err != nil {
		return DefaultAccount
	}
	name := strings.TrimSpace(string(b))
	if !nameRE.MatchString(name) {
		return DefaultAccount
	}
	d, _ := m.AccountDir(name, false)
	if _, err := os.Stat(d); err != nil {
		return DefaultAccount
	}
	return name
}

// Use selects an existing account.
func (m *Manager) Use(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	d, _ := m.AccountDir(name, false)
	if _, err := os.Stat(d); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &AccountNotFound{Name: name}
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.currentPath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(m.currentPath(), []byte(name), 0o600)
}

// Remove deletes the account directory; if it was the current account, the
// `.current` selector is cleared too.
func (m *Manager) Remove(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	d, _ := m.AccountDir(name, false)
	if _, err := os.Stat(d); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &AccountNotFound{Name: name}
		}
		return err
	}
	if name == m.Current() {
		_ = os.Remove(m.currentPath())
	}
	return os.RemoveAll(d)
}

// Paths bundles the per-account paths every command resolves.
type Paths struct {
	AccountDir  string
	DBPath      string
	SessionPath string
	AuditPath   string
	MediaDir    string
}

// ResolvePaths returns the per-account paths, creating the directory tree
// when necessary.
func (m *Manager) ResolvePaths(name string) (Paths, error) {
	d, err := m.AccountDir(name, true)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		AccountDir:  d,
		DBPath:      filepath.Join(d, "telegram.sqlite"),
		SessionPath: filepath.Join(d, "tg.session"),
		AuditPath:   filepath.Join(d, "audit.log"),
		MediaDir:    filepath.Join(d, "media"),
	}, nil
}

// AccountPaths satisfies the AuthPathProvider contract used by the commands
// package: returns (db, session, audit) tuple.
func (m *Manager) AccountPaths(name string) (string, string, string) {
	p, _ := m.ResolvePaths(name)
	return p.DBPath, p.SessionPath, p.AuditPath
}

// MaybeMigrateDefaultFromRoot performs the one-time migration of root-level
// telegram.sqlite/tg.session/audit.log/media into accounts/default/.
// Returns true when files were moved.
func (m *Manager) MaybeMigrateDefaultFromRoot() (bool, error) {
	defaultDir := filepath.Join(m.accountsRoot(), DefaultAccount)
	if _, err := os.Stat(defaultDir); err == nil {
		return false, nil
	}
	srcs := []string{
		filepath.Join(m.Root, "telegram.sqlite"),
		filepath.Join(m.Root, "tg.session"),
		filepath.Join(m.Root, "audit.log"),
	}
	anyExists := false
	for _, s := range srcs {
		if _, err := os.Stat(s); err == nil {
			anyExists = true
			break
		}
	}
	srcMedia := filepath.Join(m.Root, "media")
	if !anyExists {
		if info, err := os.Stat(srcMedia); err != nil || !info.IsDir() {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Join(defaultDir, "media"), 0o700); err != nil {
		return false, err
	}
	moved := false
	for _, src := range srcs {
		if _, err := os.Stat(src); err == nil {
			dst := filepath.Join(defaultDir, filepath.Base(src))
			if err := os.Rename(src, dst); err == nil {
				moved = true
			}
		}
	}
	srcLock := filepath.Join(m.Root, "tg.session.lock")
	if _, err := os.Stat(srcLock); err == nil {
		_ = os.Rename(srcLock, filepath.Join(defaultDir, "tg.session.lock"))
	}
	if entries, err := os.ReadDir(srcMedia); err == nil {
		for _, e := range entries {
			_ = os.Rename(filepath.Join(srcMedia, e.Name()), filepath.Join(defaultDir, "media", e.Name()))
			moved = true
		}
		_ = os.Remove(srcMedia)
	}
	return moved, nil
}
