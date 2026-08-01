package client

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/b1rd33/tgctl-go/internal/media"
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
	api               *tg.Client
	albumAPI          albumUploadAPI
	backfillAPI       backfillHistoryAPI
	mediaAPI          mediaDownloadAPI
	fileDownloader    fileDownloader
	destinationOpener destinationOpener
	tgc               *telegram.Client
	cancel            context.CancelFunc
	done              chan error
	db                *sql.DB // per-account entity cache; may be nil for ephemeral clients
	events            chan ListenEvent
}

// albumUploadAPI is the narrow Telegram surface needed by UploadAlbum. It is
// kept beside GotdClient so standalone command-documentation builds that list
// the core client source files still include the interface definition.
type albumUploadAPI interface {
	uploader.Client
	MessagesUploadMedia(context.Context, *tg.MessagesUploadMediaRequest) (tg.MessageMediaClass, error)
	MessagesSendMultiMedia(context.Context, *tg.MessagesSendMultiMediaRequest) (tg.UpdatesClass, error)
}

type backfillHistoryAPI interface {
	MessagesGetHistory(context.Context, *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
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
	APIID   int
	APIHash string
	Session string
	Prompt  AuthPrompt
}

// Login runs an interactive auth flow that persists a session at
// LoginOptions.Session. It blocks until the user is authorized.
func Login(ctx context.Context, opts LoginOptions) (User, error) {
	if err := validateAPIID(opts.APIID); err != nil {
		return User{}, err
	}
	if opts.APIHash == "" {
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
	if err := validateAPIID(apiID); err != nil {
		return nil, err
	}
	storage, err := sessionStorageForMode(ctx, sessionPath, false)
	if err != nil {
		return nil, err
	}
	return newClient(ctx, apiID, apiHash, sessionPath, dbPath, storage)
}

// NewReadonly opens a gotd client with an in-memory snapshot of sessionPath.
// gotd can refresh its session state for the lifetime of the connection, but
// no session data is written back to the real file.
func NewReadonly(ctx context.Context, apiID int, apiHash, sessionPath string) (*GotdClient, error) {
	if err := validateAPIID(apiID); err != nil {
		return nil, err
	}
	storage, err := sessionStorageForMode(ctx, sessionPath, true)
	if errors.Is(err, session.ErrNotFound) {
		return nil, safety.NewMissingCredentials(
			"not authorized; run `tg login` first to create a session at " + sessionPath,
		)
	}
	if err != nil {
		return nil, err
	}
	return newClient(ctx, apiID, apiHash, sessionPath, "", storage)
}

func sessionStorageForMode(ctx context.Context, sessionPath string, readOnly bool) (session.Storage, error) {
	fileStorage := &session.FileStorage{Path: sessionPath}
	if !readOnly {
		return fileStorage, nil
	}
	data, err := fileStorage.LoadSession(ctx)
	if err != nil {
		return nil, err
	}
	memoryStorage := &session.StorageMemory{}
	if err := memoryStorage.StoreSession(ctx, data); err != nil {
		return nil, err
	}
	return memoryStorage, nil
}

func newClient(ctx context.Context, apiID int, apiHash, sessionPath, dbPath string, storage session.Storage) (*GotdClient, error) {
	if err := validateAPIID(apiID); err != nil {
		return nil, err
	}
	if apiHash == "" {
		return nil, safety.NewMissingCredentials("TG_API_ID and TG_API_HASH must be set")
	}
	events := make(chan ListenEvent, 32)
	tgc := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: storage,
		UpdateHandler: telegram.UpdateHandlerFunc(func(ctx context.Context, u tg.UpdatesClass) error {
			for _, event := range listenEventsFromUpdates(u) {
				select {
				case events <- event:
				default:
				}
			}
			return nil
		}),
	})

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
	gc := &GotdClient{
		api: api, mediaAPI: api,
		fileDownloader:    gotdFileDownloader{client: tgc, api: api},
		destinationOpener: atomicDestinationOpener{},
		tgc:               tgc, cancel: cancel, done: done, events: events,
	}
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
	if err := validateOptionalTelegramInt32(req.ReplyTo, "reply_to"); err != nil {
		return SendMessageResp{}, err
	}
	if err := validateOptionalTelegramInt32(req.TopicID, "topic_id"); err != nil {
		return SendMessageResp{}, err
	}
	// chat_id-only sends require a cached access_hash. The pipeline already
	// resolved selector→chat_id from tg_chats; for now we accept the
	// selector via the tg_chats.username column when available, or via @username.
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return SendMessageResp{}, err
	}
	r := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  req.Text,
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
	if err := validateOptionalTelegramInt32(replyTo, "reply_to"); err != nil {
		return SendMessageResp{}, err
	}
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
	if err := validateOptionalTelegramInt32(req.ReplyTo, "reply_to"); err != nil {
		return UploadFileResp{}, err
	}
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
	if err := validatePositiveTelegramInt32(req.MessageID, "message_id"); err != nil {
		return err
	}
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
	if err := validatePositiveTelegramInts32(req.MessageIDs, "message_id"); err != nil {
		return ForwardResp{}, err
	}
	if err := validateOptionalTelegramInt32(req.TopicID, "topic_id"); err != nil {
		return ForwardResp{}, err
	}
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
	if err := validatePositiveTelegramInt32(req.MessageID, "message_id"); err != nil {
		return err
	}
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
	if err := validatePositiveTelegramInt32(req.MessageID, "message_id"); err != nil {
		return err
	}
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
	if err := validateOptionalTelegramInt32(req.UpToID, "up_to_id"); err != nil {
		return err
	}
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
	if err := validatePositiveTelegramInts32(req.MessageIDs, "message_id"); err != nil {
		return DeleteMessagesResp{}, err
	}
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
	var err error
	limit, err = defaultedTelegramInt32Limit(limit, 200, "limit")
	if err != nil {
		return nil, err
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

// messagesFromHistoryResp normalizes the three concrete return types of
// messages.GetHistory into a flat []tg.MessageClass.
//
// Channels and supergroups return *tg.MessagesChannelMessages — without
// that case, backfill silently inserted 0 messages for those chats.
// MessagesMessagesNotModified is the "history hasn't changed since last
// fetch" hint and is intentionally treated as empty.
func messagesFromHistoryResp(resp tg.MessagesMessagesClass) []tg.MessageClass {
	return historyPageFromResp(resp).Messages
}

type historyPage struct {
	Messages   []tg.MessageClass
	Total      int
	TotalKnown bool
}

// historyPageFromResp retains Telegram's total-result metadata so the pager
// can distinguish an exact-full final page from a page with more history.
func historyPageFromResp(resp tg.MessagesMessagesClass) historyPage {
	switch m := resp.(type) {
	case *tg.MessagesMessages:
		if m == nil {
			return historyPage{}
		}
		return historyPage{Messages: m.Messages, Total: len(m.Messages), TotalKnown: true}
	case *tg.MessagesMessagesSlice:
		if m == nil {
			return historyPage{}
		}
		return historyPage{Messages: m.Messages, Total: m.Count, TotalKnown: true}
	case *tg.MessagesChannelMessages:
		if m == nil {
			return historyPage{}
		}
		return historyPage{Messages: m.Messages, Total: m.Count, TotalKnown: true}
	}
	return historyPage{}
}

// pageSize is messages.getHistory's per-call ceiling. Telegram caps the
// returned set at 100 even when you ask for more, so larger requests must
// paginate by OffsetID.
const backfillPageSize = 100

func (g *GotdClient) BackfillMessages(ctx context.Context, req BackfillReq) (BackfillResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = backfillPageSize
	}
	req.Limit = limit
	if limit > MaxBackfillMessages {
		return BackfillResult{}, safety.NewBadArgs("backfill limit %d exceeds maximum %d", limit, MaxBackfillMessages)
	}
	if req.MaxMediaBytes < 0 {
		return BackfillResult{}, safety.NewBadArgs("max_media_bytes cannot be negative")
	}
	if req.DownloadMedia {
		if strings.TrimSpace(req.MediaDir) == "" {
			return BackfillResult{}, safety.NewBadArgs("media_dir cannot be blank when download_media is enabled")
		}
		absMediaDir, err := filepath.Abs(filepath.Clean(req.MediaDir))
		if err != nil {
			return BackfillResult{}, fmt.Errorf("resolve media directory: %w", err)
		}
		req.MediaDir = absMediaDir
	}
	if err := ctx.Err(); err != nil {
		return BackfillResult{}, err
	}
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return BackfillResult{}, err
	}
	historyAPI := g.backfillAPI
	if historyAPI == nil {
		historyAPI = g.api
	}
	if historyAPI == nil {
		return BackfillResult{}, errors.New("Telegram history API is not initialized")
	}
	return g.paginateBackfillHistory(ctx, req,
		func(ctx context.Context, offsetID, pageLimit int) (historyPage, error) {
			resp, err := historyAPI.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:     peer,
				Limit:    pageLimit,
				OffsetID: offsetID,
			})
			if err != nil {
				return historyPage{}, mapRPCErr(err)
			}
			return historyPageFromResp(resp), nil
		}, waitForThrottle)
}

