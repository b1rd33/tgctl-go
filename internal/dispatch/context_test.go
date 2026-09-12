package dispatch

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestRunPreservesParentContext(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), key{}, "value"), time.Minute)
	defer cancel()
	code := Run("test", Options{Context: parent, Stdout: io.Discard, Stderr: io.Discard}, func(ctx context.Context) (any, error) {
		if ctx.Value(key{}) != "value" || RequestIDFrom(ctx) == "" {
			t.Fatal("context values lost")
		}
		deadline, ok := ctx.Deadline()
		want, _ := parent.Deadline()
		if !ok || !deadline.Equal(want) {
			t.Fatal("deadline lost")
		}
		cancel()
		if ctx.Err() != context.Canceled {
			t.Fatal("cancellation lost")
		}
		return nil, ctx.Err()
	})
	if code == 0 {
		t.Fatal("cancellation reported success")
	}
}

func TestRunDoesNotStartCanceledOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code := Run("test", Options{Context: ctx, Stdout: io.Discard, Stderr: io.Discard}, func(context.Context) (any, error) {
		t.Fatal("canceled operation started")
		return nil, nil
	})
	if code == 0 {
		t.Fatal("cancellation reported success")
	}
}
