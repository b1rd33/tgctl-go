package client

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (g *GotdClient) listenDurable(ctx context.Context) (ListenEvent, error) {
	g.listenMu.Lock()
	defer g.listenMu.Unlock()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return ListenEvent{}, err
		}
		if err := g.updateStore.err(); err != nil {
			return ListenEvent{}, err
		}
		var id int64
		var encoded string
		err := g.db.QueryRowContext(ctx, "SELECT id,event FROM tg_event_outbox ORDER BY id LIMIT 1").Scan(&id, &encoded)
		if err == nil {
			var e ListenEvent
			if err := json.Unmarshal([]byte(encoded), &e); err != nil {
				return ListenEvent{}, err
			}
			e.EventID = id
			g.lastEvent = id
			return e, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return ListenEvent{}, err
		}
		var done <-chan struct{}
		if g.lifecycle != nil {
			done = g.lifecycle.done
		}
		select {
		case <-ctx.Done():
			return ListenEvent{}, ctx.Err()
		case <-done:
			return ListenEvent{}, g.lifecycle.terminalError()
		case <-g.updateStore.failed:
			return ListenEvent{}, g.updateStore.err()
		case <-ticker.C:
		}
	}
}

// AcknowledgeEvent removes only the event delivered to this consumer. Call after
// successful output/persistence; a crash before acknowledgment permits replay.
func (g *GotdClient) AcknowledgeEvent(ctx context.Context, id int64) error {
	g.listenMu.Lock()
	defer g.listenMu.Unlock()
	if id == 0 {
		return nil
	}
	if id != g.lastEvent {
		return errors.New("cannot acknowledge an event not delivered to this consumer")
	}
	if _, err := g.db.ExecContext(ctx, "DELETE FROM tg_event_outbox WHERE id=?", id); err != nil {
		return err
	}
	g.lastEvent = 0
	return nil
}

// AcknowledgeListenEvent also supports clients without a durable outbox.
func AcknowledgeListenEvent(ctx context.Context, c Client, e ListenEvent) error {
	if a, ok := c.(interface {
		AcknowledgeEvent(context.Context, int64) error
	}); ok {
		return a.AcknowledgeEvent(ctx, e.EventID)
	}
	return nil
}