// paginateHistory implements Telegram's 100-message history pagination behind
// test seams for page fetching and sleeping. Throttling happens only after a
// non-empty full page when another request is needed; it never delays the first
// request or runs after a final/partial page.
func paginateHistory(
	ctx context.Context,
	chatID int64,
	limit int,
	throttle time.Duration,
	fetch func(context.Context, int, int) (historyPage, error),
	wait func(context.Context, time.Duration) error,
) ([]BackfillMessage, error) {
	result, err := (&GotdClient{}).paginateBackfillHistory(ctx, BackfillReq{
		ChatID: chatID, Limit: limit, Throttle: throttle,
	}, fetch, wait)
	return result.Messages, err
}

func (g *GotdClient) paginateBackfillHistory(
	ctx context.Context,
	req BackfillReq,
	fetch func(context.Context, int, int) (historyPage, error),
	wait func(context.Context, time.Duration) error,
) (BackfillResult, error) {
	limit := req.Limit
	if limit > MaxBackfillMessages {
		return BackfillResult{Warnings: []string{}}, safety.NewBadArgs("backfill limit %d exceeds maximum %d", limit, MaxBackfillMessages)
	}
	initialCapacity := limit
	if initialCapacity > backfillPageSize {
		initialCapacity = backfillPageSize
	}
	result := BackfillResult{
		Messages: make([]BackfillMessage, 0, initialCapacity),
		Warnings: []string{},
	}
	offsetID := 0
	serverItemsSeen := 0
	seenMessages := make(map[int64]struct{})
	seenAlbums := make(map[int64]struct{})
	for len(result.Messages) < limit {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		want := limit - len(result.Messages)
		if want > backfillPageSize {
			want = backfillPageSize
		}
		page, err := fetch(ctx, offsetID, want)
		if err != nil {
			return result, err
		}
		msgs := page.Messages
		if len(msgs) == 0 {
			break
		}
		minID := 0
		for _, mc := range msgs {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			messageID, hasID := historyMessageID(mc)
			if hasID {
				if minID == 0 || messageID < int64(minID) {
					minID = int(messageID)
				}
				if _, seen := seenMessages[messageID]; seen {
					continue
				}
				seenMessages[messageID] = struct{}{}
			}
			serverItemsSeen++
			m, ok := mc.(*tg.Message)
			if !ok {
				// Other concrete types: MessageEmpty, MessageService.
				// Skip but still update minID so we don't re-fetch.
				continue
			}
			row := BackfillMessage{
				ChatID: req.ChatID, MessageID: int64(m.ID), Date: timeFromUnix(m.Date),
				Text: m.Message, IsOutgoing: m.Out, HasMedia: m.Media != nil, GroupedID: m.GroupedID, MediaDisposition: BackfillMediaNone,
			}
			row.SenderID = peerID(m.FromID)
			if req.DownloadMedia {
				if err := g.backfillMessageMedia(ctx, req, m, &row, &result); err != nil {
					return result, err
				}
			} else if m.Media != nil {
				row.MediaType = fmt.Sprintf("%T", m.Media)
			}
			if m.GroupedID != 0 {
				if _, seen := seenAlbums[m.GroupedID]; !seen {
					seenAlbums[m.GroupedID] = struct{}{}
					result.AlbumsSeen++
				}
			}
			result.Messages = append(result.Messages, row)
			if len(result.Messages) >= limit {
				break
			}
		}
		if minID == 0 || minID == offsetID {
			// Telegram returned only non-Message items or didn't move the
			// cursor; bail to avoid an infinite loop.
			break
		}
		offsetID = minID
		more := len(msgs) == want
		if page.TotalKnown && serverItemsSeen >= page.Total {
			more = false
		}
		if !more {
			break
		}
		if len(result.Messages) < limit && req.Throttle > 0 {
			if err := wait(ctx, req.Throttle); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func historyMessageID(mc tg.MessageClass) (int64, bool) {
	switch m := mc.(type) {
	case *tg.Message:
		if m == nil {
			return 0, false
		}
		return int64(m.ID), true
	case *tg.MessageEmpty:
		if m == nil {
			return 0, false
		}
		return int64(m.ID), true
	case *tg.MessageService:
		if m == nil {
			return 0, false
		}
		return int64(m.ID), true
	default:
		return 0, false
	}
}

func (g *GotdClient) backfillMessageMedia(ctx context.Context, req BackfillReq, message *tg.Message, row *BackfillMessage, result *BackfillResult) error {
	if message.Media == nil {
		return nil
	}
	switch message.Media.(type) {
	case *tg.MessageMediaPhoto, *tg.MessageMediaDocument:
		// Continue below: these are Telegram's downloadable file media classes.
	default:
		row.MediaDisposition = BackfillMediaUnsupported
		appendBackfillMediaOutcome(result, BackfillMediaOutcome{
			ChatID: req.ChatID, MessageID: int64(message.ID), Status: BackfillMediaUnsupported, ErrorCode: "UNSUPPORTED",
		})
		result.Warnings = append(result.Warnings, backfillMediaWarning(req.ChatID, int64(message.ID), "skipped", "UNSUPPORTED"))
		return nil
	}
	extracted, err := extractDownloadMedia(message)
	if err != nil {
		row.MediaDisposition = BackfillMediaMalformed
		appendBackfillMediaOutcome(result, BackfillMediaOutcome{
			ChatID: req.ChatID, MessageID: int64(message.ID), Status: BackfillMediaMalformed, ErrorCode: mediaFailureCode(err),
		})
		result.Warnings = append(result.Warnings, backfillMediaWarning(req.ChatID, int64(message.ID), "failed", mediaFailureCode(err)))
		return nil
	}
	row.MediaIdentity = extracted.Identity
	row.MediaType = extracted.MediaType
	identityName := strings.ReplaceAll(extracted.Identity, ":", "_")
	uniqueName := fmt.Sprintf("%d_%d_%s_%s", req.ChatID, message.ID, identityName, media.SanitizeDownloadName(extracted.Filename))
	resp, err := g.downloadExtractedMessageMedia(ctx, DownloadMediaReq{
		ChatID: req.ChatID, MessageID: int64(message.ID), OutputDir: req.MediaDir,
		MaxBytes: req.MaxMediaBytes, Overwrite: req.OverwriteMedia,
	}, message, extracted, uniqueName)
	if err != nil {
		var committedDownload *CommittedMediaDownloadError
		if errors.As(err, &committedDownload) && committedDownload != nil {
			row.MediaDisposition = BackfillMediaFailed
			appendBackfillMediaOutcome(result, BackfillMediaOutcome{
				ChatID: req.ChatID, MessageID: int64(message.ID), MediaIdentity: extracted.Identity,
				Status: BackfillMediaFailed, MediaType: extracted.MediaType,
				MediaPath: committedDownload.Response.Path, Bytes: committedDownload.Response.Bytes,
				ErrorCode: "COMMITTED", Committed: true,
			})
			result.Warnings = append(result.Warnings, backfillMediaWarning(req.ChatID, int64(message.ID), "failed", "COMMITTED"))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		row.MediaDisposition = BackfillMediaFailed
		appendBackfillMediaOutcome(result, BackfillMediaOutcome{
			ChatID: req.ChatID, MessageID: int64(message.ID), MediaIdentity: extracted.Identity,
			Status: BackfillMediaFailed, MediaType: extracted.MediaType, ErrorCode: mediaFailureCode(err),
		})
		result.Warnings = append(result.Warnings, backfillMediaWarning(req.ChatID, int64(message.ID), "failed", mediaFailureCode(err)))
		return nil
	}
	row.MediaType = resp.MediaType
	row.MediaPath = resp.Path
	status := BackfillMediaDownloaded
	if resp.Skipped {
		status = BackfillMediaSkipped
	}
	row.MediaDisposition = status
	appendBackfillMediaOutcome(result, BackfillMediaOutcome{
		ChatID: req.ChatID, MessageID: int64(message.ID), MediaIdentity: extracted.Identity,
		Status: status, MediaType: resp.MediaType, MediaPath: resp.Path, Bytes: resp.Bytes, Committed: !resp.Skipped,
	})
	return nil
}

func appendBackfillMediaOutcome(result *BackfillResult, outcome BackfillMediaOutcome) {
	result.MediaOutcomes = append(result.MediaOutcomes, outcome)
	switch outcome.Status {
	case BackfillMediaDownloaded:
		result.MediaDownloaded++
	case BackfillMediaSkipped, BackfillMediaUnsupported:
		result.MediaSkipped++
	case BackfillMediaFailed, BackfillMediaMalformed:
		result.MediaFailed++
	}
}

func backfillMediaWarning(chatID, messageID int64, outcome, code string) string {
	return fmt.Sprintf("chat_id=%d message_id=%d media=%s code=%s", chatID, messageID, outcome, code)
}

func mediaFailureCode(err error) string {
	var badArgs *safety.BadArgs
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "CANCELED"
	case errors.Is(err, media.ErrDestinationCommitted):
		return "COMMITTED"
	case errors.Is(err, media.ErrCleanupIncomplete):
		return "CLEANUP_INCOMPLETE"
	case errors.Is(err, media.ErrLimitExceeded):
		return "LIMIT"
	case errors.As(err, &badArgs):
		return "BAD_ARGS"
	default:
		return "TRANSFER"
	}
}

func waitForThrottle(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *GotdClient) ListTopics(ctx context.Context, chatID int64, limit int, query string) ([]TopicInfo, error) {
	var err error
	limit, err = defaultedTelegramInt32Limit(limit, 50, "limit")
	if err != nil {
		return nil, err
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, safety.NewBadArgs("topics-list target is not a forum supergroup")
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
	if err := validateNonNegativeNativeTelegramInt32(req.IconColor, "icon_color"); err != nil {
		return CreateTopicResp{}, err
	}
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
	if err := validatePositiveTelegramInt32(req.TopicID, "topic_id"); err != nil {
		return err
	}
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
	if err := validatePositiveTelegramInt32(req.TopicID, "topic_id"); err != nil {
		return err
	}
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
			out = append(out, folderInfoFromDialogFilter(df))
		}
	}
	return out, nil
}

func (g *GotdClient) UpdateFolder(ctx context.Context, req FolderUpdateReq) error {
	if err := validatePositiveTelegramInt32(req.ID, "folder_id"); err != nil {
		return err
	}
	if existing, ok, err := g.folderInfoByID(ctx, req.ID); err != nil {
		return err
	} else if ok {
		req = mergeFolderUpdate(existing, req)
	}
	includePeers, err := g.inputPeersFromChatIDs(ctx, req.IncludeChatIDs)
	if err != nil {
		return err
	}
	excludePeers, err := g.inputPeersFromChatIDs(ctx, req.ExcludeChatIDs)
	if err != nil {
		return err
	}
	filter := folderFilterFromReq(req, includePeers, excludePeers)
	_, err = g.api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(req.ID), Filter: filter})
	return mapRPCErr(err)
}

