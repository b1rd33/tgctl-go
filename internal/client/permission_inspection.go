package client

import (
	"context"
	"time"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/tg"
)

func (g *GotdClient) GetChatPermissions(ctx context.Context, chatID, userID int64) (PermissionInfo, error) {
	if userID < 0 {
		return PermissionInfo{}, safety.NewBadArgs("permission subject must be a user")
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return PermissionInfo{}, err
	}
	switch peer.(type) {
	case *tg.InputPeerChannel, *tg.InputPeerChat:
	default:
		return PermissionInfo{}, safety.NewBadArgs("chat-permissions requires a group or channel")
	}
	chats, err := g.GetChatsInfo(ctx, []int64{chatID})
	if err != nil {
		return PermissionInfo{}, err
	}
	if len(chats) != 1 {
		return PermissionInfo{}, safety.NewBadArgs("requested peer metadata was not returned by Telegram")
	}
	subject := userID
	if subject == 0 {
		subject = g.selfID
	}
	now := time.Now()
	info := PermissionInfo{Chat: chats[0], UserID: subject, Role: "unknown", Advisory: true, FreshAt: now.UTC().Format(time.RFC3339)}
	defaultsApply := true
	info.DefaultRestrictionsApply = &defaultsApply
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		channel := &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}
		full, err := g.api.ChannelsGetFullChannel(ctx, channel)
		if err != nil {
			return PermissionInfo{}, mapRPCErr(err)
		}
		if full == nil {
			return PermissionInfo{}, safety.NewBadArgs("Telegram did not return channel information")
		}
		details, ok := full.FullChat.(*tg.ChannelFull)
		if !ok || details.ID != p.ChannelID {
			return PermissionInfo{}, safety.NewBadArgs("Telegram returned different channel information")
		}
		info.Chat.SlowmodeSeconds = details.SlowmodeSeconds
		info.Chat.SlowmodeNextSendDate = int64(details.SlowmodeNextSendDate)
		info.Chat.SlowmodeKnown = true
		if threshold, enabled := details.GetBoostsUnrestrict(); enabled && threshold > 0 {
			if subject != g.selfID || subject == 0 {
				info.DefaultRestrictionsApply = nil
			} else {
				defaultsApply = details.BoostsApplied < threshold
			}
		}
		var target tg.InputPeerClass = &tg.InputPeerSelf{}
		if userID != 0 && userID != g.selfID {
			target, err = g.peerFromChatID(ctx, userID)
			if err != nil {
				return PermissionInfo{}, err
			}
		}
		participant, err := g.api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{Channel: channel, Participant: target})
		if err != nil {
			return PermissionInfo{}, mapRPCErr(err)
		}
		if participant == nil {
			return PermissionInfo{}, safety.NewBadArgs("Telegram did not return participant information")
		}
		info.Role, info.AdminRights, info.BannedRights = participantPermission(participant.Participant, now.Unix())
	case *tg.InputPeerChat:
		if subject <= 0 {
			return PermissionInfo{}, safety.NewBadArgs("authenticated user identity is unavailable")
		}
		full, err := g.api.MessagesGetFullChat(ctx, p.ChatID)
		if err != nil {
			return PermissionInfo{}, mapRPCErr(err)
		}
		if full == nil {
			return PermissionInfo{}, safety.NewBadArgs("Telegram did not return group information")
		}
		details, ok := full.FullChat.(*tg.ChatFull)
		if !ok || details.ID != p.ChatID {
			return PermissionInfo{}, safety.NewBadArgs("Telegram returned different group information")
		}
		info.Role = basicParticipantRole(details.Participants, subject)
		if subject == g.selfID && info.Role == "admin" {
			info.AdminRights = info.Chat.AdminRights
		}
	}
	if info.Role == "creator" || info.Role == "admin" {
		defaultsApply = false
		info.DefaultRestrictionsApply = &defaultsApply
	}
	info.Effective = effectivePermissionSnapshot(info, now.Unix())
	return info, nil
}

func basicParticipantRole(participants tg.ChatParticipantsClass, userID int64) string {
	role := func(p tg.ChatParticipantClass) string {
		switch v := p.(type) {
		case *tg.ChatParticipantCreator:
			if v.UserID == userID {
				return "creator"
			}
		case *tg.ChatParticipantAdmin:
			if v.UserID == userID {
				return "admin"
			}
		case *tg.ChatParticipant:
			if v.UserID == userID {
				return "member"
			}
		}
		return "unknown"
	}
	switch p := participants.(type) {
	case *tg.ChatParticipants:
		for _, member := range p.Participants {
			if result := role(member); result != "unknown" {
				return result
			}
		}
		return "left"
	case *tg.ChatParticipantsForbidden:
		if self, ok := p.GetSelfParticipant(); ok {
			return role(self)
		}
	}
	return "unknown"
}

func participantPermission(participant tg.ChannelParticipantClass, now int64) (string, *tg.ChatAdminRights, *tg.ChatBannedRights) {
	switch p := participant.(type) {
	case *tg.ChannelParticipantCreator:
		return "creator", &p.AdminRights, nil
	case *tg.ChannelParticipantAdmin:
		return "admin", &p.AdminRights, nil
	case *tg.ChannelParticipantBanned:
		if restrictionsExpired(&p.BannedRights, now) {
			if p.Left || p.BannedRights.ViewMessages {
				return "left", nil, &p.BannedRights
			}
			return "member", nil, &p.BannedRights
		}
		if p.BannedRights.ViewMessages {
			return "banned", nil, &p.BannedRights
		}
		if p.Left {
			return "left", nil, &p.BannedRights
		}
		return "restricted", nil, &p.BannedRights
	case *tg.ChannelParticipantLeft:
		return "left", nil, nil
	case *tg.ChannelParticipant, *tg.ChannelParticipantSelf:
		return "member", nil, nil
	default:
		return "unknown", nil, nil
	}
}

