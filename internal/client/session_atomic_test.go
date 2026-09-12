package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicSessionCancellationPreservesPrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	s := &AtomicSessionStorage{Path: path}
	if err := s.StoreSession(context.Background(), []byte("old")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.StoreSession(ctx, []byte("new")); err == nil {
		t.Fatal("canceled write succeeded")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "old" {
		t.Fatal("previous session lost")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary session leaked")
	}
}
