package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func eventDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	db, err := Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, raw := range []string{`{"text":"first"}`, `{"text":"second"}`, `{"text":"third"}`} {
		if _, err := db.Exec("INSERT INTO tg_event_outbox(event) VALUES(?)", raw); err != nil {
			t.Fatal(err)
		}
	}
	return db, EventScope(path, "work")
}

func TestPendingEventsReplayAndSelectiveAck(t *testing.T) {
	db, scope := eventDB(t)
	ctx := context.Background()
	first, more, err := PendingEvents(ctx, db, scope, 2)
	if err != nil || !more || len(first) != 2 {
		t.Fatalf("list: %v %v %v", first, more, err)
	}
	again, _, err := PendingEvents(ctx, db, scope, 2)
	if err != nil || first[0].Receipt != again[0].Receipt || first[1].EventID <= first[0].EventID {
		t.Fatal("unstable replay/order", err)
	}
	id, pending, err := AcknowledgePendingEvent(ctx, db, scope, first[1].Receipt, true)
	if err != nil || !pending || id != first[1].EventID {
		t.Fatal("preview", err)
	}
	rows, _, _ := PendingEvents(ctx, db, scope, 100)
	if len(rows) != 3 {
		t.Fatal("preview consumed an event")
	}
	for attempt := 0; attempt < 2; attempt++ {
		id, removed, err := AcknowledgePendingEvent(ctx, db, scope, first[1].Receipt, false)
		if err != nil || id != first[1].EventID || removed != (attempt == 0) {
			t.Fatalf("ack: %d %v %v", id, removed, err)
		}
	}
	rows, more, err = PendingEvents(ctx, db, scope, 100)
	if err != nil || more || len(rows) != 2 || rows[0].EventID != first[0].EventID || rows[1].EventID != 3 {
		t.Fatal("ack removed unrelated events", rows, err)
	}
}

func TestEventReceiptIsolationAndReplacement(t *testing.T) {
	db, scope := eventDB(t)
	ctx := context.Background()
	rows, _, _ := PendingEvents(ctx, db, scope, 1)
	for _, token := range []string{"", "invalid", strings.Repeat("x", 1025)} {
		if _, _, err := AcknowledgePendingEvent(ctx, db, scope, token, false); err == nil {
			t.Fatal("accepted invalid receipt")
		}
	}
	for _, other := range []string{EventScope("other.sqlite", "work"), EventScope("events.sqlite", "other")} {
		if _, _, err := AcknowledgePendingEvent(ctx, db, other, rows[0].Receipt, false); err == nil {
			t.Fatal("accepted wrong scope")
		}
	}
	if _, err := db.Exec("UPDATE tg_event_outbox SET event=? WHERE id=1", `{"text":"replacement"}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AcknowledgePendingEvent(ctx, db, scope, rows[0].Receipt, false); err == nil {
		t.Fatal("acknowledged replacement")
	}
	after, _, err := PendingEvents(ctx, db, scope, 100)
	if err != nil || len(after) != 3 {
		t.Fatal("invalid receipts changed queue", err)
	}
}

func TestPendingEventsBoundsAndCancellation(t *testing.T) {
	db, scope := eventDB(t)
	for _, limit := range []int{0, -1, 101} {
		if _, _, err := PendingEvents(context.Background(), db, scope, limit); err == nil {
			t.Fatal("unbounded query")
		}
	}
	receipt, err := PendingEventReceipt(context.Background(), db, scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := PendingEvents(ctx, db, scope, 1); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, _, err := AcknowledgePendingEvent(ctx, db, scope, receipt, false); err == nil {
		t.Fatal("ignored ack cancellation")
	}
	for _, raw := range []string{`broken json`, `"` + strings.Repeat("x", 262145) + `"`} {
		if _, err := db.Exec("UPDATE tg_event_outbox SET event=? WHERE id=1", raw); err != nil {
			t.Fatal(err)
		}
		if _, _, err := PendingEvents(context.Background(), db, scope, 1); err == nil {
			t.Fatal("accepted corrupt/oversized row")
		}
		if _, err := PendingEventReceipt(context.Background(), db, scope, 1); err == nil {
			t.Fatal("issued receipt for corrupt row")
		}
	}
}

func TestConcurrentEventAcknowledgement(t *testing.T) {
	db, scope := eventDB(t)
	receipt, err := PendingEventReceipt(context.Background(), db, scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	removed := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, didRemove, err := AcknowledgePendingEvent(context.Background(), db, scope, receipt, false)
			if err != nil {
				t.Error(err)
			}
			removed <- didRemove
		}()
	}
	wg.Wait()
	close(removed)
	count := 0
	for v := range removed {
		if v {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("removal count %d", count)
	}
}
