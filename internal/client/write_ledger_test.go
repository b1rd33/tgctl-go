package client

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"os"
	"path/filepath"
	"testing"
)

type invokeFunc func(context.Context, bin.Encoder, bin.Decoder) error

func (f invokeFunc) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	return f(ctx, in, out)
}
func TestWriteLedgerRecordsExactRequestBeforeRPC(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inv := &writeLedgerInvoker{db: db, owner: "synthetic", next: invokeFunc(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		var state string
		var request []byte
		if err := db.QueryRow("SELECT state,request FROM tg_write_calls").Scan(&state, &request); err != nil {
			t.Fatal(err)
		}
		var decoded tg.MessagesSendMessageRequest
		if err := decoded.Decode(&bin.Buffer{Buf: request}); err != nil {
			t.Fatal(err)
		}
		if state != "prepared" || decoded.RandomID != 71 {
			t.Fatal("request was not frozen before RPC")
		}
		return errors.New("response lost")
	})}
	req := &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerSelf{}, RandomID: 71, Message: "synthetic"}
	if err := inv.Invoke(context.Background(), req, nil); err == nil {
		t.Fatal("unknown outcome reported success")
	}
	var state string
	if err := db.QueryRow("SELECT state FROM tg_write_calls").Scan(&state); err != nil || state != "unknown" {
		t.Fatalf("state=%s err=%v", state, err)
	}
}
func TestLedgerFailurePreventsRPC(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	inv := &writeLedgerInvoker{db: db, next: invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Fatal("RPC called without ledger")
		return nil
	})}
	if err := inv.Invoke(context.Background(), &tg.MessagesSendMessageRequest{Peer: &tg.InputPeerSelf{}, RandomID: 1}, nil); err == nil {
		t.Fatal("closed ledger accepted")
	}
}

func TestUploadChangedAfterFingerprintDoesNotContactTelegram(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := safety.WithFileDigests(context.Background(), map[string]string{path: fmt.Sprintf("%x", sha256.Sum256([]byte("original")))})
	api := tg.NewClient(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Fatal("upload called after fingerprint mismatch")
		return nil
	}))
	if _, err := uploadSnapshot(ctx, api, path); err == nil {
		t.Fatal("changed upload accepted")
	}
}
