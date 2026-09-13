package client

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/resolve"
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
	lifecycle         *clientLifecycle
	closeOnce         sync.Once
	closeErr          error
	db                *sql.DB // per-account entity cache; may be nil for ephemeral clients
	events            chan ListenEvent
	updateStore       *updateStorage
	listenMu          sync.Mutex
	lastEvent         int64
	selfID            int64
	resolvedPeers     map[int64]tg.InputPeerClass
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
	QR      bool
	QRShow  func(context.Context, string, time.Time) error
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
	lock := &safety.SessionLock{}
	if err := lock.AcquireContext(ctx, opts.Session, safety.LockWait(ctx), false); err != nil {
		return User{}, err
	}
	defer lock.Release()
	storage := &AtomicSessionStorage{Path: opts.Session}
	var dispatcher tg.UpdateDispatcher
	var useDispatcher bool
	if opts.QR {
		dispatcher = tg.NewUpdateDispatcher()
		useDispatcher = true
	}
	telegramOptions := telegram.Options{SessionStorage: storage}
	if useDispatcher {
		telegramOptions.UpdateHandler = dispatcher
	}
	client := telegram.NewClient(opts.APIID, opts.APIHash, telegramOptions)
	var me User
	err := client.Run(ctx, func(ctx context.Context) error {
		if opts.QR {
			if opts.QRShow == nil {
				return errors.New("QR login display callback is not configured")
			}
			loggedIn := qrlogin.OnLoginToken(dispatcher)
			authorization, err := client.QR().Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
				return opts.QRShow(ctx, token.URL(), token.Expires())
			})
			if err != nil {
				if rpc, ok := tgerr.As(err); ok && rpc.Type == "SESSION_PASSWORD_NEEDED" {
					password, promptErr := (fullAuthenticator{p: opts.Prompt}).Password(ctx)
					if promptErr != nil {
						return promptErr
					}
					authorization, err = client.Auth().Password(ctx, password)
				}
				if err != nil {
					return err
				}
			}
			self, ok := authorization.User.AsNotEmpty()
			if !ok {
				return fmt.Errorf("QR login returned unexpected user %T", authorization.User)
			}
			me = userFromSelf(self)
			return nil
		}
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
	return newLockedClient(ctx, apiID, apiHash, sessionPath, dbPath, storage, false)
}

// NewReadonly opens a gotd client with an in-memory snapshot of sessionPath.
// gotd can refresh its session state for the lifetime of the connection, but
// no session data is written back to the real file.
func NewReadonly(ctx context.Context, apiID int, apiHash, sessionPath string) (*GotdClient, error) {
	if err := validateAPIID(apiID); err != nil {
		return nil, err
	}
	gc, err := newLockedClient(ctx, apiID, apiHash, sessionPath, "", nil, true)
	if err != nil {
		return nil, err
	}
	db, dbErr := store.ConnectReadonly(filepath.Join(filepath.Dir(sessionPath), "telegram.sqlite"))
	var missing *store.DatabaseMissing
	if dbErr != nil && !errors.As(dbErr, &missing) {
		_ = gc.Close()
		return nil, dbErr
	}
	if db != nil {
		var bound int64
		if err := db.QueryRow("SELECT user_id FROM tg_account_identity WHERE slot=1").Scan(&bound); err != nil || bound != gc.selfID {
			db.Close()
			gc.Close()
			return nil, safety.NewBadArgs("read-only cache identity is missing or differs from the session; initialize a matching cache with a writable account operation")
		}
	}
	gc.db = db
	return gc, nil
}

func sessionStorageForMode(ctx context.Context, sessionPath string, readOnly bool) (session.Storage, error) {
	fileStorage := &AtomicSessionStorage{Path: sessionPath}
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

func newLockedClient(ctx context.Context, apiID int, apiHash, sessionPath, dbPath string, storage session.Storage, readOnly bool) (*GotdClient, error) {
	if apiHash == "" {
		return nil, safety.NewMissingCredentials("TG_API_ID and TG_API_HASH must be set")
	}
	lock := &safety.SessionLock{}
	if err := lock.AcquireContext(ctx, sessionPath, safety.LockWait(ctx), readOnly); err != nil {
		return nil, err
	}
	if readOnly {
		var err error
		storage, err = sessionStorageForMode(ctx, sessionPath, true)
		if err != nil {
			lock.Release()
			return nil, err
		}
	}

	return newClient(safetySessionContext(ctx, lock), apiID, apiHash, sessionPath, dbPath, storage)
}

type sessionLockKey struct{}

func safetySessionContext(ctx context.Context, lock *safety.SessionLock) context.Context {
	return context.WithValue(ctx, sessionLockKey{}, lock)
}

func newClient(ctx context.Context, apiID int, apiHash, sessionPath, dbPath string, storage session.Storage) (*GotdClient, error) {
	if err := validateAPIID(apiID); err != nil {
		return nil, err
	}
	if apiHash == "" {
		return nil, safety.NewMissingCredentials("TG_API_ID and TG_API_HASH must be set")
	}
	var db *sql.DB
	if dbPath != "" {
		var err error
		db, err = store.Connect(dbPath)
		if err != nil {
			if lock, ok := ctx.Value(sessionLockKey{}).(*safety.SessionLock); ok {
				lock.Release()
			}
			return nil, err
		}
	}
	var updateStore *updateStorage
	var manager *updates.Manager
	options := telegram.Options{SessionStorage: storage, NoUpdates: db == nil}
	if db != nil {
		updateStore = newUpdateStorage(db)
		manager = updates.New(updates.Config{Handler: updateStore, Storage: updateStore, AccessHasher: updateStore, OnChannelTooLong: func(int64) { updateStore.fail(errors.New("channel gap cannot be completely recovered")) }})
		options.UpdateHandler = manager
	}
	tgc := telegram.NewClient(apiID, apiHash, options)

	var api *tg.Client
	var selfID int64
	life, err := startClientRun(ctx, func(runCtx context.Context, ready chan<- error) error {
		if lock, ok := ctx.Value(sessionLockKey{}).(*safety.SessionLock); ok {
			defer lock.Release()
		}
		return tgc.Run(runCtx, func(rctx context.Context) error {
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
			selfID = status.User.ID
			if db != nil {
				var bound int64
				bindErr := db.QueryRow("SELECT user_id FROM tg_account_identity WHERE slot=1").Scan(&bound)
				if errors.Is(bindErr, sql.ErrNoRows) {
					_, bindErr = db.Exec("INSERT INTO tg_account_identity(slot,user_id) VALUES(1,?)", status.User.ID)
					bound = status.User.ID
				}
				if bindErr != nil {
					return bindErr
				}
				if bound != status.User.ID {
					return safety.NewBadArgs("session identity differs from the account cache; use a separate account directory")
				}
			}
			ledger := &writeLedgerInvoker{next: tgc.API().Invoker(), db: db, owner: output.NewRequestID()}
			if manager != nil {
				ledger.apply = manager.Handle
			}
			api = tg.NewClient(ledger)
			if manager == nil {
				ready <- nil
				<-rctx.Done()
				return rctx.Err()
			}
			recoveryCtx, cancel := context.WithCancel(rctx)
			defer cancel()
			stopped := make(chan error, 1)
			go func() {
				stopped <- manager.Run(recoveryCtx, recoveryAPI{Client: api, storage: updateStore}, status.User.ID, updates.AuthOptions{OnStart: func(context.Context) { ready <- nil }})
			}()
			select {
			case err := <-stopped:
				return err
			case <-updateStore.failed:
				cancel()
				<-stopped
				return updateStore.err()
			case <-rctx.Done():
				cancel()
				<-stopped
				return rctx.Err()
			}

		})
	})
	if err != nil {
		if db != nil {
			_ = db.Close()
		}
		return nil, err
	}
	gc := &GotdClient{
		api: api, mediaAPI: api, selfID: selfID,
		resolvedPeers:     make(map[int64]tg.InputPeerClass),
		fileDownloader:    gotdFileDownloader{client: tgc, api: api},
		destinationOpener: atomicDestinationOpener{},
		tgc:               tgc, lifecycle: life, updateStore: updateStore,
	}
	gc.db = db
	return gc, nil
}

// Close cancels the underlying client.Run and waits for it to drain.
func (g *GotdClient) Close() error {
	g.closeOnce.Do(func() {
		if g.lifecycle != nil {
			g.closeErr = g.lifecycle.close()
		}
		if g.db != nil {
			g.closeErr = errors.Join(g.closeErr, g.db.Close())
		}
	})
	return g.closeErr
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
		ID:           self.ID,
		Username:     self.Username,
		Phone:        self.Phone,
		FirstName:    self.FirstName,
		LastName:     self.LastName,
		IsBot:        self.Bot,
		Premium:      self.Premium,
		PremiumKnown: !self.Min,
		DisplayName:  DisplayName(self.FirstName, self.LastName, self.Username, self.ID),
	}
}

