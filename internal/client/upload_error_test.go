package client

import (
	"context"
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func TestNoMessageSentMakesCancellationDefinitive(t *testing.T) {
	err := noMessageSent(context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	var rejected *safety.DefinitiveRejection
	if !errors.As(err, &rejected) {
		t.Fatalf("error = %T, want *safety.DefinitiveRejection", err)
	}
}
