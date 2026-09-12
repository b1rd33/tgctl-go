package client

import (
	"context"
	"fmt"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/tg"
)

func dialogEntityInfo(users []tg.UserClass, chats []tg.ChatClass) map[int64]ChatInfo {
	out := map[int64]ChatInfo{}
	for _, u := range users {
		if v, ok := u.(*tg.User); ok {
			out[v.ID] = ChatInfo{ID: v.ID, Type: "user", Title: DisplayName(v.FirstName, v.LastName, v.Username, v.ID), Username: v.Username, Source: "telegram"}
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			kind := "channel"
			if v.Megagroup {
				kind = "supergroup"
			}
			out[peerid.Channel(v.ID)] = ChatInfo{ID: peerid.Channel(v.ID), Type: kind, Title: v.Title, Username: v.Username, Source: "telegram", Creator: v.Creator, DefaultBannedRights: &v.DefaultBannedRights, AdminRights: &v.AdminRights}
		case *tg.Chat:
			out[peerid.Chat(v.ID)] = ChatInfo{ID: peerid.Chat(v.ID), Type: "group", Title: v.Title, Source: "telegram", Creator: v.Creator, DefaultBannedRights: &v.DefaultBannedRights, AdminRights: &v.AdminRights}
		}
	}
	return out
}
func (g *GotdClient) cacheDialogEntities(users []tg.UserClass, chats []tg.ChatClass) error {
	if g.db == nil || g.updateStore == nil {
		return nil
	}
	for _, u := range users {
		if v, ok := u.(*tg.User); ok && !v.Min {
			if err := store.UpsertEntity(g.db, v.ID, store.EntityUser, v.AccessHash); err != nil {
				return err
			}
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			if !v.Min {
				if err := store.UpsertEntity(g.db, v.ID, store.EntityChannel, v.AccessHash); err != nil {
					return err
				}
			}
		case *tg.Chat:
			if err := store.UpsertEntity(g.db, v.ID, store.EntityChat, 0); err != nil {
				return err
			}
		}
	}
	return nil
}
func (g *GotdClient) discoverDialogs(ctx context.Context, limit int) ([]ChatInfo, error) {
	if _, err := defaultedTelegramInt32Limit(limit, 200, "limit"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 10000 {
		return nil, safety.NewBadArgs("dialog limit exceeds 10000")
	}
	out := make([]ChatInfo, 0, min(limit, 100))
	seen := map[int64]bool{}
	for _, folder := range []int{0, 1} {
		req := &tg.MessagesGetDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}}
		req.SetFolderID(folder)
		for len(out) < limit {
			req.Limit = min(100, limit-len(out))
			response, err := g.api.MessagesGetDialogs(ctx, req)
			if err != nil {
				return nil, mapRPCErr(err)
			}
			var dialogs []tg.DialogClass
			var users []tg.UserClass
			var chats []tg.ChatClass
			var messages []tg.MessageClass
			complete := false
			switch p := response.(type) {
			case *tg.MessagesDialogs:
				dialogs = p.Dialogs
				users = p.Users
				chats = p.Chats
				messages = p.Messages
				complete = true
			case *tg.MessagesDialogsSlice:
				dialogs = p.Dialogs
				users = p.Users
				chats = p.Chats
				messages = p.Messages
			default:
				return nil, fmt.Errorf("unexpected dialog response %T", response)
			}
			if err := g.cacheDialogEntities(users, chats); err != nil {
				return nil, err
			}
			entities := dialogEntityInfo(users, chats)
			var last *tg.Dialog
			for _, d := range dialogs {
				v, ok := d.(*tg.Dialog)
				if !ok {
					continue
				}
				last = v
				id := peerID(v.Peer)
				info, ok := entities[id]
				if !ok {
					return nil, safety.NewBadArgs("dialog entity metadata is unavailable")
				}
				info.ReadInboxMaxID = v.ReadInboxMaxID
				info.UnreadCount = v.UnreadCount
				info.FolderID = folder
				info.TopMessageID = v.TopMessage
				info.ReadStateKnown = true
				if !seen[id] {
					seen[id] = true
					out = append(out, info)
				}
				if g.updateStore != nil {
					if _, err := g.db.Exec(`INSERT INTO tg_chats(chat_id,type,title,username) VALUES(?,?,?,?) ON CONFLICT(chat_id) DO UPDATE SET type=excluded.type,title=excluded.title,username=excluded.username`, id, info.Type, info.Title, info.Username); err != nil {
						return nil, err
					}
					if _, err := g.db.Exec(`INSERT INTO tg_read_state(chat_id,max_id) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET max_id=MAX(max_id,excluded.max_id),updated_at=CURRENT_TIMESTAMP`, id, v.ReadInboxMaxID); err != nil {
						return nil, err
					}
				}
			}
			if complete || len(dialogs) < req.Limit || last == nil || len(out) >= limit {
				break
			}
			offset, err := inputPeerForDialog(last.Peer, users, chats)
			if err != nil {
				return nil, err
			}
			if req.OffsetID == last.TopMessage && sameInputPeer(req.OffsetPeer, offset) {
				return nil, safety.NewBadArgs("dialog pagination did not advance")
			}
			req.OffsetPeer = offset
			req.OffsetID = last.TopMessage
			req.OffsetDate = 0
			req.ExcludePinned = true
			for _, mc := range messages {
				switch m := mc.(type) {
				case *tg.Message:
					if m.ID == last.TopMessage && peerID(m.PeerID) == peerID(last.Peer) {
						req.OffsetDate = m.Date
					}
				case *tg.MessageService:
					if m.ID == last.TopMessage && peerID(m.PeerID) == peerID(last.Peer) {
						req.OffsetDate = m.Date
					}
				}
			}
		}
	}
	return out, nil
}
func inputPeerForDialog(peer tg.PeerClass, users []tg.UserClass, chats []tg.ChatClass) (tg.InputPeerClass, error) {
	switch p := peer.(type) {
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, nil
	case *tg.PeerUser:
		for _, u := range users {
			if v, ok := u.(*tg.User); ok && v.ID == p.UserID && !v.Min {
				return &tg.InputPeerUser{UserID: v.ID, AccessHash: v.AccessHash}, nil
			}
		}
	case *tg.PeerChannel:
		for _, c := range chats {
			if v, ok := c.(*tg.Channel); ok && v.ID == p.ChannelID && !v.Min {
				return &tg.InputPeerChannel{ChannelID: v.ID, AccessHash: v.AccessHash}, nil
			}
		}
	}
	return nil, safety.NewBadArgs("dialog pagination lacks a usable peer access hash")
}
func (g *GotdClient) ListPinnedMessages(ctx context.Context, chatID int64) ([]PinnedMessage, error) {
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	response, err := g.api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: peer, Filter: &tg.InputMessagesFilterPinned{}, Limit: 100})
	if err != nil {
		return nil, mapRPCErr(err)
	}
	result := make([]PinnedMessage, 0)
	for _, m := range messagesFromHistoryResp(response) {
		if v, ok := m.(*tg.Message); ok && !expiringMessage(v) {
			result = append(result, PinnedMessage{MessageID: int64(v.ID), ChatID: chatID, Text: v.Message, Date: timeFromUnix(v.Date)})
		}
	}
	return result, nil
}