func (g *GotdClient) ResolveSelector(ctx context.Context, selector string) (ResolvedPeer, error) {
	s := strings.TrimSpace(selector)
	if strings.EqualFold(s, "self") || strings.EqualFold(s, "me") {
		me, err := g.GetMe(ctx)
		if err != nil {
			return ResolvedPeer{}, err
		}
		return ResolvedPeer{ChatID: me.ID, Kind: string(store.EntityUser), Username: me.Username, Title: me.DisplayName, Self: true}, nil
	}
	if id, ok := parseSelectorID(s); ok {
		peer, err := g.peerFromChatID(ctx, id)
		if err != nil {
			return ResolvedPeer{}, err
		}
		return resolvedPeerFromInput(peer, ""), nil
	}
	peer, err := g.resolvePeer(ctx, s)
	if err != nil {
		return ResolvedPeer{}, err
	}
	return resolvedPeerFromInput(peer, strings.TrimPrefix(s, "@")), nil
}

func parseSelectorID(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	var id int64
	for i, r := range value {
		if i == 0 && r == '-' {
			continue
		}
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	if _, err := fmt.Sscan(value, &id); err != nil {
		return 0, false
	}
	return id, true
}

func resolvedPeerFromInput(peer tg.InputPeerClass, username string) ResolvedPeer {
	resolved := ResolvedPeer{Username: username}
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		resolved.Self = true
		resolved.Kind = string(store.EntityUser)
	case *tg.InputPeerUser:
		resolved.ChatID, resolved.Kind, resolved.AccessHash = p.UserID, string(store.EntityUser), p.AccessHash
	case *tg.InputPeerChat:
		resolved.ChatID, resolved.Kind = peerid.Chat(p.ChatID), string(store.EntityChat)
	case *tg.InputPeerChannel:
		resolved.ChatID, resolved.Kind, resolved.AccessHash = peerid.Channel(p.ChannelID), string(store.EntityChannel), p.AccessHash
	}
	return resolved
}

func (g *GotdClient) GetAccountLimits(ctx context.Context) (AccountLimits, error) {
	me, err := g.GetMe(ctx)
	if err != nil {
		return AccountLimits{}, err
	}
	config, err := g.api.HelpGetAppConfig(ctx, 0)
	if err != nil {
		return AccountLimits{}, mapRPCErr(err)
	}
	app, ok := config.(*tg.HelpAppConfig)
	if !ok {
		return AccountLimits{Source: "telegram", Premium: premiumPointer(me), PremiumKnown: me.PremiumKnown, FreshAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	raw := jsonValueToAny(app.Config)
	values, _ := raw.(map[string]any)
	limits := AccountLimits{
		Source:       "telegram",
		FreshAt:      time.Now().UTC().Format(time.RFC3339),
		Premium:      premiumPointer(me),
		PremiumKnown: me.PremiumKnown,
		Raw:          values,
	}
	if me.PremiumKnown {
		suffix := "default"
		if me.Premium {
			suffix = "premium"
		}
		limits.CaptionLength = jsonInt(values, "caption_length_limit_"+suffix)
		limits.UploadMaxFileParts = jsonInt(values, "upload_max_fileparts_"+suffix)
		if limits.UploadMaxFileParts > 0 {
			limits.UploadMaxBytes = limits.UploadMaxFileParts * 524288
		}
		limits.FoldersLimit = jsonInt(values, "dialog_filters_limit_"+suffix)
		limits.FolderChatsLimit = jsonInt(values, "dialog_filters_chats_limit_"+suffix)
		limits.PinnedDialogsLimit = jsonInt(values, "dialogs_pinned_limit_"+suffix)
	}
	return limits, nil
}

func (g *GotdClient) RemoteHistory(ctx context.Context, req RemoteHistoryReq) (RemotePage, error) {
	if req.Limit < 1 || req.Limit > 100 {
		return RemotePage{}, safety.NewBadArgs("remote history limit must be between 1 and 100")
	}
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return RemotePage{}, err
	}
	api := g.backfillAPI
	if api == nil {
		api = g.api
	}
	if api == nil {
		return RemotePage{}, errors.New("Telegram history API is not initialized")
	}
	resp, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer: peer, OffsetID: int(req.OffsetID), OffsetDate: int(req.OffsetDate), Limit: req.Limit,
		MinID: int(req.MinID), MaxID: int(req.MaxID),
	})
	if err != nil {
		return RemotePage{}, mapRPCErr(err)
	}
	return remotePageFromResp(req.ChatID, resp), nil
}

