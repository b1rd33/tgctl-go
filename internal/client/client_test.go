package client

import (
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/tg"
)

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
