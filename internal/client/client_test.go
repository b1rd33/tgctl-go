package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
