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

func TestCanceledUploadBeforeSendCanRetry(t *testing.T) {
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	in := PipelineInput{Cmd: "upload-photo", Args: Args{Args: safety.Args{AllowWrite: true}, IdempotencyKey: "key"}, ConfirmedTarget: &ConfirmedTarget{ChatID: 1}, PayloadPreview: map[string]any{"media_type": "photo"}, Run: func(context.Context, int64, string) (map[string]any, error) {
		calls++
		if calls == 1 {
			return nil, &safety.DefinitiveRejection{Err: context.Canceled}
		}
		return map[string]any{"message_id": 7}, nil
	}}
	if _, err := Run(context.Background(), db, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v, want context.Canceled", err)
	}
	if _, err := Run(context.Background(), db, in); err != nil {
		t.Fatalf("safe retry failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
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
