package writes

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"path/filepath"
	"testing"
)

func TestOrdinaryWriteUnknownOutcomeCannotResend(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	in := PipelineInput{Cmd: "send", Args: Args{Args: safety.Args{AllowWrite: true}, IdempotencyKey: "key"}, ConfirmedTarget: &ConfirmedTarget{ChatID: 1}, PayloadPreview: map[string]any{"text": "synthetic"}, Run: func(context.Context, int64, string) (map[string]any, error) {
		calls++
		return nil, errors.New("connection lost after send")
	}}
	for i := 0; i < 2; i++ {
		if _, err := Run(context.Background(), db, in); err == nil {
			t.Fatal("unknown operation accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("resent %d times", calls)
	}
}
func TestOrdinaryWriteChangedPayloadIsRejected(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	in := PipelineInput{Cmd: "send", Args: Args{Args: safety.Args{AllowWrite: true}, IdempotencyKey: "key"}, ConfirmedTarget: &ConfirmedTarget{ChatID: 1}, PayloadPreview: map[string]any{"text": "first"}, Run: func(context.Context, int64, string) (map[string]any, error) {
		calls++
		return map[string]any{"message_id": 7}, nil
	}}
	if _, err := Run(context.Background(), db, in); err != nil {
		t.Fatal(err)
	}
	in.PayloadPreview["text"] = "changed"
	if _, err := Run(context.Background(), db, in); err == nil {
		t.Fatal("changed payload replayed")
	}
	if calls != 1 {
		t.Fatal("changed request sent")
	}
}
