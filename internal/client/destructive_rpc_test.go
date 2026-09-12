package client

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
)

func TestDeleteRefusesMessagesFromDifferentPeer(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 1); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.MessagesGetMessagesRequest:
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, PeerID: &tg.PeerUser{UserID: 8}}}}
			return nil
		default:
			t.Fatalf("destructive RPC reached: %T", in)
			return nil
		}
	}))}
	if _, err := g.DeleteMessages(context.Background(), DeleteMessagesReq{ChatID: 7, MessageIDs: []int64{3}, ForEveryone: true}); err == nil {
		t.Fatal("wrong-peer deletion accepted")
	}
}
func TestDeleteChannelScopeAndCount(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityChannel, 1); err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := &GotdClient{db: db, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		switch v := in.(type) {
		case *tg.ChannelsGetMessagesRequest:
			if v.Channel.(*tg.InputChannel).ChannelID != 7 {
				t.Fatal("marked ID sent to Telegram")
			}
			out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 7}}}}
		case *tg.ChannelsDeleteMessagesRequest:
			*out.(*tg.MessagesAffectedMessages) = tg.MessagesAffectedMessages{PtsCount: 99}
		default:
			t.Fatalf("unexpected %T", in)
		}
		return nil
	}))}
	req := DeleteMessagesReq{ChatID: peerid.Channel(7), MessageIDs: []int64{3}}
	if _, err := g.DeleteMessages(context.Background(), req); err == nil || calls != 0 {
		t.Fatal("local-only channel deletion reached RPC")
	}
	req.ForEveryone = true
	got, err := g.DeleteMessages(context.Background(), req)
	if err != nil || got.Deleted != 1 || got.PtsCount != 99 {
		t.Fatalf("count %+v err %v", got, err)
	}
}
func TestSendResultRequiresMatchingRandomID(t *testing.T) {
	id, err := sentMessageID(&tg.UpdatesCombined{Updates: []tg.UpdateClass{&tg.UpdateMessageID{RandomID: 71, ID: 3}, &tg.UpdateNewMessage{Message: &tg.Message{ID: 99}}}}, 71)
	if err != nil || id != 3 {
		t.Fatal("wrong correlation")
	}
	_, err = sentMessageID(&tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{RandomID: 72, ID: 3}}}, 71)
	var committed *safety.CommittedWrite
	if !errors.As(err, &committed) {
		t.Fatal("unmatched response reported success")
	}
}

func TestKickReportsPartialCommitAndDoesNotClearExistingRestrictions(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unban failure", true: "existing restrictions"}[restricted], func(t *testing.T) {
			db := updateTestDB(t)
			store.UpsertEntity(db, 7, store.EntityChannel, 42)
			store.UpsertEntity(db, 8, store.EntityUser, 43)
			writes := 0
			g := &GotdClient{db: db, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch r := in.(type) {
				case *tg.ChannelsGetParticipantRequest:
					if restricted {
						out.(*tg.ChannelsChannelParticipant).Participant = &tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 8}, BannedRights: tg.ChatBannedRights{SendMessages: true}}
					} else {
						out.(*tg.ChannelsChannelParticipant).Participant = &tg.ChannelParticipant{UserID: 8}
					}
				case *tg.ChannelsEditBannedRequest:
					writes++
					if writes == 1 {
						if !r.BannedRights.ViewMessages {
							t.Fatal("kick did not remove member")
						}
						out.(*tg.UpdatesBox).Updates = &tg.Updates{}
					} else {
						if r.BannedRights.ViewMessages {
							t.Fatal("unban retained view restriction")
						}
						return errors.New("response lost")
					}
				default:
					t.Fatalf("unexpected %T", in)
				}
				return nil
			}))}
			_, err := g.AdminAction(context.Background(), AdminActionReq{Action: "kick", ChatID: peerid.Channel(7), UserID: 8})
			if restricted {
				if err == nil || writes != 0 {
					t.Fatal("existing restrictions altered")
				}
			} else {
				var committed *safety.CommittedWrite
				if !errors.As(err, &committed) || writes != 2 {
					t.Fatalf("partial kick hidden: %v", err)
				}
			}
		})
	}
}
