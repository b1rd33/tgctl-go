package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestSessionStorageForModeReadOnlyUsesInMemorySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tg.session")
	original := []byte("existing-authorized-session")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	wantModTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	storage, err := sessionStorageForMode(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := storage.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, original) {
		t.Fatalf("snapshot = %q, want %q", loaded, original)
	}
	replacement := []byte("gotd-refreshed-session-state")
	if err := storage.StoreSession(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	loaded, err = storage.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, replacement) {
		t.Fatalf("in-memory update = %q, want %q", loaded, replacement)
	}

	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, original) {
		t.Fatalf("real session changed: got %q, want %q", afterBytes, original)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("real session metadata changed: before=%v after=%v", before, after)
	}
}

func TestSessionStorageForModeReadOnlyMissingDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "tg.session")
	_, err := sessionStorageForMode(context.Background(), path, true)
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("error = %v, want session.ErrNotFound", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("session path created or unexpected stat error: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Dir(path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("session parent created or unexpected stat error: %v", statErr)
	}
}

func TestSessionStorageForModeWritablePersistsUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tg.session")
	storage, err := sessionStorageForMode(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("persisted-session-state")
	if err := storage.StoreSession(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("session file = %q, want %q", got, want)
	}
}

func TestEnsureCredentialsMissing(t *testing.T) {
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsMalformedID(t *testing.T) {
	t.Setenv("TG_API_ID", "abc")
	t.Setenv("TG_API_HASH", "x")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsZeroID(t *testing.T) {
	t.Setenv("TG_API_ID", "0")
	t.Setenv("TG_API_HASH", "x")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsValid(t *testing.T) {
	t.Setenv("TG_API_ID", "12345")
	t.Setenv("TG_API_HASH", "deadbeef")
	id, hash, err := EnsureCredentials()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != 12345 || hash != "deadbeef" {
		t.Fatalf("got id=%d hash=%q", id, hash)
	}
}

func TestDisplayNameOrdering(t *testing.T) {
	cases := []struct {
		first, last, username string
		id                    int64
		want                  string
	}{
		{"Bjørn", "Müller", "bjorn", 1, "Bjørn Müller"},
		{"Bjørn", "", "bjorn", 1, "Bjørn"},
		{"", "Müller", "bjorn", 1, "Müller"},
		{"", "", "bjorn", 1, "@bjorn"},
		{"", "", "", 7, "user_7"},
	}
	for _, c := range cases {
		if got := DisplayName(c.first, c.last, c.username, c.id); got != c.want {
			t.Errorf("DisplayName(%q,%q,%q,%d) = %q, want %q",
				c.first, c.last, c.username, c.id, got, c.want)
		}
	}
}

func TestFirstTopicIDUsesTopicCreateServiceMessageID(t *testing.T) {
	updates := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateChannel{ChannelID: 3957621025},
		&tg.UpdateNewChannelMessage{Message: &tg.MessageService{
			ID:     42,
			Action: &tg.MessageActionTopicCreate{Title: "IE Germany - Status"},
		}},
	}}

	if got := firstTopicID(updates); got != 42 {
		t.Fatalf("firstTopicID = %d, want topic top message id 42", got)
	}
}

func TestFolderFilterFromReqIncludesRequestedPeers(t *testing.T) {
	include := []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 10, AccessHash: 20}}
	exclude := []tg.InputPeerClass{&tg.InputPeerUser{UserID: 30, AccessHash: 40}}

	filter := folderFilterFromReq(FolderUpdateReq{ID: 7, Title: "IE DE", Emoji: "📦"}, include, exclude)

	if filter.ID != 7 || filter.Title.Text != "IE DE" || filter.Emoticon != "📦" {
		t.Fatalf("filter metadata = id:%d title:%q emoji:%q", filter.ID, filter.Title.Text, filter.Emoticon)
	}
	if len(filter.IncludePeers) != 1 || filter.IncludePeers[0] != include[0] {
		t.Fatalf("IncludePeers = %#v, want requested include peer", filter.IncludePeers)
	}
	if len(filter.ExcludePeers) != 1 || filter.ExcludePeers[0] != exclude[0] {
		t.Fatalf("ExcludePeers = %#v, want requested exclude peer", filter.ExcludePeers)
	}
}