func (g *GotdClient) RemoteSearch(ctx context.Context, req RemoteSearchReq) (RemotePage, error) {
	if req.Limit < 1 || req.Limit > 100 {
		return RemotePage{}, safety.NewBadArgs("remote search limit must be between 1 and 100")
	}
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return RemotePage{}, err
	}
	r := &tg.MessagesSearchRequest{
		Peer: peer, Q: req.Query, Limit: req.Limit, OffsetID: int(req.OffsetID),
		MinID: int(req.MinID), MaxID: int(req.MaxID), MinDate: int(req.MinDate), MaxDate: int(req.MaxDate),
		Filter: messagesFilter(req.Filter),
	}
	if req.SenderID != 0 {
		sender, err := g.peerFromChatID(ctx, req.SenderID)
		if err != nil {
			return RemotePage{}, err
		}
		r.SetFromID(sender)
	}
	if req.TopMsgID != 0 {
		r.SetTopMsgID(int(req.TopMsgID))
	}
	resp, err := g.api.MessagesSearch(ctx, r)
	if err != nil {
		return RemotePage{}, mapRPCErr(err)
	}
	return remotePageFromResp(req.ChatID, resp), nil
}

func messagesFilter(kind string) tg.MessagesFilterClass {
	switch kind {
	case "photo":
		return &tg.InputMessagesFilterPhotos{}
	case "video":
		return &tg.InputMessagesFilterVideo{}
	case "photo-video":
		return &tg.InputMessagesFilterPhotoVideo{}
	case "document":
		return &tg.InputMessagesFilterDocument{}
	case "voice":
		return &tg.InputMessagesFilterVoice{}
	case "audio":
		return &tg.InputMessagesFilterMusic{}
	default:
		return &tg.InputMessagesFilterEmpty{}
	}
}

func (g *GotdClient) RemoteGetMessage(ctx context.Context, chatID, messageID int64) (*BackfillMessage, error) {
	if err := validatePositiveTelegramInt32(messageID, "message_id"); err != nil {
		return nil, err
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	input := []tg.InputMessageClass{&tg.InputMessageID{ID: int(messageID)}}
	var resp tg.MessagesMessagesClass
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		resp, err = g.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, ID: input,
		})
	} else {
		resp, err = g.api.MessagesGetMessages(ctx, input)
	}
	if err != nil {
		return nil, mapRPCErr(err)
	}
	for _, message := range messagesFromHistoryResp(resp) {
		if row, ok := remoteMessageFromTL(chatID, message); ok && row.MessageID == messageID {
			return &row, nil
		}
	}
	return nil, nil
}

func (g *GotdClient) GetReplies(ctx context.Context, req RepliesReq) (RemotePage, error) {
	if req.Limit < 1 || req.Limit > 100 {
		return RemotePage{}, safety.NewBadArgs("replies limit must be between 1 and 100")
	}
	if err := validatePositiveTelegramInt32(req.RootID, "message_id"); err != nil {
		return RemotePage{}, err
	}
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return RemotePage{}, err
	}
	resp, err := g.api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: peer, MsgID: int(req.RootID), OffsetID: int(req.OffsetID), Limit: req.Limit})
	if err != nil {
		return RemotePage{}, mapRPCErr(err)
	}
	return remotePageFromResp(req.ChatID, resp), nil
}

func (g *GotdClient) TopicHistory(ctx context.Context, req TopicHistoryReq) (RemotePage, error) {
	if req.Limit < 1 || req.Limit > 100 {
		return RemotePage{}, safety.NewBadArgs("topic history limit must be between 1 and 100")
	}
	if err := validatePositiveTelegramInt32(req.TopicID, "topic-id"); err != nil {
		return RemotePage{}, err
	}
	chats, err := g.GetChatsInfo(ctx, []int64{req.ChatID})
	if err != nil {
		return RemotePage{}, err
	}
	if len(chats) != 1 || chats[0].Type != "supergroup" || !chats[0].Forum {
		return RemotePage{}, safety.NewBadArgs("topic history target must be a forum supergroup")
	}
	root, err := g.RemoteGetMessage(ctx, req.ChatID, req.TopicID)
	if err != nil {
		return RemotePage{}, err
	}
	if root == nil || root.Deleted {
		return RemotePage{}, resolve.NewNotFound("topic %d root message was not found", req.TopicID)
	}
	if root.ChatID != 0 && root.ChatID != req.ChatID {
		return RemotePage{}, safety.NewBadArgs("topic root belongs to a different peer")
	}
	return g.GetReplies(ctx, RepliesReq{ChatID: req.ChatID, RootID: req.TopicID, OffsetID: req.OffsetID, Limit: req.Limit})
}

