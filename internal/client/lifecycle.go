package client

import (
	"context"
	"errors"
)

// clientLifecycle publishes completion by closing done, so startup, listening
// and repeated Close calls can all observe the same terminal result.
type clientLifecycle struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error // written before done closes; read only after receiving done
}

func startClientRun(ctx context.Context, run func(context.Context, chan<- error) error) (*clientLifecycle, error) {
	runCtx, cancel := context.WithCancel(ctx)
	life := &clientLifecycle{cancel: cancel, done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		defer close(life.done)
		defer cancel()
		life.err = run(runCtx, ready)
	}()
	select {
	case err := <-ready:
		if err != nil {
			cancel()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			cancel()
			return nil, err
		}
		return life, nil
	case <-life.done:
		return nil, life.terminalError()
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

func (l *clientLifecycle) terminalError() error {
	if l.err != nil {
		return l.err
	}
	return errors.New("Telegram client stopped")
}

func (l *clientLifecycle) close() error {
	l.cancel()
	<-l.done
	if errors.Is(l.err, context.Canceled) {
		return nil
	}
	return l.err
}
