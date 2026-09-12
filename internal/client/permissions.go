package client

import (
	"context"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/tg"
	"strings"
)

func ValidatePermissions(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "restrict", "read-only", "send-messages":
		return nil
	}
	return safety.NewBadArgs("permissions must be exactly read-only, restrict, or send-messages")
}
func patchBannedRights(r tg.ChatBannedRights, value string) (tg.ChatBannedRights, error) {
	if err := ValidatePermissions(value); err != nil {
		return r, err
	}
	banned := strings.ToLower(strings.TrimSpace(value)) != "send-messages"
	// gotd SetFlags only sets bits; explicitly clear the fields we replace.
	for _, bit := range []int{1, 2, 3, 4, 5, 6, 7, 8, 19, 20, 21, 22, 23, 24, 25} {
		r.Flags.Unset(bit)
	}
	r.SendMessages = banned
	r.SendMedia = banned
	r.SendStickers = banned
	r.SendGifs = banned
	r.SendGames = banned
	r.SendInline = banned
	r.SendPolls = banned
	r.EmbedLinks = banned
	r.SendPhotos = banned
	r.SendVideos = banned
	r.SendRoundvideos = banned
	r.SendAudios = banned
	r.SendVoices = banned
	r.SendDocs = banned
	r.SendPlain = banned
	return r, nil
}
func (g *GotdClient) defaultBannedRights(ctx context.Context, peer tg.InputPeerClass) (tg.ChatBannedRights, error) {
	var chats []tg.ChatClass
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		resp, err := g.api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}})
		if err != nil {
			return tg.ChatBannedRights{}, mapRPCErr(err)
		}
		chats = resp.GetChats()
	case *tg.InputPeerChat:
		resp, err := g.api.MessagesGetChats(ctx, []int64{p.ChatID})
		if err != nil {
			return tg.ChatBannedRights{}, mapRPCErr(err)
		}
		chats = resp.GetChats()
	default:
		return tg.ChatBannedRights{}, safety.NewBadArgs("default permissions require a group or channel")
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			return v.DefaultBannedRights, nil
		case *tg.Chat:
			return v.DefaultBannedRights, nil
		}
	}
	return tg.ChatBannedRights{}, safety.NewBadArgs("could not read existing default permissions")
}
func patchFolderPeers(original *tg.DialogFilter, include, exclude []tg.InputPeerClass) *tg.DialogFilter {
	result := *original
	result.IncludePeers = append([]tg.InputPeerClass(nil), original.IncludePeers...)
	result.ExcludePeers = append([]tg.InputPeerClass(nil), original.ExcludePeers...)
	result.PinnedPeers = append([]tg.InputPeerClass(nil), original.PinnedPeers...)
	remove := func(peers []tg.InputPeerClass, target tg.InputPeerClass) []tg.InputPeerClass {
		out := peers[:0]
		for _, p := range peers {
			if !sameInputPeer(p, target) {
				out = append(out, p)
			}
		}
		return out
	}
	add := func(peers []tg.InputPeerClass, target tg.InputPeerClass) []tg.InputPeerClass {
		for _, p := range peers {
			if sameInputPeer(p, target) {
				return peers
			}
		}
		return append(peers, target)
	}
	for _, p := range include {
		result.ExcludePeers = remove(result.ExcludePeers, p)
		result.IncludePeers = add(result.IncludePeers, p)
	}
	for _, p := range exclude {
		result.IncludePeers = remove(result.IncludePeers, p)
		result.PinnedPeers = remove(result.PinnedPeers, p)
		result.ExcludePeers = add(result.ExcludePeers, p)
	}
	return &result
}
func sameInputPeer(a, b tg.InputPeerClass) bool {
	switch p := a.(type) {
	case *tg.InputPeerUser:
		q, ok := b.(*tg.InputPeerUser)
		return ok && p.UserID == q.UserID
	case *tg.InputPeerChat:
		q, ok := b.(*tg.InputPeerChat)
		return ok && p.ChatID == q.ChatID
	case *tg.InputPeerChannel:
		q, ok := b.(*tg.InputPeerChannel)
		return ok && p.ChannelID == q.ChannelID
	}
	return false
}

func ParseAdminRights(value string) (map[string]bool, error) {
	flags := map[string]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "other", "change_info", "delete_messages", "ban_users", "invite_users", "pin_messages", "manage_topics", "post_messages", "edit_messages", "manage_call", "add_admins":
			flags[name] = true
		default:
			return nil, safety.NewBadArgs("unknown admin right; use the documented comma-separated --rights values")
		}
	}
	return flags, nil
}
func adminRightsFromFlags(f map[string]bool) tg.ChatAdminRights {
	if len(f) == 0 {
		return tg.ChatAdminRights{Other: true}
	}
	return tg.ChatAdminRights{Other: f["other"], ChangeInfo: f["change_info"], DeleteMessages: f["delete_messages"], BanUsers: f["ban_users"], InviteUsers: f["invite_users"], PinMessages: f["pin_messages"], ManageTopics: f["manage_topics"], PostMessages: f["post_messages"], EditMessages: f["edit_messages"], ManageCall: f["manage_call"], AddAdmins: f["add_admins"]}
}
