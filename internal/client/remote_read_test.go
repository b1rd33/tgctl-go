package client

import (
	"context"
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestUserFromSelfPreservesUnknownPremiumState(t *testing.T) {
	unknown := userFromSelf(&tg.User{ID: 7, Premium: true, Min: true, FirstName: "unknown"})
	if !unknown.Premium || unknown.PremiumKnown {
		t.Fatalf("unknown self premium = %+v", unknown)
	}

	knownFree := userFromSelf(&tg.User{ID: 8, Premium: false, Min: false, FirstName: "free"})
	if knownFree.Premium || !knownFree.PremiumKnown {
		t.Fatalf("known free self premium = %+v", knownFree)
	}

	knownPremium := userFromSelf(&tg.User{ID: 9, Premium: true, Min: false, FirstName: "premium"})
	if !knownPremium.Premium || !knownPremium.PremiumKnown {
		t.Fatalf("known premium self premium = %+v", knownPremium)
	}
}

func TestEffectiveRightsApplyBansOverAdminDefaults(t *testing.T) {
	got := effectiveRights(
		&tg.ChatAdminRights{PostMessages: true, ManageTopics: true},
		&tg.ChatBannedRights{SendMessages: true, SendMedia: true, EmbedLinks: true},
	)
	if !got["manage_topics"] || got["send_messages"] || got["send_media"] || got["embed_links"] {
		t.Fatalf("effective rights = %#v", got)
	}
}

func TestRemoteHistoryAdapterPropagatesAllOffsets(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesGetHistoryRequest)
		if !ok {
			t.Fatalf("wrong history request %T", in)
		}
		peer, ok := req.Peer.(*tg.InputPeerUser)
		if !ok || peer.UserID != 7 || peer.AccessHash != 70 {
			t.Fatalf("history peer = %#v", req.Peer)
		}
		if req.OffsetID != 42 || req.OffsetDate != 1234 || req.Limit != 7 || req.MinID != 3 || req.MaxID != 99 {
			t.Fatalf("history offsets = %+v", req)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 41, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "synthetic"},
		}}
		return nil
	}))}

	page, err := g.RemoteHistory(context.Background(), RemoteHistoryReq{ChatID: 7, OffsetID: 42, OffsetDate: 1234, Limit: 7, MinID: 3, MaxID: 99})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].MessageID != 41 || page.NextOffsetID != 41 {
		t.Fatalf("history page = %+v", page)
	}
}

func TestRemoteSearchAdapterPropagatesFiltersAndOffsets(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEntity(db, 8, store.EntityUser, 80); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSearchRequest)
		if !ok {
			t.Fatalf("wrong search request %T", in)
		}
		peer, ok := req.Peer.(*tg.InputPeerUser)
		if !ok || peer.UserID != 7 || peer.AccessHash != 70 {
			t.Fatalf("search peer = %#v", req.Peer)
		}
		sender, ok := req.GetFromID()
		senderPeer, senderOK := sender.(*tg.InputPeerUser)
		if !ok || !senderOK || senderPeer.UserID != 8 || senderPeer.AccessHash != 80 {
			t.Fatalf("search sender = %#v", sender)
		}
		if req.Q != "needle" || req.MinDate != 100 || req.MaxDate != 200 || req.OffsetID != 42 || req.Limit != 6 || req.MinID != 3 || req.MaxID != 99 || req.TopMsgID != 77 {
			t.Fatalf("search options = %+v", req)
		}
		if _, ok := req.Filter.(*tg.InputMessagesFilterPhotoVideo); !ok {
			t.Fatalf("search filter = %T", req.Filter)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 41, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "synthetic"},
		}}
		return nil
	}))}

	page, err := g.RemoteSearch(context.Background(), RemoteSearchReq{ChatID: 7, SenderID: 8, Query: "needle", Filter: "photo-video", MinDate: 100, MaxDate: 200, OffsetID: 42, Limit: 6, MinID: 3, MaxID: 99, TopMsgID: 77})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].MessageID != 41 {
		t.Fatalf("search page = %+v", page)
	}
}