func TestFolderInfoFromDialogFilterIncludesPeerIDs(t *testing.T) {
	filter := &tg.DialogFilter{
		ID:       6,
		Title:    tg.TextWithEntities{Text: "Ops"},
		Emoticon: "box",
		IncludePeers: []tg.InputPeerClass{
			&tg.InputPeerUser{UserID: 1240314255, AccessHash: 10},
			&tg.InputPeerChannel{ChannelID: 3957621025, AccessHash: 20},
			&tg.InputPeerChat{ChatID: 5122015159},
		},
		ExcludePeers: []tg.InputPeerClass{
			&tg.InputPeerUser{UserID: 777000, AccessHash: 30},
		},
	}

	info := folderInfoFromDialogFilter(filter)

	if info.ID != 6 || info.Title != "Ops" || info.Emoji != "box" {
		t.Fatalf("info metadata = %#v", info)
	}
	wantInclude := []int64{1240314255, 3957621025, 5122015159}
	if len(info.IncludeChatIDs) != len(wantInclude) {
		t.Fatalf("IncludeChatIDs = %#v, want %#v", info.IncludeChatIDs, wantInclude)
	}
	for i, want := range wantInclude {
		if info.IncludeChatIDs[i] != want {
			t.Fatalf("IncludeChatIDs[%d] = %d, want %d", i, info.IncludeChatIDs[i], want)
		}
	}
	if len(info.ExcludeChatIDs) != 1 || info.ExcludeChatIDs[0] != 777000 {
		t.Fatalf("ExcludeChatIDs = %#v, want [777000]", info.ExcludeChatIDs)
	}
}

func TestMergeFolderUpdatePreservesExistingMetadataAndMembership(t *testing.T) {
	existing := FolderInfo{
		ID:             6,
		Title:          "Ops",
		Emoji:          "box",
		IncludeChatIDs: []int64{1240314255},
		ExcludeChatIDs: []int64{777000},
	}

	merged := mergeFolderUpdate(existing, FolderUpdateReq{ID: 6, IncludeChatIDs: []int64{3957621025}})

	if merged.Title != "Ops" || merged.Emoji != "box" {
		t.Fatalf("merged metadata = title:%q emoji:%q", merged.Title, merged.Emoji)
	}
	wantInclude := []int64{1240314255, 3957621025}
	if len(merged.IncludeChatIDs) != len(wantInclude) {
		t.Fatalf("IncludeChatIDs = %#v, want %#v", merged.IncludeChatIDs, wantInclude)
	}
	for i, want := range wantInclude {
		if merged.IncludeChatIDs[i] != want {
			t.Fatalf("IncludeChatIDs[%d] = %d, want %d", i, merged.IncludeChatIDs[i], want)
		}
	}
	if len(merged.ExcludeChatIDs) != 1 || merged.ExcludeChatIDs[0] != 777000 {
		t.Fatalf("ExcludeChatIDs = %#v, want [777000]", merged.ExcludeChatIDs)
	}
}

func TestMessagesFromHistoryRespCoversAllThreeShapes(t *testing.T) {
	cases := []struct {
		name string
		resp tg.MessagesMessagesClass
		want int
	}{
		{
			name: "MessagesMessages (regular DM)",
			resp: &tg.MessagesMessages{Messages: []tg.MessageClass{
				&tg.Message{ID: 1}, &tg.Message{ID: 2},
			}},
			want: 2,
		},
		{
			name: "MessagesMessagesSlice (paged DM)",
			resp: &tg.MessagesMessagesSlice{Messages: []tg.MessageClass{&tg.Message{ID: 3}}},
			want: 1,
		},
		{
			name: "MessagesChannelMessages (channel/supergroup)",
			resp: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
				&tg.Message{ID: 4}, &tg.Message{ID: 5}, &tg.Message{ID: 6},
			}},
			want: 3,
		},
		{
			name: "MessagesMessagesNotModified treated as empty",
			resp: &tg.MessagesMessagesNotModified{},
			want: 0,
		},
	}
	for _, tc := range cases {
		got := messagesFromHistoryResp(tc.resp)
		if len(got) != tc.want {
			t.Errorf("%s: len = %d, want %d", tc.name, len(got), tc.want)
		}
	}
}

