package client

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// GotdClient is the production Client implementation backed by gotd/td.
//
// gotd/td's telegram.Client.Run is blocking and owns the connection
// lifecycle, so we run it in a background goroutine and proxy API calls in.
// Close() cancels Run and waits for the goroutine to exit, ensuring the
// session file flushes cleanly.
type GotdClient struct {
	api    *tg.Client
	tgc    *telegram.Client
	cancel context.CancelFunc
	done   chan error
	db     *sql.DB // per-account entity cache; may be nil for ephemeral clients
}

// AuthPrompt is the interactive callback set used during `tg login`. Each
// callback is allowed to read from stdin / write to stderr.
type AuthPrompt struct {
	Phone     func() (string, error)
	Code      func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error)
	Password  func(ctx context.Context) (string, error)
	AcceptTOS func(ctx context.Context, terms tg.HelpTermsOfService) error
}

// FlowFromPrompt converts AuthPrompt into a gotd auth.Flow.
func FlowFromPrompt(p AuthPrompt) auth.Flow {
	return auth.NewFlow(
		auth.CodeOnly(must(p.Phone), &authCodeReader{p: p}),
		auth.SendCodeOptions{},
	)
}

type authCodeReader struct{ p AuthPrompt }

func (r *authCodeReader) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	return r.p.Code(ctx, sentCode)
}

func must(f func() (string, error)) string {
	if f == nil {
		return ""
	}
	v, _ := f()
	return v
}

// LoginOptions configures the login flow.
type LoginOptions struct {
	APIID    int
	APIHash  string
	Session  string
	Prompt   AuthPrompt
}

// Login runs an interactive auth flow that persists a session at
// LoginOptions.Session. It blocks until the user is authorized.
func Login(ctx context.Context, opts LoginOptions) (User, error) {
	if opts.APIID == 0 || opts.APIHash == "" {
		return User{}, safety.NewMissingCredentials("TG_API_ID and TG_API_HASH must be set")
	}
	storage := &session.FileStorage{Path: opts.Session}
	client := telegram.NewClient(opts.APIID, opts.APIHash, telegram.Options{
		SessionStorage: storage,
	})
	var me User
	err := client.Run(ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(
			fullAuthenticator{p: opts.Prompt},
			auth.SendCodeOptions{},
		)
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		me = userFromSelf(self)
		return nil
	})
	if err != nil {
		return User{}, mapAuthErr(err)
	}
	return me, nil
}

// fullAuthenticator implements gotd's auth.UserAuthenticator using AuthPrompt.
type fullAuthenticator struct{ p AuthPrompt }

func (a fullAuthenticator) Phone(_ context.Context) (string, error) {
	if a.p.Phone == nil {
		return "", errors.New("no phone prompt provided")
	}
	return a.p.Phone()
}

func (a fullAuthenticator) Password(ctx context.Context) (string, error) {
	if a.p.Password == nil {
		return "", errors.New("2FA password requested but no prompt provided")
	}
	return a.p.Password(ctx)
}

func (a fullAuthenticator) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	if a.p.Code == nil {
		return "", errors.New("no code prompt provided")
	}
	return a.p.Code(ctx, sentCode)
}

func (a fullAuthenticator) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	if a.p.AcceptTOS == nil {
		return nil
	}
	return a.p.AcceptTOS(ctx, tos)
}

func (a fullAuthenticator) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign-up not supported by tgctl-go; create the account on a phone first")
}

