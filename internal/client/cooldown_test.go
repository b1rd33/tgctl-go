package client

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/tgerr"
	"path/filepath"
	"testing"
)

func TestCooldownSurvivesReopenAndIsScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordCooldown(db, "peer:7", tgerr.New(420, "SLOWMODE_WAIT_60")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var wait *safety.FloodWait
	if !errors.As(checkCooldown(context.Background(), db, "peer:7"), &wait) {
		t.Fatal("peer cooldown lost")
	}
	if err := checkCooldown(context.Background(), db, "peer:8"); err != nil {
		t.Fatal(err)
	}
	if err := recordCooldown(db, "peer:7", tgerr.New(420, "FLOOD_WAIT_60")); err != nil {
		t.Fatal(err)
	}
	if !errors.As(checkCooldown(context.Background(), db, "peer:8"), &wait) {
		t.Fatal("account cooldown lost")
	}
}
func TestWriteWindowSharedAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	first, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for i := 0; i < 20; i++ {
		if err := reserveWriteWindow(context.Background(), first); err != nil {
			t.Fatal(err)
		}
	}
	var limited *safety.LocalRateLimited
	if !errors.As(reserveWriteWindow(context.Background(), second), &limited) {
		t.Fatal("second connection bypassed account limit")
	}
}