func historyMessages(highID, count int) []tg.MessageClass {
	msgs := make([]tg.MessageClass, 0, count)
	for id := highID; id > highID-count; id-- {
		msgs = append(msgs, &tg.Message{ID: id, Date: 1_700_000_000, Message: "message"})
	}
	return msgs
}

func TestBackfillThrottleSleepsOnceBetweenTwoFullPages(t *testing.T) {
	pages := []historyPage{
		{Messages: historyMessages(200, 100), Total: 200, TotalKnown: true},
		{Messages: historyMessages(100, 100), Total: 200, TotalKnown: true},
	}
	type request struct{ offsetID, limit int }
	var requests []request
	var sleeps []time.Duration

	rows, err := paginateHistory(context.Background(), 42, 200, 250*time.Millisecond,
		func(_ context.Context, offsetID, limit int) (historyPage, error) {
			requests = append(requests, request{offsetID, limit})
			page := pages[0]
			pages = pages[1:]
			return page, nil
		},
		func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 200 {
		t.Fatalf("rows=%d, want 200", len(rows))
	}
	wantRequests := []request{{0, 100}, {101, 100}}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests=%#v, want %#v", requests, wantRequests)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{250 * time.Millisecond}) {
		t.Fatalf("sleeps=%v, want [250ms]", sleeps)
	}
}

