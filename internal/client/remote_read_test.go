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

func TestRemoteGetAdapterPreservesDeletedPlaceholder(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesGetMessagesRequest)
		if !ok {
			t.Fatalf("wrong get request %T", in)
		}
		if len(req.ID) != 1 {
			t.Fatalf("get IDs = %+v", req.ID)
		}
		messageID, ok := req.ID[0].(*tg.InputMessageID)
		if !ok || messageID.ID != 9 {
			t.Fatalf("get ID = %+v", req.ID[0])
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 9}}}
		return nil
	}))}

	message, err := g.RemoteGetMessage(context.Background(), 7, 9)
	if err != nil || message == nil || message.ChatID != 7 || message.MessageID != 9 || !message.Deleted {
		t.Fatalf("deleted remote message = %+v err=%v", message, err)
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

func TestTopicHistoryAdapterValidatesForumTopicAndRoutesReplies(t *testing.T) {
	newClient := func(forum bool, topic tg.ForumTopicClass, replyRoot, offset int) *GotdClient {
		db := updateTestDB(t)
		if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
			t.Fatal(err)
		}
		return &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			switch req := in.(type) {
			case *tg.MessagesGetPeerDialogsRequest:
				out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: 70, Megagroup: true, Forum: forum, Title: "Forum"}}
				out.(*tg.MessagesPeerDialogs).Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 7}}}
			case *tg.MessagesGetForumTopicsByIDRequest:
				peer, ok := req.Peer.(*tg.InputPeerChannel)
				if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || len(req.Topics) != 1 || req.Topics[0] != replyRoot {
					t.Fatalf("forum topic request = %+v", req)
				}
				out.(*tg.MessagesForumTopics).Topics = []tg.ForumTopicClass{topic}
			case *tg.MessagesGetRepliesRequest:
				peer, ok := req.Peer.(*tg.InputPeerChannel)
				if !ok || peer.ChannelID != 7 || peer.AccessHash != 70 || req.MsgID != replyRoot || req.OffsetID != offset || req.Limit != 3 {
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

	result, err := newClient(true, &tg.ForumTopic{ID: 11, Title: "Support", TopMessage: 20}, 11, 5).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, OffsetID: 5, Limit: 3})
	if err != nil || result.Topic.ID != 11 || result.Topic.TopMessageID != 20 || len(result.Page.Messages) != 1 || result.Page.Messages[0].ChatID != peerid.Channel(7) {
		t.Fatalf("topic history result = %+v err=%v", result, err)
	}

	if _, err := newClient(false, &tg.ForumTopic{ID: 11}, 11, 0).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, Limit: 3}); err == nil {
		t.Fatal("non-forum peer was accepted")
	}
	if _, err := newClient(true, &tg.ForumTopic{ID: 12}, 11, 0).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, Limit: 3}); err == nil {
		t.Fatal("ordinary message ID was accepted as a topic")
	}
	_, err = newClient(true, &tg.ForumTopicDeleted{ID: 11}, 11, 0).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 11, Limit: 3})
	var notFound *resolve.NotFound
	if !errors.As(err, &notFound) {
		t.Fatalf("deleted topic error = %T %v", err, err)
	}
	general, err := newClient(true, &tg.ForumTopic{ID: 1, Title: "General", Hidden: true}, 1, 0).TopicHistory(context.Background(), TopicHistoryReq{ChatID: peerid.Channel(7), TopicID: 1, Limit: 3})
	if err != nil || general.Topic.ID != 1 || !general.Topic.Hidden {
		t.Fatalf("general topic result = %+v err=%v", general, err)
	}
}

func TestChatPermissionsAdapterIncludesSlowmode(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.MessagesGetPeerDialogsRequest:
			out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: 70, Megagroup: true, Forum: true, Title: "Forum"}}
			out.(*tg.MessagesPeerDialogs).Dialogs = []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 7}}}
		case *tg.ChannelsGetFullChannelRequest:
			channel, ok := req.Channel.(*tg.InputChannel)
			if !ok || channel.ChannelID != 7 || channel.AccessHash != 70 {
				t.Fatalf("full channel request = %+v", req)
			}
			out.(*tg.MessagesChatFull).FullChat = &tg.ChannelFull{SlowmodeSeconds: 30, SlowmodeNextSendDate: 123}
		default:
			t.Fatalf("unexpected permissions request %T", in)
		}
		return nil
	}))}

	info, err := g.GetChatPermissions(context.Background(), peerid.Channel(7), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Chat.SlowmodeKnown || info.Chat.SlowmodeSeconds != 30 || info.Chat.SlowmodeNextSendDate != 123 {
		t.Fatalf("permissions slowmode = %+v", info.Chat)
	}
}

func TestRemoteReadsDoNotWriteCacheOrReadStateOnFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cancelled bool
		run       func(context.Context, *GotdClient) error
	}{
		{name: "history rpc failure", run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteHistory(ctx, RemoteHistoryReq{ChatID: 7, Limit: 1})
			return err
		}},
		{name: "search rpc failure", run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteSearch(ctx, RemoteSearchReq{ChatID: 7, Query: "needle", Limit: 1})
			return err
		}},
		{name: "get rpc failure", run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteGetMessage(ctx, 7, 9)
			return err
		}},
		{name: "history cancellation", cancelled: true, run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteHistory(ctx, RemoteHistoryReq{ChatID: 7, Limit: 1})
			return err
		}},
		{name: "search cancellation", cancelled: true, run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteSearch(ctx, RemoteSearchReq{ChatID: 7, Query: "needle", Limit: 1})
			return err
		}},
		{name: "get cancellation", cancelled: true, run: func(ctx context.Context, g *GotdClient) error {
			_, err := g.RemoteGetMessage(ctx, 7, 9)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := updateTestDB(t)
			if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertLiveMessage(db, store.LiveMessage{ChatID: 7, MessageID: 1, Date: "2026-01-01T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO tg_read_state(chat_id, max_id) VALUES (7, 1)"); err != nil {
				t.Fatal(err)
			}
			var rpcErr error = errors.New("synthetic RPC failure")
			api := tg.NewClient(invokeFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
				if tc.cancelled {
					return ctx.Err()
				}
				return rpcErr
			}))
			g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: api}
			ctx := context.Background()
			if tc.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := tc.run(ctx, g)
			if err == nil {
				t.Fatal("failed remote read succeeded")
			}
			if tc.cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want context.Canceled", err)
			}
			var messages, entities, markers int
			if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages").Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM tg_entities").Scan(&entities); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM tg_read_state").Scan(&markers); err != nil {
				t.Fatal(err)
			}
			if messages != 1 || entities != 1 || markers != 1 {
				t.Fatalf("failure changed cache state: messages=%d entities=%d read_markers=%d", messages, entities, markers)
			}
		})
	}
}