func (g *GotdClient) GetDiscussionMessage(ctx context.Context, chatID, messageID int64) (DiscussionInfo, error) {
	if err := validatePositiveTelegramInt32(messageID, "message_id"); err != nil {
		return DiscussionInfo{}, err
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return DiscussionInfo{}, err
	}
	resp, err := g.api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{Peer: peer, MsgID: int(messageID)})
	if err != nil {
		return DiscussionInfo{}, mapRPCErr(err)
	}
	info := DiscussionInfo{OriginalChatID: chatID, OriginalMessageID: messageID, UnreadCount: resp.UnreadCount, MaxID: int64(resp.MaxID), ReadInboxMaxID: int64(resp.ReadInboxMaxID), ReadOutboxMaxID: int64(resp.ReadOutboxMaxID), Messages: make([]BackfillMessage, 0, len(resp.Messages))}
	for _, chat := range resp.Chats {
		switch v := chat.(type) {
		case *tg.Channel:
			id := peerid.Channel(v.ID)
			if id != chatID {
				info.DiscussionChatID = id
			}
		case *tg.Chat:
			id := peerid.Chat(v.ID)
			if id != chatID {
				info.DiscussionChatID = id
			}
		}
	}
	for _, message := range resp.Messages {
		if row, ok := remoteMessageFromTL(chatID, message); ok {
			info.Messages = append(info.Messages, row)
		}
	}
	return info, nil
}

func remotePageFromResp(chatID int64, resp tg.MessagesMessagesClass) RemotePage {
	page := historyPageFromResp(resp)
	out := make([]BackfillMessage, 0, len(page.Messages))
	var next int64
	for _, message := range page.Messages {
		row, ok := remoteMessageFromTL(chatID, message)
		if !ok {
			continue
		}
		out = append(out, row)
		if row.MessageID > 0 && (next == 0 || row.MessageID < next) {
			next = row.MessageID
		}
	}
	return RemotePage{Messages: out, Total: page.Total, TotalKnown: page.TotalKnown, NextOffsetID: next}
}

func remoteMessageFromTL(chatID int64, message tg.MessageClass) (BackfillMessage, bool) {
	switch m := message.(type) {
	case *tg.Message:
		if m == nil {
			return BackfillMessage{}, false
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return BackfillMessage{}, false
		}
		messageChatID := peerID(m.PeerID)
		if messageChatID == 0 {
			messageChatID = chatID
		}
		return BackfillMessage{
			ChatID: messageChatID, MessageID: int64(m.ID), SenderID: peerID(m.FromID), Date: timeFromUnix(m.Date),
			Text: m.Message, IsOutgoing: m.Out, ReplyToMsgID: replyMessageID(m.ReplyTo), HasMedia: m.Media != nil,
			GroupedID: m.GroupedID, MediaType: messageMediaType(m.Media), RawJSON: string(raw),
		}, true
	case *tg.MessageService:
		if m == nil {
			return BackfillMessage{}, false
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return BackfillMessage{}, false
		}
		messageChatID := peerID(m.PeerID)
		if messageChatID == 0 {
			messageChatID = chatID
		}
		return BackfillMessage{ChatID: messageChatID, MessageID: int64(m.ID), SenderID: peerID(m.FromID), Date: timeFromUnix(m.Date), IsOutgoing: m.Out, RawJSON: string(raw)}, true
	case *tg.MessageEmpty:
		if m == nil {
			return BackfillMessage{}, false
		}
		return BackfillMessage{ChatID: chatID, MessageID: int64(m.ID), Deleted: true}, true
	default:
		return BackfillMessage{}, false
	}
}

func premiumPointer(me User) *bool {
	if !me.PremiumKnown {
		return nil
	}
	value := me.Premium
	return &value
}

func jsonInt(values map[string]any, key string) int64 {
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch n := value.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func jsonValueToAny(value tg.JSONValueClass) any {
	switch v := value.(type) {
	case *tg.JSONNull:
		return nil
	case *tg.JSONBool:
		return v.Value
	case *tg.JSONNumber:
		return v.Value
	case *tg.JSONString:
		return v.Value
	case *tg.JSONArray:
		out := make([]any, len(v.Value))
		for i, item := range v.Value {
			out[i] = jsonValueToAny(item)
		}
		return out
	case *tg.JSONObject:
		out := make(map[string]any, len(v.Value))
		for _, item := range v.Value {
			out[item.Key] = jsonValueToAny(item.Value)
		}
		return out
	default:
		return nil
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
	if err := g.persistEntitiesFromResolved(resolved.Users, resolved.Chats); err != nil {
		return nil, err
	}
	var peer tg.InputPeerClass
	switch p := resolved.Peer.(type) {
	case *tg.PeerUser:
		for _, u := range resolved.Users {
			user, ok := u.(*tg.User)
			if ok && user.ID == p.UserID {
				peer = &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
				break
			}
		}
	case *tg.PeerChat:
		peer = &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerChannel:
		for _, c := range resolved.Chats {
			ch, ok := c.(*tg.Channel)
			if ok && ch.ID == p.ChannelID {
				peer = &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
				break
			}
		}
	}
	if peer != nil {
		marked := resolvedPeerFromInput(peer, "").ChatID
		if marked != 0 {
			if g.resolvedPeers == nil {
				g.resolvedPeers = make(map[int64]tg.InputPeerClass)
			}
			g.resolvedPeers[marked] = peer
		}
		return peer, nil
	}
	return nil, fmt.Errorf("could not build input peer for resolved username %q", username)
}

// persistEntitiesFromResolved writes user/chat/channel access_hashes into the
// entity cache so future chat_id-keyed operations can run without hitting
// ContactsResolveUsername. No-op when db is nil.
func (g *GotdClient) persistEntitiesFromResolved(users []tg.UserClass, chats []tg.ChatClass) error {
	if g.db == nil {
		return nil
	}
	// Read-only clients resolve from their snapshot without changing it.
	if g.updateStore == nil && g.lifecycle != nil {
		return nil
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok && !user.Min {
			if err := store.UpsertEntity(g.db, user.ID, store.EntityUser, user.AccessHash); err != nil {
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
		RandomID: operationRandomID(ctx),
	}
	if req.ReplyTo != 0 || req.TopicID != 0 {
		replyTo := req.ReplyTo
		if replyTo == 0 {
			replyTo = req.TopicID
		}
		reply := &tg.InputReplyToMessage{ReplyToMsgID: int(replyTo)}
		if req.TopicID != 0 {
			reply.SetTopMsgID(int(req.TopicID))
		}
		r.ReplyTo = reply
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
	id, err := sentMessageID(updates, r.RandomID)
	return SendMessageResp{MessageID: id}, err
}

// peerFromChatID looks up an entity in tg_entities and builds the right
// InputPeer for it. Returns a clear error pointing at backfill-entities
// when nothing is cached.
func (g *GotdClient) peerFromChatID(_ context.Context, chatID int64) (tg.InputPeerClass, error) {
	if chatID == g.selfID && chatID > 0 {
		return &tg.InputPeerSelf{}, nil
	}
	if peer, ok := g.resolvedPeers[chatID]; ok {
		return peer, nil
	}
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
	if peerid.Kind(chatID) != string(kind) {
		return nil, safety.NewBadArgs("cached peer kind conflicts with marked chat ID; refresh the cache")
	}
	switch kind {
	case store.EntityUser:
		return &tg.InputPeerUser{UserID: chatID, AccessHash: accessHash}, nil
	case store.EntityChannel:
		return &tg.InputPeerChannel{ChannelID: peerid.Raw(chatID), AccessHash: accessHash}, nil
	case store.EntityChat:
		return &tg.InputPeerChat{ChatID: peerid.Raw(chatID)}, nil
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
		Peer: peer, Message: text, RandomID: operationRandomID(ctx),
		Silent: silent, NoWebpage: noWeb,
	}
	if replyTo != 0 {
		r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(replyTo)}
	}
	updates, err := g.api.MessagesSendMessage(ctx, r)
	if err != nil {
		return SendMessageResp{}, mapRPCErr(err)
	}
	id, err := sentMessageID(updates, r.RandomID)
	return SendMessageResp{MessageID: id}, err
}

func (g *GotdClient) UploadFile(ctx context.Context, req UploadFileReq) (UploadFileResp, error) {
	if err := validateOptionalTelegramInt32(req.ReplyTo, "reply_to"); err != nil {
		return UploadFileResp{}, err
	}
	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return UploadFileResp{}, err
	}
	file, err := uploadSnapshot(ctx, g.api, req.Path)
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
		RandomID: operationRandomID(ctx),
		Silent:   req.Silent,
	}
	if req.ReplyTo != 0 {
		r.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: int(req.ReplyTo)}
	}
	updates, err := g.api.MessagesSendMedia(ctx, r)
	if err != nil {
		return UploadFileResp{}, mapRPCErr(err)
	}
	id, err := sentMessageID(updates, r.RandomID)
	return UploadFileResp{MessageID: id}, err
}

func mimeForUpload(kind, path string) string {
	switch kind {
	case "voice":
		return "audio/ogg"
	case "video":
		return "video/mp4"
	case "audio":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".mp3":
			return "audio/mpeg"
		case ".m4a":
			return "audio/mp4"
		case ".flac":
			return "audio/flac"
		case ".wav":
			return "audio/wav"
		case ".ogg", ".opus":
			return "audio/ogg"
		default:
			return "audio/*"
		}
	case "photo":
		return "image/jpeg"
	}
	if strings.HasSuffix(strings.ToLower(path), ".txt") {
		return "text/plain"
	}
	return "application/octet-stream"
}