func TestBackfillThrottleDoesNotSleepAfterExactFullFinalPage(t *testing.T) {
	calls := 0
	sleeps := 0
	rows, err := paginateHistory(context.Background(), 42, 200, time.Second,
		func(_ context.Context, _, limit int) (historyPage, error) {
			calls++
			if limit != 100 {
				t.Fatalf("limit=%d, want 100", limit)
			}
			return historyPage{Messages: historyMessages(100, 100), Total: 100, TotalKnown: true}, nil
		},
		func(context.Context, time.Duration) error { sleeps++; return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 100 || calls != 1 || sleeps != 0 {
		t.Fatalf("rows=%d calls=%d sleeps=%d, want 100/1/0", len(rows), calls, sleeps)
	}
}

func TestBackfillThrottleDoesNotWaitForOnePageOrDisabledThrottle(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limit    int
		throttle time.Duration
		pages    []historyPage
	}{
		{name: "one page", limit: 50, throttle: time.Second, pages: []historyPage{{Messages: historyMessages(50, 50), Total: 50, TotalKnown: true}}},
		{name: "zero", limit: 200, pages: []historyPage{{Messages: historyMessages(200, 100), Total: 200, TotalKnown: true}, {Messages: historyMessages(100, 100), Total: 200, TotalKnown: true}}},
		{name: "negative", limit: 200, throttle: -time.Second, pages: []historyPage{{Messages: historyMessages(200, 100), Total: 200, TotalKnown: true}, {Messages: historyMessages(100, 100), Total: 200, TotalKnown: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sleeps := 0
			_, err := paginateHistory(context.Background(), 42, tc.limit, tc.throttle,
				func(_ context.Context, _, _ int) (historyPage, error) {
					page := tc.pages[calls]
					calls++
					return page, nil
				},
				func(context.Context, time.Duration) error { sleeps++; return nil },
			)
			if err != nil {
				t.Fatal(err)
			}
			if want := len(tc.pages); calls != want {
				t.Fatalf("calls=%d, want %d", calls, want)
			}
			if sleeps != 0 {
				t.Fatalf("sleeps=%d, want 0", sleeps)
			}
		})
	}
}

func TestBackfillPaginationFinalPartialPage(t *testing.T) {
	pages := []historyPage{
		{Messages: historyMessages(225, 100), Total: 225, TotalKnown: true},
		{Messages: historyMessages(125, 100), Total: 225, TotalKnown: true},
		{Messages: historyMessages(25, 25), Total: 225, TotalKnown: true},
	}
	var limits []int
	var sleeps []time.Duration
	rows, err := paginateHistory(context.Background(), 42, 250, time.Second,
		func(_ context.Context, _ int, limit int) (historyPage, error) {
			limits = append(limits, limit)
			page := pages[0]
			pages = pages[1:]
			return page, nil
		},
		func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 225 {
		t.Fatalf("rows=%d, want 225", len(rows))
	}
	if !reflect.DeepEqual(limits, []int{100, 100, 50}) {
		t.Fatalf("limits=%v, want [100 100 50]", limits)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, time.Second}) {
		t.Fatalf("sleeps=%v, want two one-second sleeps", sleeps)
	}
}

func TestBackfillPaginationStopsOnEmptyPage(t *testing.T) {
	pages := []historyPage{{Messages: historyMessages(100, 100)}, {}}
	var limits []int
	rows, err := paginateHistory(context.Background(), 42, 250, 0,
		func(_ context.Context, _ int, limit int) (historyPage, error) {
			limits = append(limits, limit)
			page := pages[0]
			pages = pages[1:]
			return page, nil
		},
		func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 100 {
		t.Fatalf("rows=%d, want 100", len(rows))
	}
	if !reflect.DeepEqual(limits, []int{100, 100}) {
		t.Fatalf("limits=%v, want [100 100]", limits)
	}
}

func TestBackfillThrottleCancellationReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := paginateHistory(ctx, 42, 200, time.Hour,
			func(_ context.Context, _, _ int) (historyPage, error) {
				calls++
				return historyPage{Messages: historyMessages(200, 100), Total: 200, TotalKnown: true}, nil
			},
			func(ctx context.Context, d time.Duration) error {
				close(started)
				return waitForThrottle(ctx, d)
			},
		)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pagination did not return promptly after cancellation")
	}
	if calls != 1 {
		t.Fatalf("fetch calls=%d, want 1", calls)
	}
}

func TestBackfillPaginationRejectsUnboundedLimitBeforeFetch(t *testing.T) {
	fetches := 0
	rows, err := paginateHistory(context.Background(), 42, MaxBackfillMessages+1, 0,
		func(context.Context, int, int) (historyPage, error) {
			fetches++
			return historyPage{}, nil
		},
		waitForThrottle,
	)
	var badArgs *safety.BadArgs
	if !errors.As(err, &badArgs) {
		t.Fatalf("err=%v, want *safety.BadArgs", err)
	}
	if len(rows) != 0 || fetches != 0 {
		t.Fatalf("rows=%d fetches=%d, want 0/0", len(rows), fetches)
	}
}

func TestHistoryPageFromRespCarriesKnownTotals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resp       tg.MessagesMessagesClass
		wantTotal  int
		wantKnown  bool
		wantLength int
	}{
		{name: "full", resp: &tg.MessagesMessages{Messages: historyMessages(2, 2)}, wantTotal: 2, wantKnown: true, wantLength: 2},
		{name: "slice", resp: &tg.MessagesMessagesSlice{Count: 123, Messages: historyMessages(2, 2)}, wantTotal: 123, wantKnown: true, wantLength: 2},
		{name: "channel", resp: &tg.MessagesChannelMessages{Count: 456, Messages: historyMessages(2, 2)}, wantTotal: 456, wantKnown: true, wantLength: 2},
		{name: "not modified", resp: &tg.MessagesMessagesNotModified{}, wantLength: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := historyPageFromResp(tc.resp)
			if page.Total != tc.wantTotal || page.TotalKnown != tc.wantKnown || len(page.Messages) != tc.wantLength {
				t.Fatalf("page=%#v", page)
			}
		})
	}
}

func TestMapRPCErrClassifiesFloodWait(t *testing.T) {
	// gotd surfaces FloodWait as an *tgerr.Error with Type=FLOOD_WAIT and
	// Argument=<seconds>. Construct one and verify mapRPCErr lands on
	// *safety.FloodWait with the right seconds.
	syntheticErr := tgerr.New(420, "FLOOD_WAIT_42")
	out := mapRPCErr(syntheticErr)
	var fw *safety.FloodWait
	if !errors.As(out, &fw) {
		t.Fatalf("got %T (%v), want *safety.FloodWait", out, out)
	}
	if fw.Seconds != 42 {
		t.Fatalf("FloodWait.Seconds = %d, want 42", fw.Seconds)
	}
}

func TestMapRPCErrClassifiesPremiumRequired(t *testing.T) {
	syntheticErr := tgerr.New(403, "PREMIUM_ACCOUNT_REQUIRED")
	out := mapRPCErr(syntheticErr)
	var pr *safety.PremiumRequired
	if !errors.As(out, &pr) {
		t.Fatalf("got %T (%v), want *safety.PremiumRequired", out, out)
	}
}