// New opens a gotd client against an existing session, starts Run in a
// goroutine, and returns a Client that proxies API calls into it. The
// returned Client must be closed to release the connection.
func New(ctx context.Context, apiID int, apiHash, sessionPath, dbPath string) (*GotdClient, error) {
	if apiID == 0 || apiHash == "" {
		return nil, safety.NewMissingCredentials("TG_API_ID and TG_API_HASH must be set")
	}
	storage := &session.FileStorage{Path: sessionPath}
	tgc := telegram.NewClient(apiID, apiHash, telegram.Options{SessionStorage: storage})

	runCtx, cancel := context.WithCancel(ctx)
	ready := make(chan error, 1)
	done := make(chan error, 1)
	var api *tg.Client
	go func() {
		done <- tgc.Run(runCtx, func(rctx context.Context) error {
			status, err := tgc.Auth().Status(rctx)
			if err != nil {
				ready <- err
				return err
			}
			if !status.Authorized {
				err := safety.NewMissingCredentials(
					"not authorized; run `tg login` first to create a session at " + sessionPath,
				)
				ready <- err
				return err
			}
			api = tgc.API()
			ready <- nil
			<-rctx.Done()
			return rctx.Err()
		})
	}()
	if err := <-ready; err != nil {
		cancel()
		<-done
		return nil, err
	}
	gc := &GotdClient{api: api, tgc: tgc, cancel: cancel, done: done}
	if dbPath != "" {
		if db, err := store.Connect(dbPath); err == nil {
			gc.db = db
		}
	}
	return gc, nil
}

// Close cancels the underlying client.Run and waits for it to drain.
func (g *GotdClient) Close() error {
	g.cancel()
	err := <-g.done
	if g.db != nil {
		_ = g.db.Close()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (g *GotdClient) GetMe(ctx context.Context) (User, error) {
	self, err := g.tgc.Self(ctx)
	if err != nil {
		return User{}, err
	}
	return userFromSelf(self), nil
}

func userFromSelf(self *tg.User) User {
	return User{
		ID:          self.ID,
		Username:    self.Username,
		Phone:       self.Phone,
		FirstName:   self.FirstName,
		LastName:    self.LastName,
		IsBot:       self.Bot,
		DisplayName: DisplayName(self.FirstName, self.LastName, self.Username, self.ID),
	}
}

// resolvePeer turns a chat-id selector into a tg.InputPeerClass. Supports:
//   - @username (via contacts.ResolveUsername; populates the cache)
//   - bare username (no @) — same path
//   - me / self
//
// On a successful username resolution we persist the (id, kind, access_hash)
// triple into tg_entities so subsequent chat_id-keyed operations can build an
// InputPeer from the cache.
func (g *GotdClient) resolvePeer(ctx context.Context, selector string) (tg.InputPeerClass, error) {
	s := strings.TrimSpace(selector)
	if s == "me" || s == "self" {
		return &tg.InputPeerSelf{}, nil
	}
	username := strings.TrimPrefix(s, "@")
	if username == "" || strings.ContainsAny(username, " /\\") {
		return nil, fmt.Errorf("cannot resolve %q: pass an @username", selector)
	}
	resolved, err := g.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, err
	}
	g.persistEntitiesFromResolved(resolved.Users, resolved.Chats)
	switch p := resolved.Peer.(type) {
	case *tg.PeerUser:
		for _, u := range resolved.Users {
			user, ok := u.(*tg.User)
			if ok && user.ID == p.UserID {
				return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, nil
	case *tg.PeerChannel:
		for _, c := range resolved.Chats {
			ch, ok := c.(*tg.Channel)
			if ok && ch.ID == p.ChannelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
			}
		}
	}
	return nil, fmt.Errorf("could not build input peer for resolved username %q", username)
}

// persistEntitiesFromResolved writes user/chat/channel access_hashes into the
// entity cache so future chat_id-keyed operations can run without hitting
// ContactsResolveUsername. No-op when db is nil.
func (g *GotdClient) persistEntitiesFromResolved(users []tg.UserClass, chats []tg.ChatClass) {
	if g.db == nil {
		return
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && !user.Min {
			_ = store.UpsertEntity(g.db, user.ID, store.EntityUser, user.AccessHash)
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			if !v.Min {
				_ = store.UpsertEntity(g.db, v.ID, store.EntityChannel, v.AccessHash)
			}
		case *tg.Chat:
			_ = store.UpsertEntity(g.db, v.ID, store.EntityChat, 0)
		}
	}
}

func (g *GotdClient) SendMessage(ctx context.Context, req SendMessageReq) (SendMessageResp, error) {
	// chat_id-only sends require a cached access_hash. The pipeline already
	// resolved selector→chat_id from tg_chats; for now we accept the
	// selector via the tg_chats.username column when available, or via @username.
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return SendMessageResp{}, err
	}
	r := &tg.MessagesSendMessageRequest{
		Peer:    peer,
		Message: req.Text,
		RandomID: randomID(),
	}
	if req.ReplyTo != 0 {
		r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(req.ReplyTo)}
	}
	if req.NoWebpage {
		r.NoWebpage = true
	}
	if req.Silent {
		r.Silent = true
	}
	updates, err := g.api.MessagesSendMessage(ctx, r)
	if err != nil {
		return SendMessageResp{}, mapRPCErr(err)
	}
	id := extractNewMessageID(updates)
	return SendMessageResp{MessageID: id}, nil
}