func (g *GotdClient) folderInfoByID(ctx context.Context, id int64) (FolderInfo, bool, error) {
	filters, err := g.api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return FolderInfo{}, false, mapRPCErr(err)
	}
	for _, f := range filters.Filters {
		if df, ok := f.(*tg.DialogFilter); ok && int64(df.ID) == id {
			return folderInfoFromDialogFilter(df), true, nil
		}
	}
	return FolderInfo{}, false, nil
}

func (g *GotdClient) inputPeersFromChatIDs(ctx context.Context, ids []int64) ([]tg.InputPeerClass, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	peers := make([]tg.InputPeerClass, 0, len(ids))
	for _, id := range ids {
		peer, err := g.peerFromChatID(ctx, id)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func folderInfoFromDialogFilter(filter *tg.DialogFilter) FolderInfo {
	return FolderInfo{
		ID:             int64(filter.ID),
		Title:          filter.Title.Text,
		Emoji:          filter.Emoticon,
		IncludeChatIDs: inputPeerIDs(filter.IncludePeers),
		ExcludeChatIDs: inputPeerIDs(filter.ExcludePeers),
	}
}

func inputPeerIDs(peers []tg.InputPeerClass) []int64 {
	if len(peers) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(peers))
	for _, peer := range peers {
		switch p := peer.(type) {
		case *tg.InputPeerUser:
			ids = append(ids, p.UserID)
		case *tg.InputPeerChannel:
			ids = append(ids, p.ChannelID)
		case *tg.InputPeerChat:
			ids = append(ids, p.ChatID)
		}
	}
	return ids
}

func mergeFolderUpdate(existing FolderInfo, req FolderUpdateReq) FolderUpdateReq {
	merged := FolderUpdateReq{
		ID:             req.ID,
		Title:          existing.Title,
		Emoji:          existing.Emoji,
		IncludeChatIDs: append([]int64(nil), existing.IncludeChatIDs...),
		ExcludeChatIDs: append([]int64(nil), existing.ExcludeChatIDs...),
	}
	if req.Title != "" {
		merged.Title = req.Title
	}
	if req.Emoji != "" {
		merged.Emoji = req.Emoji
	}
	for _, id := range req.IncludeChatIDs {
		merged.ExcludeChatIDs = removeInt64(merged.ExcludeChatIDs, id)
		merged.IncludeChatIDs = appendUniqueInt64(merged.IncludeChatIDs, id)
	}
	for _, id := range req.ExcludeChatIDs {
		merged.IncludeChatIDs = removeInt64(merged.IncludeChatIDs, id)
		merged.ExcludeChatIDs = appendUniqueInt64(merged.ExcludeChatIDs, id)
	}
	return merged
}

func appendUniqueInt64(ids []int64, id int64) []int64 {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeInt64(ids []int64, id int64) []int64 {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

func folderFilterFromReq(req FolderUpdateReq, includePeers, excludePeers []tg.InputPeerClass) *tg.DialogFilter {
	return &tg.DialogFilter{
		ID:           int(req.ID),
		Title:        tg.TextWithEntities{Text: req.Title},
		Emoticon:     req.Emoji,
		IncludePeers: includePeers,
		ExcludePeers: excludePeers,
	}
}

func (g *GotdClient) DeleteFolder(ctx context.Context, id int64) error {
	if err := validatePositiveTelegramInt32(id, "folder_id"); err != nil {
		return err
	}
	_, err := g.api.MessagesUpdateDialogFilter(ctx, &tg.MessagesUpdateDialogFilterRequest{ID: int(id)})
	return mapRPCErr(err)
}

func (g *GotdClient) ReorderFolders(ctx context.Context, ids []int64) error {
	if err := validatePositiveTelegramInts32(ids, "folder_id"); err != nil {
		return err
	}
	order := make([]int, len(ids))
	for i, id := range ids {
		order[i] = int(id)
	}
	_, err := g.api.MessagesUpdateDialogFiltersOrder(ctx, order)
	return mapRPCErr(err)
}

func (g *GotdClient) ListPinnedDialogs(ctx context.Context, chatID int64) ([]ChatInfo, error) {
	resp, err := g.api.MessagesGetPinnedDialogs(ctx, 0)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	out := make([]ChatInfo, 0, len(resp.Chats)+len(resp.Users))
	for _, c := range resp.Chats {
		switch v := c.(type) {
		case *tg.Channel:
			out = append(out, ChatInfo{ID: v.ID, Type: "channel", Title: v.Title, Username: v.Username})
		case *tg.Chat:
			out = append(out, ChatInfo{ID: v.ID, Type: "group", Title: v.Title})
		}
	}
	for _, u := range resp.Users {
		if user, ok := u.(*tg.User); ok {
			out = append(out, ChatInfo{
				ID: user.ID, Type: "user", Title: DisplayName(user.FirstName, user.LastName, user.Username, user.ID), Username: user.Username,
			})
		}
	}
	return out, nil
}

func (g *GotdClient) AdminAction(ctx context.Context, req AdminActionReq) (InviteLinkResp, error) {
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return InviteLinkResp{}, err
	}
	switch req.Action {
	case "chat-title":
		switch p := peer.(type) {
		case *tg.InputPeerChannel:
			_, err = g.api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{Channel: &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash}, Title: req.Value})
		case *tg.InputPeerChat:
			_, err = g.api.MessagesEditChatTitle(ctx, &tg.MessagesEditChatTitleRequest{ChatID: p.ChatID, Title: req.Value})
		default:
			err = safety.NewBadArgs("chat-title target must be a group or channel")
		}
		return InviteLinkResp{}, mapRPCErr(err)
	case "chat-description":
		_, err = g.api.MessagesEditChatAbout(ctx, &tg.MessagesEditChatAboutRequest{Peer: peer, About: req.Value})
		return InviteLinkResp{}, mapRPCErr(err)
	case "chat-photo":
		file, err := uploader.NewUploader(g.api).FromPath(ctx, req.Path)
		if err != nil {
			return InviteLinkResp{}, err
		}
		photo := &tg.InputChatUploadedPhoto{File: file}
		switch p := peer.(type) {
		case *tg.InputPeerChannel:
			_, err = g.api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{
				Channel: &tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
				Photo:   photo,
			})
		case *tg.InputPeerChat:
			_, err = g.api.MessagesEditChatPhoto(ctx, &tg.MessagesEditChatPhotoRequest{ChatID: p.ChatID, Photo: photo})
		default:
			err = safety.NewBadArgs("chat-photo target must be a group or channel")
		}
		return InviteLinkResp{}, mapRPCErr(err)
	case "set-permissions":
		_, err = g.api.MessagesEditChatDefaultBannedRights(ctx, &tg.MessagesEditChatDefaultBannedRightsRequest{
			Peer:         peer,
			BannedRights: parseBannedRights(req.Value),
		})
		return InviteLinkResp{}, mapRPCErr(err)
	case "chat-invite-link":
		invite, err := g.api.MessagesExportChatInvite(ctx, &tg.MessagesExportChatInviteRequest{Peer: peer})
		if err != nil {
			return InviteLinkResp{}, mapRPCErr(err)
		}
		return InviteLinkResp{Link: inviteLink(invite)}, nil
	case "promote", "demote":
		ch, ok := peer.(*tg.InputPeerChannel)
		if !ok {
			return InviteLinkResp{}, safety.NewBadArgs("%s target must be a channel or supergroup", req.Action)
		}
		user, err := g.inputUserFromID(req.UserID)
		if err != nil {
			return InviteLinkResp{}, err
		}
		rights := tg.ChatAdminRights{}
		if req.Action == "promote" {
			rights = tg.ChatAdminRights{
				ChangeInfo: true, DeleteMessages: true, BanUsers: true,
				InviteUsers: true, PinMessages: true, Other: true, ManageTopics: true,
			}
		}
		_, err = g.api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
			Channel:     &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			UserID:      user,
			AdminRights: rights,
		})
		return InviteLinkResp{}, mapRPCErr(err)
	case "ban-from-chat", "kick", "unban-from-chat":
		ch, ok := peer.(*tg.InputPeerChannel)
		if !ok {
			return InviteLinkResp{}, safety.NewBadArgs("%s target must be a channel or supergroup", req.Action)
		}
		participant, err := g.peerFromChatID(ctx, req.UserID)
		if err != nil {
			return InviteLinkResp{}, err
		}
		rights := tg.ChatBannedRights{}
		switch req.Action {
		case "ban-from-chat", "kick":
			rights.ViewMessages = true
			rights.SendMessages = true
		case "unban-from-chat":
			rights = tg.ChatBannedRights{}
		}
		_, err = g.api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{
			Channel:      &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			Participant:  participant,
			BannedRights: rights,
		})
		return InviteLinkResp{}, mapRPCErr(err)
	}
	return InviteLinkResp{}, safety.NewBadArgs("%s is unsupported for this peer type", req.Action)
}