func extractNewMessageID(u tg.UpdatesClass) int64 {
	d, err := collectAlbumUpdates(u)
	if err != nil {
		return 0
	}
	if len(d.messageOrder) == 1 {
		return d.messageOrder[0]
	}
	if len(d.mapping) == 1 {
		for _, id := range d.mapping {
			return id
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
		randomIDs[i] = operationRandomID(ctx)
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
	d, decodeErr := collectAlbumUpdates(updates)
	if decodeErr != nil {
		return ForwardResp{}, safety.NewCommittedWriteWithExtras("forward accepted but response mapping is invalid", decodeErr, nil)
	}
	result := make([]int64, len(randomIDs))
	for i, random := range randomIDs {
		result[i] = d.mapping[random]
		if result[i] == 0 {
			return ForwardResp{}, safety.NewCommittedWriteWithExtras("forward accepted but response mapping is incomplete; do not retry blindly", nil, nil)
		}
	}
	return ForwardResp{MessageIDs: result}, nil
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

	maxID := req.UpToID
	if maxID == 0 {
		details, readErr := g.GetChatsInfo(ctx, []int64{req.ChatID})
		if readErr != nil {
			return readErr
		}
		if len(details) != 1 {
			return safety.NewBadArgs("cannot determine the chat read boundary")
		}
		maxID = int64(details[0].TopMessageID)
	}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		_, err = g.api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, MaxID: int(maxID)})
	} else {
		_, err = g.api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer, MaxID: int(maxID)})
	}
	if err != nil {
		return mapRPCErr(err)
	}
	if g.db != nil {
		if _, err := g.db.Exec("INSERT INTO tg_read_state(chat_id,max_id) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET max_id=MAX(max_id,excluded.max_id),updated_at=CURRENT_TIMESTAMP", req.ChatID, maxID); err != nil {
			return safety.NewCommittedWriteWithExtras("read acknowledgment accepted but local marker could not be saved", err, nil)
		}
	}
	return nil
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
	inputs := make([]tg.InputMessageClass, len(ids))
	for i, id := range ids {
		inputs[i] = &tg.InputMessageID{ID: id}
	}
	var existing tg.MessagesMessagesClass
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		if !req.ForEveryone {
			return DeleteMessagesResp{}, safety.NewBadArgs("channel deletions affect everyone; pass --for-everyone explicitly")
		}
		existing, err = g.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: inputs})
	} else {
		existing, err = g.api.MessagesGetMessages(ctx, inputs)
	}
	if err != nil {
		return DeleteMessagesResp{}, mapRPCErr(err)
	}
	verified := map[int]bool{}
	for _, msg := range messagesFromHistoryResp(existing) {
		switch m := msg.(type) {
		case *tg.Message:
			if peerID(m.PeerID) != req.ChatID {
				return DeleteMessagesResp{}, safety.NewBadArgs("message does not belong to the confirmed chat")
			}
			verified[m.ID] = true
		case *tg.MessageService:
			if peerID(m.PeerID) != req.ChatID {
				return DeleteMessagesResp{}, safety.NewBadArgs("message does not belong to the confirmed chat")
			}
			verified[m.ID] = true
		}
	}
	for _, id := range ids {
		if !verified[id] {
			return DeleteMessagesResp{}, safety.NewBadArgs("cannot verify every requested message in the confirmed chat")
		}
	}
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		resp, err := g.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			ID:      ids,
		})
		if err != nil {
			return DeleteMessagesResp{}, mapRPCErr(err)
		}
		return DeleteMessagesResp{Deleted: len(verified), PtsCount: resp.PtsCount}, nil
	}
	resp, err := g.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
		ID: ids, Revoke: req.ForEveryone,
	})
	if err != nil {
		return DeleteMessagesResp{}, mapRPCErr(err)
	}
	return DeleteMessagesResp{Deleted: len(verified), PtsCount: resp.PtsCount}, nil
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
	return g.discoverDialogs(ctx, limit)
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
	if err := g.persistEntitiesFromResolved(cc.Users, nil); err != nil {
		return nil, err
	}
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
				MinID:    int(req.AfterMessageID),
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
			if expiringMessage(m) {
				result.Warnings = append(result.Warnings, "expiring message omitted from persistent cache")
				continue
			}
			row := BackfillMessage{
				ChatID: req.ChatID, MessageID: int64(m.ID), Date: timeFromUnix(m.Date),
				Text: m.Message, IsOutgoing: m.Out, HasMedia: m.Media != nil, GroupedID: m.GroupedID, MediaDisposition: BackfillMediaNone,
			}
			row.SenderID = peerID(m.FromID)
			row.EditDate = m.EditDate
			row.ReplyToMsgID = replyMessageID(m.ReplyTo)
			raw, rawErr := json.Marshal(m)
			if rawErr != nil {
				return result, rawErr
			}
			row.RawJSON = string(raw)
			if req.DownloadMedia {
				if err := g.backfillMessageMedia(ctx, req, m, &row, &result); err != nil {
					return result, err
				}
			} else if m.Media != nil {
				row.MediaType = messageMediaType(m.Media)
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
		result.NextOffsetID = int64(minID)
		more := len(msgs) == want
		if page.TotalKnown && serverItemsSeen >= page.Total {
			more = false
		}
		if !more {
			break
		}
		if len(result.Messages) >= limit {
			result.Truncated = true
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
		RandomID: operationRandomID(ctx),
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
	id := firstTopicID(updates)
	if id == 0 {
		return CreateTopicResp{}, safety.NewCommittedWriteWithExtras("topic accepted but response topic ID is missing; do not retry blindly", nil, nil)
	}
	return CreateTopicResp{TopicID: id, Title: req.Title}, nil
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
		} else if shared, ok := f.(*tg.DialogFilterChatlist); ok {
			out = append(out, FolderInfo{ID: int64(shared.ID), Title: shared.Title.Text, Emoji: shared.Emoticon, IncludeChatIDs: inputPeerIDs(shared.IncludePeers), Shared: true})
		}
	}
	return out, nil
}

