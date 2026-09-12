package client

import (
	"context"
	"database/sql"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func updateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Connect(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestDurableUpdatesPreserveBurstAndReplayUnacknowledged(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	batch := &tg.UpdatesCombined{}
	for i := 1; i <= 150; i++ {
		batch.Updates = append(batch.Updates, &tg.UpdateNewMessage{Message: &tg.Message{ID: i, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "synthetic"}})
	}
	if err := s.Handle(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, updateStore: s}
	for i := 1; i <= 150; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e, err := g.ListenOnce(ctx)
		cancel()
		if err != nil || e.MessageID != int64(i) {
			t.Fatalf("event %d: %v %v", i, e, err)
		}
		if i < 150 {
			if err := g.AcknowledgeEvent(context.Background(), e.EventID); err != nil {
				t.Fatal(err)
			}
		}
	}
	restarted := &GotdClient{db: db, updateStore: newUpdateStorage(db)}
	e, err := restarted.ListenOnce(context.Background())
	if err != nil || e.MessageID != 150 {
		t.Fatal("unacknowledged event lost")
	}
}
func TestCommonDeletionDoesNotTouchChannelAndSurvivesBackfill(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	for _, peer := range []tg.PeerClass{&tg.PeerUser{UserID: 7}, &tg.PeerChannel{ChannelID: 7}} {
		if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: &tg.Message{ID: 9, PeerID: peer, Date: 100, Message: "synthetic"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateDeleteMessages{Messages: []int{9, 10}}}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]int{7: 1, peerid.Channel(7): 0} {
		var deleted int
		if err := db.QueryRow("SELECT deleted FROM tg_messages WHERE chat_id=? AND message_id=9", id).Scan(&deleted); err != nil || deleted != want {
			t.Fatalf("peer %d: %d %v", id, deleted, err)
		}
	}
	if err := store.UpsertLiveMessage(db, store.LiveMessage{ChatID: 7, MessageID: 10, Date: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	var deleted int
	if err := db.QueryRow("SELECT deleted FROM tg_messages WHERE chat_id=7 AND message_id=10").Scan(&deleted); err != nil || deleted != 1 {
		t.Fatal("deleted message resurrected")
	}
}

type stateAPI struct{}

func (stateAPI) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{Pts: 10, Date: 100}, nil
}
func (stateAPI) UpdatesGetDifference(context.Context, *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	return &tg.UpdatesDifferenceEmpty{Date: 100}, nil
}
func (stateAPI) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	return nil, errors.New("unexpected channel")
}
func TestGotdManagerCannotAdvanceCheckpointAfterPersistenceFailure(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	if _, err := db.Exec("CREATE TRIGGER fail_messages BEFORE INSERT ON tg_messages BEGIN SELECT RAISE(FAIL, 'injected persistence failure'); END"); err != nil {
		t.Fatal(err)
	}
	manager := updates.New(updates.Config{Handler: s, Storage: s, AccessHasher: s})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.Run(ctx, stateAPI{}, 7, updates.AuthOptions{OnStart: func(context.Context) { close(started) }})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not start")
	}
	if err := manager.Handle(ctx, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 7}, Date: 100}, Pts: 11, PtsCount: 1}}, Date: 100}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.failed:
	case <-time.After(3 * time.Second):
		t.Fatal("storage failure not propagated")
	}
	if err := s.SetPts(context.Background(), 7, 11); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatal("checkpoint barrier open")
	}
	state, ok, err := s.GetState(context.Background(), 7)
	if err != nil || !ok || state.Pts != 10 {
		t.Fatalf("checkpoint advanced: %+v %v", state, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop")
	}
}
func TestAcknowledgedOnceEventDoesNotReplay(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 7}, Date: 100}}}); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, updateStore: s}
	event, err := g.ListenOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := g.AcknowledgeEvent(context.Background(), event.EventID); err != nil {
		t.Fatal(err)
	}
	restarted := &GotdClient{db: db, updateStore: newUpdateStorage(db)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := restarted.ListenOnce(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acknowledged event replayed: %v", err)
	}
}
func TestCheckpointUpdateRequiresExistingState(t *testing.T) {
	s := newUpdateStorage(updateTestDB(t))
	if err := s.SetPts(context.Background(), 7, 100); !errors.Is(err, ErrRecoveryIncomplete) {
		t.Fatalf("missing checkpoint accepted: %v", err)
	}
}

type gapAPI struct{ calls int }

func (*gapAPI) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{Pts: 160, Date: 100}, nil
}
func (a *gapAPI) UpdatesGetDifference(_ context.Context, r *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	a.calls++
	end := 110
	if r.Pts >= 110 {
		end = 160
	}
	msgs := []tg.MessageClass{}
	for i := r.Pts + 1; i <= end; i++ {
		msgs = append(msgs, &tg.Message{ID: i, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "synthetic gap"})
	}
	if end == 110 {
		return &tg.UpdatesDifferenceSlice{NewMessages: msgs, IntermediateState: tg.UpdatesState{Pts: 110, Date: 100}}, nil
	}
	return &tg.UpdatesDifference{NewMessages: msgs, State: tg.UpdatesState{Pts: 160, Date: 100}}, nil
}
func (*gapAPI) UpdatesGetChannelDifference(context.Context, *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	return nil, errors.New("unexpected channel")
}
func TestManagerRecoversMoreThanOneHundredMissedMessages(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	if err := s.SetState(context.Background(), 7, updates.State{Pts: 10, Date: 100}); err != nil {
		t.Fatal(err)
	}
	manager := updates.New(updates.Config{Handler: s, Storage: s, AccessHasher: s})
	api := &gapAPI{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx, api, 7, updates.AuthOptions{}) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, _, err := s.GetState(context.Background(), 7)
		if err != nil {
			t.Fatal(err)
		}
		if state.Pts == 160 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	state, _, _ := s.GetState(context.Background(), 7)
	if count != 150 || state.Pts != 160 || api.calls != 2 {
		t.Fatalf("gap recovery count=%d pts=%d calls=%d", count, state.Pts, api.calls)
	}
}

func TestExpiringUpdatePurgesManagedContentAndCannotResurrect(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mediaDir := filepath.Join(dir, "media")
	if err := os.Mkdir(mediaDir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(mediaDir, "synthetic.jpg")
	if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	text := "synthetic"
	if err := store.UpsertLiveMessage(db, store.LiveMessage{ChatID: 7, MessageID: 1, Text: &text, Date: "2026-01-01T00:00:00Z", MediaPath: &file, HasMedia: true}); err != nil {
		t.Fatal(err)
	}
	s := newUpdateStorage(db)
	if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateNewMessage{Message: &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 7}, TTLPeriod: 10, Message: "private"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("managed expiring file remains")
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE text IS NOT NULL OR media_path IS NOT NULL").Scan(&count)
	if count != 0 {
		t.Fatal("expiring content remains cached")
	}
	if err := store.UpsertLiveMessage(db, store.LiveMessage{ChatID: 7, MessageID: 1, Text: &text, Date: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	var deleted int
	db.QueryRow("SELECT deleted FROM tg_messages WHERE chat_id=7").Scan(&deleted)
	if deleted != 1 {
		t.Fatal("expired message resurrected")
	}
	db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE text IS NOT NULL").Scan(&count)
	if count != 0 {
		t.Fatal("stale replay reintroduced expiring text")
	}
}