func parseBannedRights(value string) tg.ChatBannedRights {
	rights := tg.ChatBannedRights{}
	if strings.Contains(strings.ToLower(value), "restrict") || strings.Contains(strings.ToLower(value), "read-only") {
		rights.SendMessages = true
		rights.SendMedia = true
		rights.SendPhotos = true
		rights.SendVideos = true
		rights.SendDocs = true
		rights.SendPlain = true
	}
	return rights
}

func (g *GotdClient) inputUserFromID(userID int64) (tg.InputUserClass, error) {
	if userID == 0 {
		return nil, safety.NewBadArgs("user_id cannot be 0")
	}
	if g.db == nil {
		return nil, safety.NewBadArgs("chat_id %d cannot be resolved without an entity cache (no DB available)", userID)
	}
	kind, accessHash, ok := store.LoadEntity(g.db, userID)
	if !ok || kind != store.EntityUser {
		return nil, safety.NewBadArgs(
			"no cached access_hash for chat_id %d; run `tg backfill-entities` once or use `tg send-by-username @name`",
			userID,
		)
	}
	return &tg.InputUser{UserID: userID, AccessHash: accessHash}, nil
}

func (g *GotdClient) ListChatMembers(ctx context.Context, chatID int64, limit int) ([]MemberInfo, error) {
	var err error
	limit, err = defaultedTelegramInt32Limit(limit, 50, "limit")
	if err != nil {
		return nil, err
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	ch, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return nil, safety.NewBadArgs("chat-members target must be a channel or supergroup")
	}
	resp, err := g.api.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
		Filter:  &tg.ChannelParticipantsRecent{},
		Limit:   limit,
	})
	if err != nil {
		return nil, mapRPCErr(err)
	}
	cp, ok := resp.(*tg.ChannelsChannelParticipants)
	if !ok {
		return nil, nil
	}
	out := make([]MemberInfo, 0, len(cp.Users))
	for _, u := range cp.Users {
		if user, ok := u.(*tg.User); ok {
			out = append(out, MemberInfo{UserID: user.ID, Username: user.Username, DisplayName: DisplayName(user.FirstName, user.LastName, user.Username, user.ID)})
		}
	}
	return out, nil
}

