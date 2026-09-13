package client

import (
	"context"
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestSetPeerFolderRoutesTypedPeersAndArchiveIDs(t *testing.T) {
	tests := []struct {
		name     string
		chatID   int64
		kind     store.EntityKind
		access   int64
		folderID int
		check    func(t *testing.T, peer tg.InputPeerClass)
	}{
		{name: "user archive", chatID: 7, kind: store.EntityUser, access: 70, folderID: 1, check: func(t *testing.T, peer tg.InputPeerClass) {
			p, ok := peer.(*tg.InputPeerUser)
			if !ok || p.UserID != 7 || p.AccessHash != 70 {
				t.Fatalf("peer=%#v", peer)
			}
		}},
		{name: "group inbox", chatID: peerid.Chat(8), kind: store.EntityChat, folderID: 0, check: func(t *testing.T, peer tg.InputPeerClass) {
			p, ok := peer.(*tg.InputPeerChat)
			if !ok || p.ChatID != 8 {
				t.Fatalf("peer=%#v", peer)
			}
		}},
		{name: "channel archive", chatID: peerid.Channel(9), kind: store.EntityChannel, access: 90, folderID: 1, check: func(t *testing.T, peer tg.InputPeerClass) {
			p, ok := peer.(*tg.InputPeerChannel)
			if !ok || p.ChannelID != 9 || p.AccessHash != 90 {
				t.Fatalf("peer=%#v", peer)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := updateTestDB(t)
			if err := store.UpsertEntity(db, peerid.Raw(tt.chatID), tt.kind, tt.access); err != nil {
				t.Fatal(err)
			}
			g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				req, ok := in.(*tg.FoldersEditPeerFoldersRequest)
				if !ok || len(req.FolderPeers) != 1 || req.FolderPeers[0].FolderID != tt.folderID {
					t.Fatalf("request=%#v", in)
				}
				tt.check(t, req.FolderPeers[0].Peer)
				out.(*tg.UpdatesBox).Updates = &tg.Updates{}
				return nil
			}))}
			if err := g.SetPeerFolder(context.Background(), PeerFolderReq{ChatID: tt.chatID, FolderID: tt.folderID}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetPeerFolderRejectsInvalidFolderBeforeRPC(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityUser, 70); err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return nil
	}))}
	var badArgs *safety.BadArgs
	if err := g.SetPeerFolder(context.Background(), PeerFolderReq{ChatID: 7, FolderID: 2}); !errors.As(err, &badArgs) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestSetPeerFolderMapsDeniedPeer(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 9, store.EntityChannel, 90); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, resolvedPeers: map[int64]tg.InputPeerClass{}, api: tg.NewClient(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		return tgerr.New(403, "CHANNEL_PRIVATE")
	}))}
	var denied *safety.PermissionDenied
	err := g.SetPeerFolder(context.Background(), PeerFolderReq{ChatID: peerid.Channel(9), FolderID: 1})
	if !errors.As(err, &denied) {
		t.Fatalf("err=%T %v, want permission denied", err, err)
	}
	var notFound *resolve.NotFound
	if errors.As(err, &notFound) {
		t.Fatal("denied peer was misclassified as not found")
	}
}