// peerFromChatID looks up an entity in tg_entities and builds the right
// InputPeer for it. Returns a clear error pointing at backfill-entities
// when nothing is cached.
func (g *GotdClient) peerFromChatID(_ context.Context, chatID int64) (tg.InputPeerClass, error) {
	if g.db == nil {
		return nil, safety.NewBadArgs("chat_id %d cannot be resolved without an entity cache (no DB available)", chatID)
	}
	kind, accessHash, ok := store.LoadEntity(g.db, chatID)
	if !ok {
		return nil, safety.NewBadArgs(
			"no cached access_hash for chat_id %d; run `tg backfill-entities` once or use `tg send-by-username @name`",
			chatID,
		)
	}
	switch kind {
	case store.EntityUser:
		return &tg.InputPeerUser{UserID: chatID, AccessHash: accessHash}, nil
	case store.EntityChannel:
		return &tg.InputPeerChannel{ChannelID: chatID, AccessHash: accessHash}, nil
	case store.EntityChat:
		return &tg.InputPeerChat{ChatID: chatID}, nil
	}
	return nil, safety.NewBadArgs("unknown entity kind %q for chat_id %d", string(kind), chatID)
}

// SendMessageBySelector resolves selector via Telegram and sends in one call.
// Bypasses the cached-access-hash requirement so users can send today.
func (g *GotdClient) SendMessageBySelector(ctx context.Context, selector, text string, replyTo int64, silent, noWeb bool) (SendMessageResp, error) {
	peer, err := g.resolvePeer(ctx, selector)
	if err != nil {
		return SendMessageResp{}, err
	}
	r := &tg.MessagesSendMessageRequest{
		Peer: peer, Message: text, RandomID: randomID(),
		Silent: silent, NoWebpage: noWeb,
	}
	if replyTo != 0 {
		r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(replyTo)}
	}
	updates, err := g.api.MessagesSendMessage(ctx, r)
	if err != nil {
		return SendMessageResp{}, mapRPCErr(err)
	}
	return SendMessageResp{MessageID: extractNewMessageID(updates)}, nil
}

func (g *GotdClient) UploadFile(ctx context.Context, req UploadFileReq) (UploadFileResp, error) {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return UploadFileResp{}, err
	}
	file, err := uploader.NewUploader(g.api).FromPath(ctx, req.Path)
	if err != nil {
		return UploadFileResp{}, err
	}
	var media tg.InputMediaClass
	if req.Kind == "photo" {
		media = &tg.InputMediaUploadedPhoto{File: file}
	} else {
		attrs := []tg.DocumentAttributeClass{}
		if req.Filename != "" {
			attrs = append(attrs, &tg.DocumentAttributeFilename{FileName: req.Filename})
		}
		switch req.Kind {
		case "voice":
			attrs = append(attrs, &tg.DocumentAttributeAudio{Voice: true})
		case "video":
			attrs = append(attrs, &tg.DocumentAttributeVideo{SupportsStreaming: req.SupportsStreaming})
		}
		media = &tg.InputMediaUploadedDocument{
			File:       file,
			MimeType:   mimeForUpload(req.Kind, req.Path),
			Attributes: attrs,
			ForceFile:  req.Kind == "document",
		}
	}
	r := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		Message:  req.Caption,
		RandomID: randomID(),
		Silent:   req.Silent,
	}
	if req.ReplyTo != 0 {
		r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(req.ReplyTo)}
	}
	updates, err := g.api.MessagesSendMedia(ctx, r)
	if err != nil {
		return UploadFileResp{}, mapRPCErr(err)
	}
	return UploadFileResp{MessageID: extractNewMessageID(updates)}, nil
}

