package client

import (
	"context"
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestPermissionInspectionUsesSelectedMemberAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name           string
		broadcast      bool
		userID         int64
		participant    tg.ChannelParticipantClass
		defaults       tg.ChatBannedRights
		boostThreshold int
		boosts         int
		unknown        []string
		role           string
		want           map[string]bool
	}{
		{name: "self restrictions", participant: &tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 1}, BannedRights: tg.ChatBannedRights{SendPlain: true}}, role: "restricted", want: map[string]bool{"send_plain": false}},
		{name: "member inherits defaults", userID: 8, participant: &tg.ChannelParticipant{UserID: 8}, defaults: tg.ChatBannedRights{SendPlain: true, SendPhotos: true}, role: "member", want: map[string]bool{"send_plain": false, "send_photos": false, "send_docs": true}},
		{name: "member and default restrictions combine", userID: 8, participant: &tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 8}, BannedRights: tg.ChatBannedRights{SendDocs: true}}, defaults: tg.ChatBannedRights{SendPhotos: true}, role: "restricted", want: map[string]bool{"send_docs": false, "send_photos": false}},
		{name: "group admin ignores sending restrictions", participant: &tg.ChannelParticipantAdmin{UserID: 1, AdminRights: tg.ChatAdminRights{Other: true}}, defaults: tg.ChatBannedRights{SendMessages: true, PinMessages: true}, role: "admin", want: map[string]bool{"send_messages": true, "send_plain": true, "pin_messages": false}},
		{name: "channel subscriber cannot post", broadcast: true, userID: 8, participant: &tg.ChannelParticipant{UserID: 8}, role: "member", want: map[string]bool{"send_messages": false, "send_plain": false, "send_photos": false}},
		{name: "channel admin needs posting right", broadcast: true, participant: &tg.ChannelParticipantAdmin{UserID: 1, AdminRights: tg.ChatAdminRights{DeleteMessages: true}}, role: "admin", want: map[string]bool{"send_messages": false, "delete_messages": true}},
		{name: "creator can post", broadcast: true, participant: &tg.ChannelParticipantCreator{UserID: 1}, role: "creator", want: map[string]bool{"send_messages": true, "delete_messages": true}},
		{name: "banned cannot send", userID: 8, participant: &tg.ChannelParticipantBanned{Peer: &tg.PeerUser{UserID: 8}, BannedRights: tg.ChatBannedRights{ViewMessages: true}}, role: "banned", want: map[string]bool{"send_messages": false, "send_photos": false}},
		{name: "self boost exemption", participant: &tg.ChannelParticipantSelf{UserID: 1}, defaults: tg.ChatBannedRights{SendPlain: true}, boostThreshold: 2, boosts: 2, role: "member", want: map[string]bool{"send_plain": true}},
		{name: "self below boost threshold", participant: &tg.ChannelParticipantSelf{UserID: 1}, defaults: tg.ChatBannedRights{SendPlain: true}, boostThreshold: 2, boosts: 1, role: "member", want: map[string]bool{"send_plain": false}},
		{name: "other user boost exemption unknown", userID: 8, participant: &tg.ChannelParticipant{UserID: 8}, defaults: tg.ChatBannedRights{SendPlain: true}, boostThreshold: 2, boosts: 2, role: "member", unknown: []string{"send_plain", "embed_links"}, want: map[string]bool{"send_docs": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := updateTestDB(t)
			for _, id := range []int64{1, 8} {
				if err := store.UpsertEntity(db, id, store.EntityUser, id*10); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
				t.Fatal(err)
			}
			calls := 0
			g := &GotdClient{db: db, selfID: 1, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch req := in.(type) {
				case *tg.MessagesGetPeerDialogsRequest:
					ch := &tg.Channel{ID: 7, AccessHash: 70, Megagroup: !tc.broadcast, Broadcast: tc.broadcast, Creator: true}
					ch.SetDefaultBannedRights(tc.defaults)
					out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{ch}
				case *tg.ChannelsGetFullChannelRequest:
					full := &tg.ChannelFull{ID: 7, BoostsApplied: tc.boosts}
					if tc.boostThreshold > 0 {
						full.SetBoostsUnrestrict(tc.boostThreshold)
					}
					out.(*tg.MessagesChatFull).FullChat = full
				case *tg.ChannelsGetParticipantRequest:
					calls++
					if tc.userID == 0 {
						if _, ok := req.Participant.(*tg.InputPeerSelf); !ok {
							t.Fatalf("self target %T", req.Participant)
						}
					} else if p, ok := req.Participant.(*tg.InputPeerUser); !ok || p.UserID != tc.userID {
						t.Fatalf("wrong participant %#v", req.Participant)
					}
					out.(*tg.ChannelsChannelParticipant).Participant = tc.participant
				default:
					t.Fatalf("unexpected RPC %T", in)
				}
				return nil
			}))}
			info, err := g.GetChatPermissions(context.Background(), peerid.Channel(7), tc.userID)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || info.Role != tc.role {
				t.Fatalf("participant calls=%d role=%s want=%s", calls, info.Role, tc.role)
			}
			for _, key := range tc.unknown {
				if _, known := info.Effective[key]; known || info.DefaultRestrictionsApply != nil {
					t.Fatalf("unknown boost exemption became a permission decision for %s", key)
				}
			}
			for key, want := range tc.want {
				got, known := info.Effective[key]
				if !known || got != want {
					t.Errorf("%s=%v known=%v want=%v", key, got, known, want)
				}
			}
		})
	}
}

