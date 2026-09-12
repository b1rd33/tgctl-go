package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientStartupReturnsEarlyFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := errors.New("transport failed before ready")
	_, err := startClientRun(ctx, func(context.Context, chan<- error) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestClientStartupCancellationStopsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	cancel()
	_, err := startClientRun(ctx, func(ctx context.Context, _ chan<- error) error {
		defer close(stopped)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
}

func TestClientCompletionRemainsObservable(t *testing.T) {
	want := errors.New("transport terminated")
	finish := make(chan struct{})
	life, err := startClientRun(context.Background(), func(ctx context.Context, ready chan<- error) error {
		ready <- nil
		<-finish
		return want
	})
	if err != nil {
		t.Fatal(err)
	}
	close(finish)
	<-life.done
	for i := 0; i < 2; i++ {
		if !errors.Is(life.terminalError(), want) {
			t.Fatal("terminal result lost")
		}
		if !errors.Is(life.close(), want) {
			t.Fatal("close result lost")
		}
	}
}

func TestListenObservesTerminationAndCloseIsRepeatable(t *testing.T) {
	want := errors.New("connection stopped")
	done := make(chan struct{})
	close(done)
	g := &GotdClient{events: make(chan ListenEvent), lifecycle: &clientLifecycle{
		cancel: func() {}, done: done, err: want,
	}}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := g.ListenOnce(ctx)
		cancel()
		if !errors.Is(err, want) {
			t.Fatalf("listen: %v", err)
		}
		if !errors.Is(g.Close(), want) {
			t.Fatal("close lost error")
		}
	}
}