func mimeForUpload(kind, path string) string {
	switch kind {
	case "voice":
		return "audio/ogg"
	case "video":
		return "video/mp4"
	case "photo":
		return "image/jpeg"
	}
	if strings.HasSuffix(strings.ToLower(path), ".txt") {
		return "text/plain"
	}
	return "application/octet-stream"
}

func extractNewMessageID(u tg.UpdatesClass) int64 {
	switch v := u.(type) {
	case *tg.Updates:
		for _, up := range v.Updates {
			switch n := up.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := n.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := n.Message.(*tg.Message); ok {
					return int64(msg.ID)
				}
			case *tg.UpdateMessageID:
				return int64(n.ID)
			}
		}
	case *tg.UpdateShortSentMessage:
		return int64(v.ID)
	case *tg.UpdateShort:
		if u, ok := v.Update.(*tg.UpdateNewMessage); ok {
			if m, ok := u.Message.(*tg.Message); ok {
				return int64(m.ID)
			}
		}
	}
	return 0
}

// ---- write methods backed by gotd ----

func (g *GotdClient) EditMessage(ctx context.Context, req EditMessageReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	_, err = g.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer: peer, ID: int(req.MessageID), Message: req.NewText,
	})
	return mapRPCErr(err)
}

func (g *GotdClient) Forward(ctx context.Context, req ForwardReq) (ForwardResp, error) {
	from, err := g.peerFromChatID(ctx, req.FromChatID)
	if err != nil {
		return ForwardResp{}, err
	}
	to, err := g.peerFromChatID(ctx, req.ToChatID)
	if err != nil {
		return ForwardResp{}, err
	}
	ids := make([]int, len(req.MessageIDs))
	randomIDs := make([]int64, len(req.MessageIDs))
	for i, id := range req.MessageIDs {
		ids[i] = int(id)
		randomIDs[i] = randomID()
	}
	r := &tg.MessagesForwardMessagesRequest{
		FromPeer: from, ToPeer: to, ID: ids, RandomID: randomIDs,
	}
	if req.TopicID != 0 {
		r.TopMsgID = int(req.TopicID)
	}
	updates, err := g.api.MessagesForwardMessages(ctx, r)
	if err != nil {
		return ForwardResp{}, mapRPCErr(err)
	}
	return ForwardResp{MessageIDs: extractAllNewMessageIDs(updates)}, nil
}

func (g *GotdClient) Pin(ctx context.Context, req PinReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	_, err = g.api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
		Peer: peer, ID: int(req.MessageID),
		Silent: req.Silent, Unpin: req.Unpin,
	})
	return mapRPCErr(err)
}

func (g *GotdClient) React(ctx context.Context, req ReactReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	r := &tg.MessagesSendReactionRequest{
		Peer: peer, MsgID: int(req.MessageID), Big: req.Big,
	}
	if req.Emoji != "" {
		r.Reaction = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: req.Emoji}}
	}
	_, err = g.api.MessagesSendReaction(ctx, r)
	return mapRPCErr(err)
}

func (g *GotdClient) MarkRead(ctx context.Context, req MarkReadReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		_, err := g.api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			MaxID:   int(req.UpToID),
		})
		return mapRPCErr(err)
	}
	_, err = g.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
		Peer: peer, MaxID: int(req.UpToID),
	})
	return mapRPCErr(err)
}

