package client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/tg"
)

func TestExpiringUpdatePurgesPendingPayloadAndBlocksStaleReplay(t *testing.T) {
	cases := []struct {
		name  string
		peer  tg.PeerClass
		chat  int64
		media bool
	}{
		{"user TTL", &tg.PeerUser{UserID: 7}, 7, false},
		{"group TTL", &tg.PeerChat{ChatID: 7}, peerid.Chat(7), false},
		{"channel TTL", &tg.PeerChannel{ChannelID: 7}, peerid.Channel(7), false},
		{"photo TTL", &tg.PeerUser{UserID: 7}, 7, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := updateTestDB(t)
			s := newUpdateStorage(db)
			original := &tg.Message{ID: 9, PeerID: tc.peer, Date: 100, Message: "synthetic old private text"}
			handle := func(m *tg.Message) {
				t.Helper()
				if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: m}}); err != nil {
					t.Fatal(err)
				}
			}
			handle(original)
			edited := *original
			edited.Message = "synthetic edited private text"
			edited.EditDate = 200
			if err := s.Handle(ctx, &tg.UpdateShort{Update: &tg.UpdateEditMessage{Message: &edited}}); err != nil {
				t.Fatal(err)
			}
			// The same raw peer ID in another peer kind must keep its event/receipt.
			var otherPeer tg.PeerClass = &tg.PeerUser{UserID: 7}
			if tc.chat == 7 {
				otherPeer = &tg.PeerChannel{ChannelID: 7}
			}
			handle(&tg.Message{ID: 9, PeerID: otherPeer, Date: 100, Message: "unrelated synthetic"})
			before, _, err := store.PendingEvents(ctx, db, "synthetic-scope", 100)
			if err != nil || len(before) != 3 {
				t.Fatalf("before: %d %v", len(before), err)
			}
			expired := *original
			expired.TTLPeriod = 10
			if tc.media {
				expired.TTLPeriod = 0
				expired.Media = &tg.MessageMediaPhoto{TTLSeconds: 10}
			}
			handle(&expired)
			check := func() {
				t.Helper()
				events, _, err := store.PendingEvents(ctx, db, "synthetic-scope", 100)
				if err != nil {
					t.Fatal(err)
				}
				if len(events) != 2 {
					t.Fatalf("want unrelated event and content-free expiry notice, got %d pending rows", len(events))
				}
				if events[0].Receipt != before[2].Receipt {
					t.Fatal("unrelated event changed")
				}
				var e ListenEvent
				if err := json.Unmarshal(events[1].Event, &e); err != nil {
					t.Fatal(err)
				}
				if e.ChatID != tc.chat || e.MessageID != 9 || e.UpdateKind != "unsupported_expiring_message" || e.Text != "" || e.MediaIdentity != "" || len(e.Entities) != 0 {
					t.Fatal("expiry notice retained content or lost identity")
				}
			}
			check()
			if _, present, err := store.AcknowledgePendingEvent(ctx, db, "synthetic-scope", before[0].Receipt, true); err != nil || present {
				t.Fatal("stale payload receipt is still pending", err)
			}
			// Recovery may repeat an earlier full/short update after the tombstone.
			handle(original)
			check()
			if tc.chat == 7 {
				if err := s.Handle(ctx, &tg.UpdateShortMessage{ID: 9, UserID: 7, Date: 100, Message: original.Message}); err != nil {
					t.Fatal(err)
				}
				check()
			}
			// A restarted consumer must see the same content-free state.
			g := &GotdClient{db: db, updateStore: newUpdateStorage(db)}
			for i := 0; i < 2; i++ {
				e, err := g.ListenOnce(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if e.ChatID == tc.chat && e.Text != "" {
					t.Fatal("durable replay exposed expired text")
				}
				if err := g.AcknowledgeEvent(ctx, e.EventID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestExpiringOutboxPurgeIsAtomicAndIgnoresUnrelatedCorruptRow(t *testing.T) {
	for _, failInsert := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated corruption", true: "rollback"}[failInsert], func(t *testing.T) {
			ctx := context.Background()
			db := updateTestDB(t)
			s := newUpdateStorage(db)
			update := func(ttl int) tg.UpdatesClass {
				return &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: &tg.Message{ID: 9, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "synthetic", TTLPeriod: ttl}}}
			}
			if err := s.Handle(ctx, update(0)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO tg_event_outbox(event) VALUES('invalid unrelated JSON')"); err != nil {
				t.Fatal(err)
			}
			if failInsert {
				if _, err := db.Exec(`CREATE TRIGGER reject_expiry_notice BEFORE INSERT ON tg_event_outbox WHEN json_extract(NEW.event,'$.update_kind')='unsupported_expiring_message' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err := s.Handle(ctx, update(10))
			if (err != nil) != failInsert {
				t.Fatalf("Handle error = %v", err)
			}
			var original, corrupt, deleted int
			if err := db.QueryRow("SELECT COUNT(*) FROM tg_event_outbox WHERE id=1").Scan(&original); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM tg_event_outbox WHERE event='invalid unrelated JSON'").Scan(&corrupt); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT deleted FROM tg_messages WHERE chat_id=7 AND message_id=9").Scan(&deleted); err != nil {
				t.Fatal(err)
			}
			if corrupt != 1 {
				t.Fatal("unrelated corrupt event was removed")
			}
			if failInsert {
				if original != 1 || deleted != 0 || s.err() == nil {
					t.Fatal("failed update partially purged state or left recovery open")
				}
			} else if original != 0 || deleted != 1 {
				t.Fatal("expiry did not purge queue and cache together")
			}
		})
	}
}
