package client

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
)

func TestDiscoverUsesActualDialogsAndIncludesArchive(t *testing.T) {
	calls := 0
	g := &GotdClient{api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req := in.(*tg.MessagesGetDialogsRequest)
		calls++
		id := int64(7)
		if req.FolderID == 1 {
			id = 8
		}
		out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerUser{UserID: id}, UnreadCount: 2, ReadInboxMaxID: 5, TopMessage: 10}}, Users: []tg.UserClass{&tg.User{ID: id, FirstName: "synthetic"}, &tg.User{ID: 99, FirstName: "auxiliary-only"}}}
		return nil
	}))}
	got, err := g.DiscoverDialogs(context.Background(), 20)
	if err != nil || len(got) != 2 || calls != 2 {
		t.Fatalf("dialogs %v calls %d err %v", got, calls, err)
	}
	if got[0].ID != 7 || got[1].ID != 8 || got[1].FolderID != 1 || got[0].ReadInboxMaxID != 5 {
		t.Fatal("incorrect dialog metadata")
	}
}
func TestPinnedMessagesUsesSelectedPeer(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityChannel, 42); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSearchRequest)
		if !ok {
			t.Fatalf("wrong RPC %T", in)
		}
		if p, ok := req.Peer.(*tg.InputPeerChannel); !ok || p.ChannelID != 7 {
			t.Fatal("wrong pinned-message scope")
		}
		if _, ok := req.Filter.(*tg.InputMessagesFilterPinned); !ok {
			t.Fatal("missing pinned filter")
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 7}, Message: "pin"}}}
		return nil
	}))}
	got, err := g.ListPinnedMessages(context.Background(), peerid.Channel(7))
	if err != nil || len(got) != 1 || got[0].MessageID != 3 {
		t.Fatalf("pins %v %v", got, err)
	}
}