func (g *GotdClient) DeleteMessages(ctx context.Context, req DeleteMessagesReq) (DeleteMessagesResp, error) {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return DeleteMessagesResp{}, err
	}
	ids := make([]int, len(req.MessageIDs))
	for i, id := range req.MessageIDs {
		ids[i] = int(id)
	}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		resp, err := g.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			ID:      ids,
		})
		if err != nil {
			return DeleteMessagesResp{}, mapRPCErr(err)
		}
		return DeleteMessagesResp{Deleted: resp.PtsCount}, nil
	}
	resp, err := g.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		ID: ids, Revoke: req.ForEveryone,
	})
	if err != nil {
		return DeleteMessagesResp{}, mapRPCErr(err)
	}
	return DeleteMessagesResp{Deleted: resp.PtsCount}, nil
}

func (g *GotdClient) LeaveChat(ctx context.Context, req LeaveChatReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		_, err := g.api.ChannelsLeaveChannel(ctx, &tg.InputChannel{
			ChannelID: p.ChannelID, AccessHash: p.AccessHash,
		})
		return mapRPCErr(err)
	case *tg.InputPeerChat:
		_, err := g.api.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
			ChatID: p.ChatID, UserID: &tg.InputUserSelf{},
		})
		return mapRPCErr(err)
	}
	return safety.NewBadArgs("leave-chat target must be a group or channel, not a 1-on-1 user")
}

func (g *GotdClient) BlockUser(ctx context.Context, req BlockUserReq) error {
	peer, err := g.peerFromChatID(ctx, req.UserID)
	if err != nil {
		return err
	}
	_, err = g.api.ContactsBlock(ctx, &tg.ContactsBlockRequest{ID: peer})
	return mapRPCErr(err)
}

func (g *GotdClient) UnblockUser(ctx context.Context, req BlockUserReq) error {
	peer, err := g.peerFromChatID(ctx, req.UserID)
	if err != nil {
		return err
	}
	_, err = g.api.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: peer})
	return mapRPCErr(err)
}

func (g *GotdClient) ListSessions(ctx context.Context) ([]SessionRef, error) {
	resp, err := g.api.AccountGetAuthorizations(ctx)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	out := make([]SessionRef, len(resp.Authorizations))
	for i, a := range resp.Authorizations {
		out[i] = SessionRef{
			Hash:       a.Hash,
			DeviceName: a.DeviceModel,
			Platform:   a.Platform,
			IsCurrent:  a.Current,
		}
	}
	return out, nil
}

func (g *GotdClient) TerminateSession(ctx context.Context, req TerminateSessionReq) error {
	_, err := g.api.AccountResetAuthorization(ctx, req.Hash)
	return mapRPCErr(err)
}

func (g *GotdClient) DiscoverDialogs(ctx context.Context, limit int) ([]ChatInfo, error) {
	if limit <= 0 {
		limit = 200
	}
	dialogs, err := g.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      limit,
	})
	if err != nil {
		return nil, mapRPCErr(err)
	}
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch d := dialogs.(type) {
	case *tg.MessagesDialogs:
		users, chats = d.Users, d.Chats
	case *tg.MessagesDialogsSlice:
		users, chats = d.Users, d.Chats
	}
	var out []ChatInfo
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && !user.Min {
			g.persistEntitiesFromResolved([]tg.UserClass{u}, nil)
			out = append(out, ChatInfo{ID: user.ID, Type: "user", Title: DisplayName(user.FirstName, user.LastName, user.Username, user.ID), Username: user.Username})
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			g.persistEntitiesFromResolved(nil, []tg.ChatClass{c})
			kind := "channel"
			if v.Megagroup {
				kind = "supergroup"
			}
			out = append(out, ChatInfo{ID: v.ID, Type: kind, Title: v.Title, Username: v.Username})
		case *tg.Chat:
			g.persistEntitiesFromResolved(nil, []tg.ChatClass{c})
			out = append(out, ChatInfo{ID: v.ID, Type: "group", Title: v.Title})
		}
	}
	return out, nil
}