func (g *GotdClient) UpdateFolder(ctx context.Context, req FolderUpdateReq) error {
	if err := validatePositiveTelegramInt32(req.ID, "folder_id"); err != nil {
		return err
	}
	filters, err := g.api.MessagesGetDialogFilters(ctx)
	if err != nil {
		return mapRPCErr(err)
	}
	filter := &tg.DialogFilter{ID: int(req.ID), Title: tg.TextWithEntities{Text: req.Title}, Emoticon: req.Emoji}
	for _, existing := range filters.Filters {
		switch f := existing.(type) {
		case *tg.DialogFilter:
			if f.ID == int(req.ID) {
				copy := *f
				filter = &copy
			}
		case *tg.DialogFilterChatlist:
			if f.ID == int(req.ID) {
				return safety.NewBadArgs("shared chat-list folders require a dedicated workflow")
			}
		}
	}
	if req.Title != "" {
		filter.Title = tg.TextWithEntities{Text: req.Title}
	}
	if req.Emoji != "" {
		filter.SetEmoticon(req.Emoji)
	}
	includePeers, err := g.inputPeersFromChatIDs(ctx, req.IncludeChatIDs)
	if err != nil {
		return err
	}
	excludePeers, err := g.inputPeersFromChatIDs(ctx, req.ExcludeChatIDs)
	if err != nil {
		return err
	}
	filter = patchFolderPeers(filter, includePeers, excludePeers)

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
			ids = append(ids, peerid.Channel(p.ChannelID))
		case *tg.InputPeerChat:
			ids = append(ids, peerid.Chat(p.ChatID))
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
		file, err := uploadSnapshot(ctx, g.api, req.Path)
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
		rights, err := g.defaultBannedRights(ctx, peer)
		if err != nil {
			return InviteLinkResp{}, err
		}
		rights, err = patchBannedRights(rights, req.Value)
		if err != nil {
			return InviteLinkResp{}, err
		}
		_, err = g.api.MessagesEditChatDefaultBannedRights(ctx, &tg.MessagesEditChatDefaultBannedRightsRequest{Peer: peer, BannedRights: rights})
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
			rights = adminRightsFromFlags(req.Flags)
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
		if req.Action == "kick" {
			current, readErr := g.api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, Participant: participant})
			if readErr != nil {
				return InviteLinkResp{}, mapRPCErr(readErr)
			}
			if _, restricted := current.Participant.(*tg.ChannelParticipantBanned); restricted {
				return InviteLinkResp{}, safety.NewBadArgs("kick would erase existing restrictions; use an explicit ban or unban operation")
			}
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
		if err == nil && req.Action == "kick" {
			_, err = g.api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, Participant: participant, BannedRights: tg.ChatBannedRights{}})
			if err != nil {
				return InviteLinkResp{}, safety.NewCommittedWriteWithExtras("member removed but unban failed; member remains banned", mapRPCErr(err), map[string]any{"removed": true, "unban_failed": true})
			}
		}
		return InviteLinkResp{}, mapRPCErr(err)
	}
	return InviteLinkResp{}, safety.NewBadArgs("%s is unsupported for this peer type", req.Action)
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
	if len(ids) > 100 {
		return nil, safety.NewBadArgs("at most 100 peers may be inspected at once")
	}
	peers := make([]tg.InputDialogPeerClass, 0, len(ids))
	for _, id := range ids {
		p, err := g.peerFromChatID(ctx, id)
		if err != nil {
			return nil, err
		}
		peers = append(peers, &tg.InputDialogPeer{Peer: p})
	}
	resp, err := g.api.MessagesGetPeerDialogs(ctx, peers)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	info := dialogEntityInfo(resp.Users, resp.Chats)
	for _, d := range resp.Dialogs {
		if v, ok := d.(*tg.Dialog); ok {
			id := peerID(v.Peer)
			row := info[id]
			row.ReadInboxMaxID = v.ReadInboxMaxID
			row.UnreadCount = v.UnreadCount
			row.FolderID = v.FolderID
			row.TopMessageID = v.TopMessage
			row.ReadStateKnown = true
			info[id] = row
		}
	}
	result := make([]ChatInfo, 0, len(ids))
	for _, id := range ids {
		row, ok := info[id]
		if !ok {
			return nil, safety.NewBadArgs("requested peer metadata was not returned by Telegram")
		}
		result = append(result, row)
	}
	return result, nil
}

