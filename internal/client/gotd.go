package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"github.com/b1rd33/tgctl-go/internal/safety"
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
func New(ctx context.Context, apiID int, apiHash, sessionPath string) (*GotdClient, error) {
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
	return &GotdClient{api: api, tgc: tgc, cancel: cancel, done: done}, nil
}

// Close cancels the underlying client.Run and waits for it to drain.
func (g *GotdClient) Close() error {
	g.cancel()
	err := <-g.done
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
// chat_id resolution requires a cached access_hash; that is the entity-cache
// work that lands later. Until then, prefer @username for sends.
func (g *GotdClient) resolvePeer(ctx context.Context, selector string) (tg.InputPeerClass, error) {
	s := strings.TrimSpace(selector)
	if s == "me" || s == "self" {
		return &tg.InputPeerSelf{}, nil
	}
	username := strings.TrimPrefix(s, "@")
	if username == "" || strings.ContainsAny(username, " /\\") {
		return nil, fmt.Errorf("cannot resolve %q: pass an @username (raw chat_ids need a cached access_hash, not yet wired)", selector)
	}
	resolved, err := g.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, err
	}
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

// peerFromChatID is the chat_id→InputPeer lookup. Real implementation needs
// access_hash from a cached entity. For the minimum-viable send path we
// accept selectors that look like "@username" by stuffing them into ChatID
// elsewhere isn't feasible; instead, use SendMessageBySelector below.
func (g *GotdClient) peerFromChatID(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	return nil, fmt.Errorf("chat_id %d cannot be turned into an InputPeer without cached access_hash; "+
		"use `tg send-by-username @name <text>` until the entity cache is wired", chatID)
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

// ---- not-yet-wired methods on Client interface ----

var errNotWired = safety.NewMissingCredentials(
	"this command's gotd/td path is not wired yet; the safety pipeline runs but the Telegram call is stubbed",
)

func (g *GotdClient) EditMessage(context.Context, EditMessageReq) error  { return errNotWired }
func (g *GotdClient) Forward(context.Context, ForwardReq) (ForwardResp, error) {
	return ForwardResp{}, errNotWired
}
func (g *GotdClient) Pin(context.Context, PinReq) error                       { return errNotWired }
func (g *GotdClient) React(context.Context, ReactReq) error                   { return errNotWired }
func (g *GotdClient) MarkRead(context.Context, MarkReadReq) error             { return errNotWired }
func (g *GotdClient) DeleteMessages(context.Context, DeleteMessagesReq) (DeleteMessagesResp, error) {
	return DeleteMessagesResp{}, errNotWired
}
func (g *GotdClient) LeaveChat(context.Context, LeaveChatReq) error           { return errNotWired }
func (g *GotdClient) BlockUser(context.Context, BlockUserReq) error           { return errNotWired }
func (g *GotdClient) UnblockUser(context.Context, BlockUserReq) error         { return errNotWired }
func (g *GotdClient) ListSessions(context.Context) ([]SessionRef, error)       { return nil, errNotWired }
func (g *GotdClient) TerminateSession(context.Context, TerminateSessionReq) error {
	return errNotWired
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