func (g *GotdClient) SyncContacts(ctx context.Context) ([]ContactInfo, error) {
	resp, err := g.api.ContactsGetContacts(ctx, 0)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	cc, ok := resp.(*tg.ContactsContacts)
	if !ok {
		return nil, nil
	}
	g.persistEntitiesFromResolved(cc.Users, nil)
	out := make([]ContactInfo, 0, len(cc.Users))
	for _, u := range cc.Users {
		if user, ok := u.(*tg.User); ok {
			out = append(out, ContactInfo{
				UserID: user.ID, Phone: user.Phone, FirstName: user.FirstName,
				LastName: user.LastName, Username: user.Username, IsMutual: user.MutualContact,
			})
		}
	}
	return out, nil
}

func (g *GotdClient) BackfillMessages(ctx context.Context, req BackfillReq) ([]BackfillMessage, error) {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	resp, err := g.api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: limit})
	if err != nil {
		return nil, mapRPCErr(err)
	}
	var msgs []tg.MessageClass
	switch m := resp.(type) {
	case *tg.MessagesMessages:
		msgs = m.Messages
	case *tg.MessagesMessagesSlice:
		msgs = m.Messages
	}
	out := make([]BackfillMessage, 0, len(msgs))
	for _, mc := range msgs {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		row := BackfillMessage{
			ChatID: req.ChatID, MessageID: int64(m.ID), Date: timeFromUnix(m.Date),
			Text: m.Message, IsOutgoing: m.Out, HasMedia: m.Media != nil,
		}
		row.SenderID = peerID(m.FromID)
		if m.Media != nil {
			row.MediaType = fmt.Sprintf("%T", m.Media)
		}
		out = append(out, row)
	}
	return out, nil
}

func (g *GotdClient) ListTopics(ctx context.Context, chatID int64, limit int, query string) ([]TopicInfo, error) {
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, safety.NewBadArgs("topics-list target is not a forum supergroup")
	}
	if limit <= 0 {
		limit = 50
	}
	resp, err := g.api.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
		Peer:  ch,
		Q:     query,
		Limit: limit,
	})
	if err != nil {
		return nil, mapRPCErr(err)
	}
	out := make([]TopicInfo, 0, len(resp.Topics))
	for _, t := range resp.Topics {
		switch v := t.(type) {
		case *tg.ForumTopic:
			out = append(out, TopicInfo{
				ID: int64(v.ID), Title: v.Title, IconEmojiID: v.IconEmojiID,
				Closed: v.Closed, Hidden: v.Hidden, TopMessageID: int64(v.TopMessage), UnreadCount: v.UnreadCount,
			})
		}
	}
	return out, nil
}

func (g *GotdClient) CreateTopic(ctx context.Context, req CreateTopicReq) (CreateTopicResp, error) {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return CreateTopicResp{}, err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return CreateTopicResp{}, safety.NewBadArgs("topic-create target is not a forum supergroup")
	}
	r := &tg.MessagesCreateForumTopicRequest{
		Peer:     ch,
		Title:    req.Title,
		RandomID: randomID(),
	}
	if req.IconColor != 0 {
		r.IconColor = req.IconColor
	}
	if req.IconEmojiID != 0 {
		r.IconEmojiID = req.IconEmojiID
	}
	updates, err := g.api.MessagesCreateForumTopic(ctx, r)
	if err != nil {
		return CreateTopicResp{}, mapRPCErr(err)
	}
	return CreateTopicResp{TopicID: firstTopicID(updates), Title: req.Title}, nil
}

func (g *GotdClient) EditTopic(ctx context.Context, req EditTopicReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return safety.NewBadArgs("topic-edit target is not a forum supergroup")
	}
	r := &tg.MessagesEditForumTopicRequest{
		Peer:    ch,
		TopicID: int(req.TopicID),
	}
	if req.Title != "" {
		r.Title = req.Title
	}
	if req.IconEmojiID != 0 {
		r.IconEmojiID = req.IconEmojiID
	}
	_, err = g.api.MessagesEditForumTopic(ctx, r)
	return mapRPCErr(err)
}

