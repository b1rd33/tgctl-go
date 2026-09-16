package client

import (
	"context"
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestSetPeerNotifySettingsRoutesTypedPeersAndOnlyMuteField(t *testing.T) {
	tests := []struct {
		name   string
		chatID int64
		kind   store.EntityKind
		access int64
	}{
		{name: "user", chatID: 7, kind: store.EntityUser, access: 70},
		{name: "group", chatID: peerid.Chat(8), kind: store.EntityChat},
		{name: "channel", chatID: peerid.Channel(9), kind: store.EntityChannel, access: 90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := updateTestDB(t)
			if err := store.UpsertEntity(db, peerid.Raw(tt.chatID), tt.kind, tt.access); err != nil {
				t.Fatal(err)
			}
			g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				req, ok := in.(*tg.AccountUpdateNotifySettingsRequest)
				if !ok {
					t.Fatalf("request=%T", in)
				}
				peer, ok := req.Peer.(*tg.InputNotifyPeer)
				if !ok || peer.Peer == nil {
					t.Fatalf("notify peer=%#v", req.Peer)
				}
				muteUntil, present := req.Settings.GetMuteUntil()
				if !present || muteUntil != 123456789 || req.Settings.Flags != 1<<2 {
					t.Fatalf("settings=%#v flags=%v", req.Settings, req.Settings.Flags)
				}
				out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
				return nil
			}))}
			if err := g.SetPeerNotifySettings(context.Background(), PeerNotifySettingsReq{ChatID: tt.chatID, MuteUntil: 123456789}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetPeerNotifySettingsExplicitlyUnmutesAndRejectsInvalidInput(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		req := in.(*tg.AccountUpdateNotifySettingsRequest)
		value, present := req.Settings.GetMuteUntil()
		if !present || value != 0 {
			t.Fatalf("unmute settings=%#v", req.Settings)
		}
		out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		return nil
	}))}
	if err := g.SetPeerNotifySettings(context.Background(), PeerNotifySettingsReq{ChatID: 7}); err != nil {
		t.Fatal(err)
	}
	var badArgs *safety.BadArgs
	if err := g.SetPeerNotifySettings(context.Background(), PeerNotifySettingsReq{ChatID: 7, MuteUntil: -1}); !errors.As(err, &badArgs) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestSetPeerNotifySettingsTreatsFalseAsDefinitiveRejection(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.BoolBox).Bool = &tg.BoolFalse{}
		return nil
	}))}
	var rejected *safety.DefinitiveRejection
	if err := g.SetPeerNotifySettings(context.Background(), PeerNotifySettingsReq{ChatID: 7, MuteUntil: 1}); !errors.As(err, &rejected) {
		t.Fatalf("err=%T %v", err, err)
	}
}
