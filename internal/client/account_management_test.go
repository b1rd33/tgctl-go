package client

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLogoutOnlyClearsSessionAfterConfirmedRPC(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "unknown"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session")
			storage := &AtomicSessionStorage{Path: path}
			if err := storage.StoreSession(context.Background(), []byte("synthetic")); err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(filepath.Dir(path), "telegram.sqlite")
			os.WriteFile(sibling, []byte("keep"), 0600)
			g := &GotdClient{sessionStorage: storage, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				if storage.logoutMu.TryLock() {
					storage.logoutMu.Unlock()
					t.Fatal("session ownership can be released during logout")
				}
				if _, ok := in.(*tg.AuthLogOutRequest); !ok {
					t.Fatalf("request=%T", in)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("removed before RPC")
				}
				if fail {
					return context.DeadlineExceeded
				}
				return nil
			}))}
			err := g.Logout(context.Background())
			if fail {
				if err == nil {
					t.Fatal("unknown accepted")
				}
				if _, e := os.Stat(path); e != nil {
					t.Fatal("unknown removed session")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if storage.StoreSession(context.Background(), []byte("late")) == nil {
						t.Error("late flush succeeded")
					}
				}()
			}
			wg.Wait()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("session resurrected")
			}
			if b, err := os.ReadFile(sibling); err != nil || string(b) != "keep" {
				t.Fatal("cache removed")
			}
		})
	}
}

func TestLogoutLocalCleanupFailureIsCommitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	os.Mkdir(path, 0700)
	os.WriteFile(filepath.Join(path, "child"), nil, 0600)
	g := &GotdClient{sessionStorage: &AtomicSessionStorage{Path: path}, api: tg.NewClient(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return nil }))}
	var committed *safety.CommittedWrite
	if err := g.Logout(context.Background()); !errors.As(err, &committed) {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestContactAdapterScopesUserAndPhoneSharing(t *testing.T) {
	for _, share := range []bool{false, true} {
		db := updateTestDB(t)
		if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
			t.Fatal(err)
		}
		calls := 0
		g := &GotdClient{db: db, selfID: 99, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			calls++
			switch req := in.(type) {
			case *tg.ContactsAddContactRequest:
				u := req.ID.(*tg.InputUser)
				if u.UserID != 7 || u.AccessHash != 70 || req.Phone != "" || req.FirstName != "Test" || req.AddPhonePrivacyException != share {
					t.Fatalf("wrong add: %#v", req)
				}
			case *tg.ContactsDeleteContactsRequest:
				if len(req.ID) != 1 || req.ID[0].(*tg.InputUser).UserID != 7 {
					t.Fatal("wrong remove")
				}
			default:
				t.Fatalf("unexpected request %T", in)
			}
			out.(*tg.UpdatesBox).Updates = &tg.Updates{}
			return nil
		}))}
		if err := g.AddContact(context.Background(), AddContactReq{UserID: 7, FirstName: "Test", SharePhone: share}); err != nil {
			t.Fatal(err)
		}
		if err := g.RemoveContact(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{0, -7, 99} {
			if err := g.RemoveContact(context.Background(), id); err == nil {
				t.Fatal("invalid contact accepted")
			}
		}
		if err := g.AddContact(context.Background(), AddContactReq{UserID: 7}); err == nil {
			t.Fatal("empty name accepted")
		}
		if calls != 2 {
			t.Fatalf("calls=%d", calls)
		}
	}
}