func (g *GotdClient) GetChatPermissions(ctx context.Context, chatID, userID int64) (PermissionInfo, error) {
	chats, err := g.GetChatsInfo(ctx, []int64{chatID})
	if err != nil {
		return PermissionInfo{}, err
	}
	if len(chats) != 1 {
		return PermissionInfo{}, safety.NewBadArgs("requested peer metadata was not returned by Telegram")
	}
	info := PermissionInfo{Chat: chats[0], UserID: userID, Role: "unknown", Effective: effectiveRights(chats[0].AdminRights, chats[0].DefaultBannedRights), Advisory: true}
	if chats[0].Creator {
		info.Role = "creator"
	} else if hasAdminRights(chats[0].AdminRights) {
		info.Role = "admin"
	} else {
		info.Role = "member"
	}
	if userID == 0 || chats[0].Type != "supergroup" && chats[0].Type != "channel" {
		return info, nil
	}
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return PermissionInfo{}, err
	}
	channel, ok := peer.(*tg.InputPeerChannel)
	if !ok {
		return info, nil
	}
	var target tg.InputPeerClass
	if userID == g.selfID {
		target = &tg.InputPeerSelf{}
	} else {
		target, err = g.peerFromChatID(ctx, userID)
		if err != nil {
			return PermissionInfo{}, err
		}
	}
	participant, err := g.api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, Participant: target,
	})
	if err != nil {
		return PermissionInfo{}, mapRPCErr(err)
	}
	info.Role, info.AdminRights, info.BannedRights = participantPermission(participant.Participant)
	info.Effective = effectiveRights(info.AdminRights, info.BannedRights)
	return info, nil
}

func hasAdminRights(rights *tg.ChatAdminRights) bool {
	if rights == nil {
		return false
	}
	return rights.Other || rights.ChangeInfo || rights.DeleteMessages || rights.BanUsers || rights.InviteUsers || rights.PinMessages || rights.ManageTopics || rights.PostMessages || rights.EditMessages || rights.ManageCall || rights.AddAdmins
}

func effectiveRights(admin *tg.ChatAdminRights, banned *tg.ChatBannedRights) map[string]bool {
	out := map[string]bool{}
	if admin != nil {
		out["change_info"] = admin.ChangeInfo
		out["delete_messages"] = admin.DeleteMessages
		out["ban_users"] = admin.BanUsers
		out["invite_users"] = admin.InviteUsers
		out["pin_messages"] = admin.PinMessages
		out["manage_topics"] = admin.ManageTopics
		out["post_messages"] = admin.PostMessages
		out["edit_messages"] = admin.EditMessages
		out["manage_call"] = admin.ManageCall
		out["add_admins"] = admin.AddAdmins
		out["send_messages"] = admin.PostMessages
	}
	if banned != nil {
		out["send_messages"] = !banned.SendMessages
		out["send_media"] = !banned.SendMedia
		out["send_stickers"] = !banned.SendStickers
		out["send_polls"] = !banned.SendPolls
		out["embed_links"] = !banned.EmbedLinks
	}
	return out
}

func participantPermission(participant tg.ChannelParticipantClass) (string, *tg.ChatAdminRights, *tg.ChatBannedRights) {
	switch p := participant.(type) {
	case *tg.ChannelParticipantCreator:
		return "creator", nil, nil
	case *tg.ChannelParticipantAdmin:
		return "admin", &p.AdminRights, nil
	case *tg.ChannelParticipantBanned:
		return "banned", nil, &p.BannedRights
	case *tg.ChannelParticipantLeft:
		return "left", nil, nil
	default:
		return "member", nil, nil
	}
}

func (g *GotdClient) ListenOnce(ctx context.Context) (ListenEvent, error) {
	if g.updateStore != nil {
		return g.listenDurable(ctx)
	}
	if err := ctx.Err(); err != nil {
		return ListenEvent{}, err
	}
	var done <-chan struct{}
	if g.lifecycle != nil {
		done = g.lifecycle.done
	}
	select {
	case <-done:
		return ListenEvent{}, g.lifecycle.terminalError()
	default:
	}
	select {
	case event, ok := <-g.events:
		if !ok {
			return ListenEvent{}, errors.New("Telegram update stream closed")
		}
		return event, nil
	case <-done:
		return ListenEvent{}, g.lifecycle.terminalError()
	case <-ctx.Done():
		return ListenEvent{}, ctx.Err()
	}
}