func TestBasicGroupPermissionInspectionDoesNotBorrowOwnRole(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 7, store.EntityChat, 0); err != nil {
		t.Fatal(err)
	}
	fullCalls := 0
	g := &GotdClient{db: db, selfID: 1, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch req := in.(type) {
		case *tg.MessagesGetPeerDialogsRequest:
			ch := &tg.Chat{ID: 7, Creator: true}
			ch.SetDefaultBannedRights(tg.ChatBannedRights{SendPlain: true})
			out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{ch}
		case *tg.MessagesGetFullChatRequest:
			fullCalls++
			if req.ChatID != 7 {
				t.Fatal("wrong basic group")
			}
			out.(*tg.MessagesChatFull).FullChat = &tg.ChatFull{ID: 7, Participants: &tg.ChatParticipants{ChatID: 7, Participants: []tg.ChatParticipantClass{&tg.ChatParticipantCreator{UserID: 1}, &tg.ChatParticipant{UserID: 8}}}}
		default:
			t.Fatalf("unexpected RPC %T", in)
		}
		return nil
	}))}
	info, err := g.GetChatPermissions(context.Background(), peerid.Chat(7), 8)
	if err != nil {
		t.Fatal(err)
	}
	if fullCalls != 1 || info.Role != "member" || info.Effective["send_plain"] {
		t.Fatalf("selected member result=%+v full calls=%d", info, fullCalls)
	}
}

func TestPermissionInspectionExpiryAndUnknownState(t *testing.T) {
	const now int64 = 2000
	for _, tc := range []struct {
		name        string
		participant tg.ChannelParticipantClass
		role        string
	}{
		{"expired restriction", &tg.ChannelParticipantBanned{BannedRights: tg.ChatBannedRights{SendPlain: true, UntilDate: 1999}}, "member"},
		{"expired ban leaves user outside", &tg.ChannelParticipantBanned{BannedRights: tg.ChatBannedRights{ViewMessages: true, UntilDate: 1999}}, "left"},
		{"indefinite restriction", &tg.ChannelParticipantBanned{BannedRights: tg.ChatBannedRights{SendPlain: true}}, "restricted"},
		{"unknown participant", nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role, admin, banned := participantPermission(tc.participant, now)
			if role != tc.role {
				t.Fatalf("role=%s want=%s", role, tc.role)
			}
			out := effectiveParticipantRights(ChatInfo{Type: "supergroup", DefaultBannedRights: &tg.ChatBannedRights{SendPhotos: true}}, role, admin, banned, now)
			if role == "unknown" && len(out) != 0 {
				t.Fatal("unknown participant got invented rights")
			}
			if role == "member" && (!out["send_plain"] || out["send_photos"]) {
				t.Fatalf("expired personal ban must preserve defaults: %#v", out)
			}
			if role == "restricted" && out["send_plain"] {
				t.Fatal("indefinite ban ignored")
			}
		})
	}
	forbidden := &tg.ChatParticipantsForbidden{ChatID: 7}
	forbidden.SetSelfParticipant(&tg.ChatParticipantCreator{UserID: 1})
	if basicParticipantRole(forbidden, 8) != "unknown" {
		t.Fatal("hidden participant list leaked self role into another user")
	}
	if basicParticipantRole(forbidden, 1) != "creator" {
		t.Fatal("explicit self participant was lost")
	}
}

