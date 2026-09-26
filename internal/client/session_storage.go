package client

import (
	"context"
	"errors"
	"github.com/gotd/td/session"
	"os"
	"path/filepath"
	"sync"
)

// AtomicSessionStorage publishes a complete, private session or preserves the
// previous one. Callers must own the associated SessionLock for its lifetime.
type AtomicSessionStorage struct {
	Path     string
	mu       sync.Mutex
	logoutMu sync.Mutex // serializes logout finalization with releasing session ownership
	cleared  bool
}

func (s *AtomicSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleared {
		return nil, session.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, session.ErrNotFound
	}
	return b, err
}
func (s *AtomicSessionStorage) StoreSession(ctx context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleared {
		return errors.New("session was logged out")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path := s.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return publishSession(f.Name(), path)
}

// Clear runs after confirmed remote logout while the session lock is owned.
// Late transport flushes cannot recreate a revoked credential.
func (s *AtomicSessionStorage) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleared = true
	path, err := filepath.EvalSymlinks(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