func updateList(updates tg.UpdatesClass) []tg.UpdateClass {
	switch u := updates.(type) {
	case *tg.Updates:
		return u.Updates
	case *tg.UpdatesCombined:
		return u.Updates
	case *tg.UpdateShort:
		return []tg.UpdateClass{u.Update}
	}
	return nil
}
func listenEventsFromUpdates(updates tg.UpdatesClass) []ListenEvent {
	var out []ListenEvent
	add := func(kind string, msg tg.MessageClass) {
		m, ok := msg.(*tg.Message)
		if !ok || m == nil {
			return
		}
		if expiringMessage(m) {
			out = append(out, ListenEvent{UpdateKind: "unsupported_expiring_message", ChatID: peerID(m.PeerID), MessageID: int64(m.ID)})
			return
		}
		event := ListenEvent{UpdateKind: kind, ChatID: peerID(m.PeerID), MessageID: int64(m.ID), SenderID: peerID(m.FromID), Date: timeFromUnix(m.Date), Text: m.Message, MediaType: messageMediaType(m.Media), MediaIdentity: messageMediaIdentity(m.Media), GroupedID: m.GroupedID, IsOutgoing: m.Out, EditDate: m.EditDate}
		if reply, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			event.ReplyToMsgID = int64(reply.ReplyToMsgID)
		}
		out = append(out, event)
	}
	deleted := func(kind string, chat int64, ids []int) {
		for _, id := range ids {
			out = append(out, ListenEvent{UpdateKind: kind, ChatID: chat, MessageID: int64(id), Deleted: true})
		}
	}
	for _, up := range updateList(updates) {
		switch v := up.(type) {
		case *tg.UpdateNewMessage:
			add("message", v.Message)
		case *tg.UpdateNewChannelMessage:
			add("channel_message", v.Message)
		case *tg.UpdateEditMessage:
			add("edit_message", v.Message)
		case *tg.UpdateEditChannelMessage:
			add("edit_channel_message", v.Message)
		case *tg.UpdateDeleteMessages:
			deleted("delete_message", 0, v.Messages)
		case *tg.UpdateDeleteChannelMessages:
			deleted("delete_channel_message", peerid.Channel(v.ChannelID), v.Messages)
		case *tg.UpdateReadHistoryInbox:
			out = append(out, ListenEvent{UpdateKind: "read_inbox", ChatID: peerID(v.Peer), ReadMaxID: v.MaxID})
		case *tg.UpdateReadChannelInbox:
			out = append(out, ListenEvent{UpdateKind: "read_inbox", ChatID: peerid.Channel(v.ChannelID), ReadMaxID: v.MaxID})
		}
	}
	switch u := updates.(type) {
	case *tg.UpdateShortMessage:
		if u.TTLPeriod != 0 {
			return []ListenEvent{{UpdateKind: "unsupported_expiring_message", ChatID: u.UserID, MessageID: int64(u.ID)}}
		}
		e := ListenEvent{UpdateKind: "message", ChatID: u.UserID, MessageID: int64(u.ID), Text: u.Message, Date: timeFromUnix(u.Date), IsOutgoing: u.Out, ReplyToMsgID: replyMessageID(u.ReplyTo)}
		if !u.Out {
			e.SenderID = u.UserID
		}
		out = append(out, e)
	case *tg.UpdateShortChatMessage:
		if u.TTLPeriod != 0 {
			return []ListenEvent{{UpdateKind: "unsupported_expiring_message", ChatID: peerid.Chat(u.ChatID), MessageID: int64(u.ID)}}
		}
		out = append(out, ListenEvent{UpdateKind: "chat_message", ChatID: peerid.Chat(u.ChatID), MessageID: int64(u.ID), SenderID: u.FromID, Text: u.Message, Date: timeFromUnix(u.Date), IsOutgoing: u.Out, ReplyToMsgID: replyMessageID(u.ReplyTo)})
	}
	return out
}
func expiringMessage(m *tg.Message) bool {
	if m == nil {
		return false
	}
	if m.TTLPeriod > 0 {
		return true
	}
	switch v := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		return v != nil && v.TTLSeconds > 0
	case *tg.MessageMediaDocument:
		return v != nil && v.TTLSeconds > 0
	}
	return false
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
	if list := updateList(updates); len(list) > 0 {
		for _, up := range list {
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
		return peerid.Chat(v.ChatID)
	case *tg.PeerChannel:
		return peerid.Channel(v.ChannelID)
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
	d, err := collectAlbumUpdates(u)
	if err != nil {
		return nil
	}
	if len(d.messageOrder) > 0 {
		return d.messageOrder
	}
	var ids []int64
	for _, id := range d.mapping {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// mapRPCErr classifies a gotd RPC error into the dispatch error taxonomy.
//
// gotd's error string format changed: FLOOD_WAIT now surfaces as
// "rpc error code 420: FLOOD_WAIT (5)" — note the "(5)" arg form, not the
// "FLOOD_WAIT_5" underscore form previous string-matching assumed. Use
// gotd's typed accessors so we don't have to track that format.
func mapRPCErr(err error) error {
	var rejected *safety.DefinitiveRejection
	if errors.As(err, &rejected) {
		return &safety.DefinitiveRejection{Err: mapRPCErr(rejected.Err)}
	}
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
		case "SLOWMODE_WAIT":
			return &safety.FloodWait{Seconds: rpcErr.Argument}
		case "AUTH_KEY_UNREGISTERED", "AUTH_KEY_INVALID", "SESSION_REVOKED", "SESSION_EXPIRED", "USER_DEACTIVATED", "USER_DEACTIVATED_BAN":
			return safety.NewMissingCredentials("Telegram authorization is no longer valid; authenticate this account again")
		case "PREMIUM_ACCOUNT_REQUIRED":
			return &safety.PremiumRequired{}
		case "CHAT_WRITE_FORBIDDEN", "CHAT_ADMIN_REQUIRED", "USER_BANNED_IN_CHANNEL", "CHAT_FORBIDDEN", "CHANNEL_PRIVATE", "USER_NOT_PARTICIPANT", "RIGHT_FORBIDDEN":
			return &safety.PermissionDenied{RPCType: rpcErr.Type}
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

func replyMessageID(reply tg.MessageReplyHeaderClass) int64 {
	if r, ok := reply.(*tg.MessageReplyHeader); ok {
		return int64(r.ReplyToMsgID)
	}
	return 0
}

func sentMessageID(u tg.UpdatesClass, random int64) (int64, error) {
	if short, ok := u.(*tg.UpdateShortSentMessage); ok && short != nil && short.ID > 0 {
		return int64(short.ID), nil
	}
	data, err := collectAlbumUpdates(u)
	if err == nil {
		if id := data.mapping[random]; id > 0 {
			return id, nil
		}
	}
	return 0, safety.NewCommittedWriteWithExtras("message accepted but its ID could not be correlated; do not retry blindly", err, nil)
}

func messageMediaIdentity(media tg.MessageMediaClass) string {
	switch v := media.(type) {
	case *tg.MessageMediaPhoto:
		if v != nil {
			if p, ok := v.Photo.(*tg.Photo); ok && p != nil {
				return fmt.Sprintf("photo:%d", p.ID)
			}
		}
	case *tg.MessageMediaDocument:
		if v != nil {
			if d, ok := v.Document.(*tg.Document); ok && d != nil {
				return fmt.Sprintf("document:%d", d.ID)
			}
		}
	}
	return ""
}