func restrictionsExpired(r *tg.ChatBannedRights, now int64) bool {
	return r != nil && r.UntilDate > 0 && int64(r.UntilDate) <= now
}

// Report a permission snapshot, never a promise of server authorization. Absent
// keys remain unknown. Group defaults constrain members, not administrators;
// broadcast posting requires the creator role or the explicit post right.
func effectiveParticipantRights(chat ChatInfo, role string, admin *tg.ChatAdminRights, banned *tg.ChatBannedRights, now int64) map[string]bool {
	out := map[string]bool{}
	if role == "unknown" || role == "left" {
		return out
	}
	creator := role == "creator"
	privileged := creator || role == "admin"
	absent := role == "left" || role == "banned"
	broadcast := chat.Type == "channel"
	if admin != nil || creator || !privileged {
		a := tg.ChatAdminRights{}
		if admin != nil {
			a = *admin
		}
		for key, value := range map[string]bool{
			"change_info": a.ChangeInfo, "delete_messages": a.DeleteMessages, "ban_users": a.BanUsers,
			"invite_users": a.InviteUsers, "pin_messages": a.PinMessages, "manage_topics": a.ManageTopics,
			"post_messages": a.PostMessages, "edit_messages": a.EditMessages, "manage_call": a.ManageCall,
			"add_admins": a.AddAdmins, "post_stories": a.PostStories, "edit_stories": a.EditStories,
			"delete_stories": a.DeleteStories, "manage_direct_messages": a.ManageDirectMessages, "manage_ranks": a.ManageRanks,
		} {
			out[key] = !absent && (creator || value)
		}
	}
	canSend := !absent && (!chat.Gigagroup || privileged) && (!broadcast || creator || admin != nil && admin.PostMessages)
	out["send_messages"] = canSend
	for _, key := range []string{"send_plain", "send_media", "send_photos", "send_videos", "send_roundvideos", "send_audios", "send_voices", "send_docs", "send_stickers", "send_gifs", "send_games", "send_inline", "send_polls", "embed_links"} {
		out[key] = canSend
	}
	if !broadcast && !absent {
		defaults := restrictionFlags(chat.DefaultBannedRights, now)
		personal := restrictionFlags(banned, now)
		if !privileged {
			for key, deny := range defaults {
				if deny {
					out[key] = false
				}
			}
			for key, deny := range personal {
				if deny {
					out[key] = false
				}
			}
		}
		for _, key := range []string{"change_info", "invite_users", "pin_messages", "manage_topics"} {
			allowed := !defaults[key] && (privileged || !personal[key])
			if creator || out[key] || allowed {
				out[key] = true
			} else if !privileged || admin != nil {
				out[key] = false
			}
		}
	}
	if !out["send_messages"] {
		for key := range restrictionFlags(nil, now) {
			if len(key) >= 5 && key[:5] == "send_" || key == "embed_links" {
				out[key] = false
			}
		}
	}
	if !out["send_media"] {
		for _, key := range []string{"send_photos", "send_videos", "send_roundvideos", "send_audios", "send_voices", "send_docs", "send_stickers", "send_gifs", "send_games", "send_inline"} {
			out[key] = false
		}
	}
	if !out["send_plain"] {
		out["embed_links"] = false
	}
	if role == "banned" {
		out["view_messages"] = false
	}
	if !broadcast {
		for _, key := range []string{"post_messages", "edit_messages", "post_stories", "edit_stories", "delete_stories", "manage_direct_messages"} {
			delete(out, key)
		}
	}
	if !chat.Forum {
		delete(out, "manage_topics")
	}
	return out
}

func restrictionFlags(rights *tg.ChatBannedRights, now int64) map[string]bool {
	r := tg.ChatBannedRights{}
	if rights != nil && !restrictionsExpired(rights, now) {
		r = *rights
	}
	return map[string]bool{
		"send_messages": r.SendMessages || r.ViewMessages, "send_plain": r.SendPlain,
		"send_media": r.SendMedia, "send_photos": r.SendPhotos, "send_videos": r.SendVideos,
		"send_roundvideos": r.SendRoundvideos, "send_audios": r.SendAudios, "send_voices": r.SendVoices,
		"send_docs": r.SendDocs, "send_stickers": r.SendStickers, "send_gifs": r.SendGifs,
		"send_games": r.SendGames, "send_inline": r.SendInline, "send_polls": r.SendPolls, "embed_links": r.EmbedLinks,
		"change_info": r.ChangeInfo, "invite_users": r.InviteUsers, "pin_messages": r.PinMessages, "manage_topics": r.ManageTopics,
	}
}

// Boost counts are available for the current account only. When another member
// may bypass defaults, retain only rights identical with and without that bypass.
func effectivePermissionSnapshot(info PermissionInfo, now int64) map[string]bool {
	regular := effectiveParticipantRights(info.Chat, info.Role, info.AdminRights, info.BannedRights, now)
	if info.Role == "creator" || info.Role == "admin" || info.DefaultRestrictionsApply != nil && *info.DefaultRestrictionsApply {
		return regular
	}
	unrestricted := info.Chat
	unrestricted.DefaultBannedRights = nil
	exempt := effectiveParticipantRights(unrestricted, info.Role, info.AdminRights, info.BannedRights, now)
	if info.DefaultRestrictionsApply != nil {
		return exempt
	}
	for key, value := range regular {
		if alternate, ok := exempt[key]; !ok || value != alternate {
			delete(regular, key)
		}
	}
	return regular
}