func (g *GotdClient) GetChatsInfo(ctx context.Context, ids []int64) ([]ChatInfo, error) {
	out := make([]ChatInfo, 0, len(ids))
	for _, id := range ids {
		var title, kind, username sql.NullString
		if g.db != nil {
			_ = g.db.QueryRow("SELECT title, type, username FROM tg_chats WHERE chat_id=?", id).Scan(&title, &kind, &username)
		}
		out = append(out, ChatInfo{ID: id, Type: kind.String, Title: title.String, Username: username.String})
	}
	return out, nil
}

func (g *GotdClient) ListenOnce(ctx context.Context) (ListenEvent, error) {
	select {
	case event := <-g.events:
		return event, nil
	case <-ctx.Done():
		return ListenEvent{}, ctx.Err()
	}
}

func listenEventsFromUpdates(updates tg.UpdatesClass) []ListenEvent {
	var out []ListenEvent
	add := func(kind string, msg tg.MessageClass) {
		m, ok := msg.(*tg.Message)
		if !ok {
			return
		}
		out = append(out, ListenEvent{
			UpdateKind: kind,
			ChatID:     peerID(m.PeerID),
			MessageID:  int64(m.ID),
			SenderID:   peerID(m.FromID),
			Text:       m.Message,
			MediaType:  messageMediaType(m.Media),
		})
	}
	switch u := updates.(type) {
	case *tg.Updates:
		for _, update := range u.Updates {
			switch v := update.(type) {
			case *tg.UpdateNewMessage:
				add("message", v.Message)
			case *tg.UpdateNewChannelMessage:
				add("channel_message", v.Message)
			}
		}
	case *tg.UpdateShortMessage:
		out = append(out, ListenEvent{UpdateKind: "message", ChatID: u.UserID, MessageID: int64(u.ID), Text: u.Message})
	case *tg.UpdateShortChatMessage:
		out = append(out, ListenEvent{UpdateKind: "chat_message", ChatID: u.ChatID, MessageID: int64(u.ID), SenderID: u.FromID, Text: u.Message})
	case *tg.UpdateShort:
		switch v := u.Update.(type) {
		case *tg.UpdateNewMessage:
			add("message", v.Message)
		case *tg.UpdateNewChannelMessage:
			add("channel_message", v.Message)
		}
	}
	return out
}

