package client

import (
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
)

func TestPermissionPatchPreservesUnrelatedRights(t *testing.T) {
	old := tg.ChatBannedRights{ChangeInfo: true, InviteUsers: true, PinMessages: true, ManageTopics: true, UntilDate: 123}
	got, err := patchBannedRights(old, "read-only")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ChangeInfo || !got.InviteUsers || !got.PinMessages || !got.ManageTopics || got.UntilDate != 123 || !got.SendPlain {
		t.Fatal("unrelated rights lost")
	}
	got, err = patchBannedRights(got, "send-messages")
	if err != nil || got.SendPlain || !got.ChangeInfo {
		t.Fatal("allow patch cleared unrelated rights")
	}
	for _, value := range []string{"", "unrestrict", "all", "read-only-typo"} {
		if _, err := patchBannedRights(old, value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
func TestFolderPatchPreservesFlagsPinnedPeersAndTitleEntities(t *testing.T) {
	pinned := &tg.InputPeerChannel{ChannelID: 7, AccessHash: 91}
	old := &tg.DialogFilter{ID: 2, Contacts: true, NonContacts: true, Groups: true, ExcludeMuted: true, ExcludeRead: true, ExcludeArchived: true, PinnedPeers: []tg.InputPeerClass{pinned}, Title: tg.TextWithEntities{Text: "folder", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Length: 6}}}}
	got := patchFolderPeers(old, []tg.InputPeerClass{&tg.InputPeerUser{UserID: 7, AccessHash: 92}}, nil)
	if !got.Contacts || !got.Groups || !got.ExcludeMuted || !got.ExcludeRead || !got.ExcludeArchived || len(got.Title.Entities) != 1 || len(got.PinnedPeers) != 1 {
		t.Fatal("unrelated folder data lost")
	}
	if len(old.IncludePeers) != 0 {
		t.Fatal("original mutated")
	}
	got = patchFolderPeers(got, nil, []tg.InputPeerClass{&tg.InputPeerUser{UserID: 7}})
	if len(got.PinnedPeers) != 1 {
		t.Fatal("same numeric ID removed different peer kind")
	}
}

func TestPermissionAllowClearsEncodedBits(t *testing.T) {
	old := tg.ChatBannedRights{SendPlain: true, SendMessages: true, SendMedia: true, ChangeInfo: true, UntilDate: 123}
	old.SetFlags()
	got, err := patchBannedRights(old, "send-messages")
	if err != nil {
		t.Fatal(err)
	}
	var buf bin.Buffer
	if err := got.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	var decoded tg.ChatBannedRights
	if err := decoded.Decode(&buf); err != nil {
		t.Fatal(err)
	}
	if decoded.SendPlain || decoded.SendMessages || decoded.SendMedia || !decoded.ChangeInfo || decoded.UntilDate != 123 {
		t.Fatalf("incorrect encoded permissions: %+v", decoded)
	}
}