func (g *GotdClient) PinTopic(ctx context.Context, req PinTopicReq) error {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return safety.NewBadArgs("topic-pin target is not a forum supergroup")
	}
	_, err = g.api.MessagesUpdatePinnedForumTopic(ctx, &tg.MessagesUpdatePinnedForumTopicRequest{
		Peer:    ch,
		TopicID: int(req.TopicID),
		Pinned:  req.Pinned,
	})
	return mapRPCErr(err)
}

func (g *GotdClient) ListFolders(ctx context.Context) ([]FolderInfo, error) {
	filters, err := g.api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	out := []FolderInfo{{ID: 0, Title: "All Chats", IsDefault: true}}
	for _, f := range filters.Filters {
		if _, ok := f.(*tg.DialogFilterDefault); ok {
			continue
		}
		if df, ok := f.(*tg.DialogFilter); ok {
			out = append(out, FolderInfo{ID: int64(df.ID), Title: df.Title.Text, Emoji: df.Emoticon})
		}
	}
	return out, nil
}

func (g *GotdClient) UpdateFolder(ctx context.Context, req FolderUpdateReq) error {
	filter := &tg.DialogFilter{ID: int(req.ID), Title: tg.TextWithEntities{Text: req.Title}, Emoticon: req.Emoji}
	_, err := g.api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(req.ID), Filter: filter})
	return mapRPCErr(err)
}

func (g *GotdClient) DeleteFolder(ctx context.Context, id int64) error {
	_, err := g.api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(id)})
	return mapRPCErr(err)
}

func (g *GotdClient) ReorderFolders(ctx context.Context, ids []int64) error {
	order := make([]int, len(ids))
	for i, id := range ids {
		order[i] = int(id)
	}
	_, err := g.api.MessagesUpdateDialogFiltersOrder(ctx, order)
	return mapRPCErr(err)
}

func (g *GotdClient) ListPinnedDialogs(ctx context.Context, chatID int64) ([]ChatInfo, error) {
	return nil, safety.NewBadArgs("chat-pinned-list is not supported by gotd adapter yet for chat_id %d", chatID)
}

func firstTopicID(updates tg.UpdatesClass) int64 {
	if v, ok := updates.(*tg.Updates); ok {
		for _, up := range v.Updates {
			if t, ok := up.(*tg.UpdateChannel); ok {
				return t.ChannelID
			}
		}
	}
	return 0
}

func peerID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return v.ChatID
	case *tg.PeerChannel:
		return v.ChannelID
	}
	return 0
}

func timeFromUnix(ts int) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
}

// extractAllNewMessageIDs returns the message ids of every new-message update
// inside an Updates response.
func extractAllNewMessageIDs(u tg.UpdatesClass) []int64 {
	var out []int64
	if v, ok := u.(*tg.Updates); ok {
		for _, up := range v.Updates {
			switch n := up.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := n.Message.(*tg.Message); ok {
					out = append(out, int64(msg.ID))
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := n.Message.(*tg.Message); ok {
					out = append(out, int64(msg.ID))
				}
			case *tg.UpdateMessageID:
				out = append(out, int64(n.ID))
			}
		}
	}
	return out
}

// mapRPCErr classifies a gotd RPC error into the dispatch error taxonomy.
func mapRPCErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "FLOOD_WAIT_"):
		// gotd surfaces FloodWait as *tgerr.Error with code 420.
		return &safety.FloodWait{Seconds: extractFloodSeconds(msg)}
	case strings.Contains(msg, "PREMIUM_ACCOUNT_REQUIRED"):
		return &safety.PremiumRequired{}
	}
	return err
}

func mapAuthErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "PHONE_") || strings.Contains(err.Error(), "AUTH_") {
		return safety.NewMissingCredentials(err.Error())
	}
	return err
}

func extractFloodSeconds(msg string) int {
	idx := strings.LastIndex(msg, "FLOOD_WAIT_")
	if idx < 0 {
		return 0
	}
	rest := msg[idx+len("FLOOD_WAIT_"):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n := 0
	for i := 0; i < end; i++ {
		n = n*10 + int(rest[i]-'0')
	}
	return n
}