func TestRepliesAndDiscussionAdaptersPreservePeerRouting(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEntity(db, 9, store.EntityChannel, 90); err != nil {
		t.Fatal(err)
	}
	discussionCalls := 0
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.MessagesGetRepliesRequest:
			peer, ok := req.Peer.(*tg.InputPeerChannel)
			if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || req.MsgID != 5 || req.OffsetID != 4 || req.Limit != 3 {
				t.Fatalf("replies request = %+v", req)
			}
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
				&tg.Message{ID: 4, PeerID: &tg.PeerChannel{ChannelID: 7}, Date: 100, Message: "reply"},
			}}
		case *tg.MessagesGetDiscussionMessageRequest:
			discussionCalls++
			peer, ok := req.Peer.(*tg.InputPeerChannel)
			if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || req.MsgID != 5 {
				t.Fatalf("discussion request = %+v", req)
			}
			out.(*tg.MessagesDiscussionMessage).Chats = []tg.ChatClass{&tg.Channel{ID: 7}, &tg.Channel{ID: 9}}
			out.(*tg.MessagesDiscussionMessage).Messages = []tg.MessageClass{
				&tg.Message{ID: 5, PeerID: &tg.PeerChannel{ChannelID: 7}, Date: 100, Message: "post"},
				&tg.Message{ID: 6, PeerID: &tg.PeerChannel{ChannelID: 9}, Date: 101, Message: "reply"},
			}
		default:
			t.Fatalf("unexpected request %T", in)
		}
		return nil
	}))}

	page, err := g.GetReplies(context.Background(), RepliesReq{ChatID: peerid.Channel(7), RootID: 5, OffsetID: 4, Limit: 3})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ChatID != peerid.Channel(7) {
		t.Fatalf("replies page = %+v err=%v", page, err)
	}
	info, err := g.GetDiscussionMessage(context.Background(), peerid.Channel(7), 5)
	if err != nil {
		t.Fatal(err)
	}
	if discussionCalls != 1 || info.DiscussionChatID != peerid.Channel(9) || len(info.Messages) != 2 || info.Messages[0].ChatID != peerid.Channel(7) || info.Messages[1].ChatID != peerid.Channel(9) {
		t.Fatalf("discussion info = %+v", info)
	}
}

func TestTopicHistoryAdapterValidatesForumRootAndRoutesReplies(t *testing.T) {
	newClient := func(forum bool, root tg.MessageClass) *GotdClient {
		db := updateTestDB(t)
		if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
			t.Fatal(err)
		}
		return &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			switch req := in.(type) {
			case *tg.MessagesGetPeerDialogsRequest:
				out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: 70, Megagroup: true, Forum: forum, Title: "Forum"}}
				out.(*tg.MessagesPeerDialogs).Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 7}}}
			case *tg.ChannelsGetMessagesRequest:
				peer, ok := req.Channel.(*tg.InputChannel)
				if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || len(req.ID) != 1 {
					t.Fatalf("topic root request = %+v", req)
				}
				out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{root}}
			case *tg.MessagesGetRepliesRequest:
				peer, ok := req.Peer.(*tg.InputPeerChannel)
				if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || req.MsgID != 11 || req.OffsetID != 5 || req.Limit != 3 {
					t.Fatalf("topic replies request = %+v", req)
				}
				out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
					&tg.Message{ID: 10, PeerID: &tg.PeerChannel{ChannelID: 7}, Date: 100, Message: "topic reply"},
				}}
			default:
				t.Fatalf("unexpected topic request %T", in)
			}
			return nil
		}))}
	}

	page, err := newClient(true, &tg.Message{ID: 11, PeerID: &tg.PeerChannel{ChannelID: 7}, Date: 99, Message: "topic root"}).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, OffsetID: 5, Limit: 3})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ChatID != peerid.Channel(7) {
		t.Fatalf("topic history page = %+v err=%v", page, err)
	}

	if _, err := newClient(false, &tg.Message{ID: 11}).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, Limit: 3}); err == nil {
		t.Fatal("non-forum peer was accepted")
	}
	_, err = newClient(true, &tg.MessageEmpty{ID: 11}).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, Limit: 3})
	var notFound *resolve.NotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("deleted topic root error = %T %v", err, err)
	}
}