func messageMediaType(media tg.MessageMediaClass) string {
	switch media.(type) {
	case nil:
		return ""
	case *tg.MessageMediaPhoto:
		return "photo"
	case *tg.MessageMediaDocument:
		return "document"
	default:
		return fmt.Sprintf("%T", media)
	}
}

func inviteLink(inv tg.ExportedChatInviteClass) string {
	if v, ok := inv.(*tg.ChatInviteExported); ok {
		return v.Link
	}
	return ""
}

func firstTopicID(updates tg.UpdatesClass) int64 {
	if v, ok := updates.(*tg.Updates); ok {
		for _, up := range v.Updates {
			switch t := up.(type) {
			case *tg.UpdateNewChannelMessage:
				if svc, ok := t.Message.(*tg.MessageService); ok {
					if _, ok := svc.Action.(*tg.MessageActionTopicCreate); ok {
						return int64(svc.ID)
					}
				}
			case *tg.UpdateNewMessage:
				if svc, ok := t.Message.(*tg.MessageService); ok {
					if _, ok := svc.Action.(*tg.MessageActionTopicCreate); ok {
						return int64(svc.ID)
					}
				}
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
//
// gotd's error string format changed: FLOOD_WAIT now surfaces as
// "rpc error code 420: FLOOD_WAIT (5)" — note the "(5)" arg form, not the
// "FLOOD_WAIT_5" underscore form previous string-matching assumed. Use
// gotd's typed accessors so we don't have to track that format.
func mapRPCErr(err error) error {
	if err == nil {
		return nil
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		secs := int(d / time.Second)
		if secs == 0 && d > 0 {
			secs = 1
		}
		return &safety.FloodWait{Seconds: secs}
	}
	if rpcErr, ok := tgerr.As(err); ok {
		switch rpcErr.Type {
		case "PREMIUM_ACCOUNT_REQUIRED":
			return &safety.PremiumRequired{}
		}
	}
	return err
}

func mapAuthErr(err error) error {
	if err == nil {
		return nil
	}
	if rpcErr, ok := tgerr.As(err); ok {
		// PHONE_* and AUTH_* are user-input failures during the login flow.
		if strings.HasPrefix(rpcErr.Type, "PHONE_") || strings.HasPrefix(rpcErr.Type, "AUTH_") {
			return safety.NewMissingCredentials(err.Error())
		}
	}
	return err
}