func TestPermissionInspectionRejectsUserAndNonUserSubjectBeforeRPC(t *testing.T) {
	db := updateTestDB(t)
	if err := store.UpsertEntity(db, 1, store.EntityUser, 10); err != nil {
		t.Fatal(err)
	}
	g := &GotdClient{db: db, selfID: 1, api: tg.NewClient(invokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		t.Fatal("invalid target contacted Telegram")
		return nil
	}))}
	for _, pair := range [][2]int64{{1, 0}, {peerid.Channel(7), peerid.Chat(8)}} {
		if _, err := g.GetChatPermissions(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatal("invalid peer accepted")
		}
	}
}

func TestPermissionInspectionErrorsDoNotBecomeMembership(t *testing.T) {
	for _, stage := range []string{"metadata", "full", "participant"} {
		t.Run(stage, func(t *testing.T) {
			db := updateTestDB(t)
			if err := store.UpsertEntity(db, 7, store.EntityChannel, 70); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("synthetic permission read failure")
			g := &GotdClient{db: db, selfID: 1, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch in.(type) {
				case *tg.MessagesGetPeerDialogsRequest:
					if stage == "metadata" {
						return fault
					}
					out.(*tg.MessagesPeerDialogs).Chats = []tg.ChatClass{&tg.Channel{ID: 7, Megagroup: true}}
				case *tg.ChannelsGetFullChannelRequest:
					if stage == "full" {
						return fault
					}
					out.(*tg.MessagesChatFull).FullChat = &tg.ChannelFull{ID: 7}
				case *tg.ChannelsGetParticipantRequest:
					return fault
				default:
					t.Fatalf("unexpected RPC %T", in)
				}
				return nil
			}))}
			info, err := g.GetChatPermissions(context.Background(), peerid.Channel(7), 0)
			if !errors.Is(err, fault) || info.Role != "" {
				t.Fatalf("read failure became permission result: %+v %v", info, err)
			}
		})
	}
}

func TestPermissionInspectionBoostExemptionAndGigagroup(t *testing.T) {
	chat := ChatInfo{Type: "supergroup", DefaultBannedRights: &tg.ChatBannedRights{SendPlain: true, SendPhotos: true}}
	for _, tc := range []struct {
		name                     string
		applies                  *bool
		plainKnown, plainAllowed bool
	}{
		{"defaults apply", func() *bool { v := true; return &v }(), true, false},
		{"own boost exemption", func() *bool { v := false; return &v }(), true, true},
		{"other member boost count unknown", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := effectivePermissionSnapshot(PermissionInfo{Chat: chat, Role: "restricted", BannedRights: &tg.ChatBannedRights{SendDocs: true}, DefaultRestrictionsApply: tc.applies}, 2000)
			value, known := out["send_plain"]
			if known != tc.plainKnown || value != tc.plainAllowed {
				t.Fatalf("plain=%v known=%v", value, known)
			}
			if value, known := out["send_docs"]; !known || value {
				t.Fatal("boost exemption bypassed personal restriction")
			}
		})
	}
	out := effectiveParticipantRights(ChatInfo{Type: "supergroup", Gigagroup: true}, "member", nil, nil, 2000)
	if out["send_messages"] || out["send_plain"] {
		t.Fatal("gigagroup member can post")
	}
}
