// Package client defines the narrow Telegram interface used by all command
// runners. Production wires this to gotd/td; tests use FakeClient.
package client

import "context"

// User mirrors the subset of fields tgcli.commands.auth uses.
type User struct {
	ID          int64
	Username    string
	Phone       string
	FirstName   string
	LastName    string
	IsBot       bool
	DisplayName string
	RawJSON     string
}

// SendMessageReq mirrors the input to messages.SendMessage.
type SendMessageReq struct {
	ChatID    int64
	Text      string
	ReplyTo   int64 // 0 = no reply
	TopicID   int64 // 0 = no topic / general
	Silent    bool
	NoWebpage bool
}

type SendMessageResp struct {
	MessageID int64
	Date      string
}

// EditMessageReq mirrors messages.EditMessage.
type EditMessageReq struct {
	ChatID    int64
	MessageID int64
	NewText   string
}

// ForwardReq mirrors messages.ForwardMessages.
type ForwardReq struct {
	FromChatID int64
	MessageIDs []int64
	ToChatID   int64
	TopicID    int64
}

type ForwardResp struct {
	MessageIDs []int64
}

// PinReq mirrors messages.UpdatePinnedMessage / Unpin.
type PinReq struct {
	ChatID    int64
	MessageID int64
	Silent    bool
	Unpin     bool // true = unpin
}

// ReactReq mirrors messages.SendReaction.
type ReactReq struct {
	ChatID    int64
	MessageID int64
	Emoji     string // empty = remove reaction
	Big       bool
}

// MarkReadReq mirrors messages.ReadHistory.
type MarkReadReq struct {
	ChatID int64
	UpToID int64
}

// DeleteMessagesReq mirrors messages.DeleteMessages.
type DeleteMessagesReq struct {
	ChatID      int64
	MessageIDs  []int64
	ForEveryone bool
}

type DeleteMessagesResp struct {
	Deleted int
}

// LeaveChatReq mirrors channels.LeaveChannel / messages.DeleteChatUser.
type LeaveChatReq struct {
	ChatID int64
}

// BlockUserReq / UnblockUserReq mirror contacts.Block/Unblock.
type BlockUserReq struct {
	UserID int64
}

// SessionRef is one row from account.GetAuthorizations.
type SessionRef struct {
	Hash       int64
	DeviceName string
	Platform   string
	IsCurrent  bool
}

// TerminateSessionReq mirrors account.ResetAuthorization.
type TerminateSessionReq struct {
	Hash int64
}

// Client is the narrow API command runners use.
type Client interface {
	GetMe(ctx context.Context) (User, error)
	SendMessage(ctx context.Context, req SendMessageReq) (SendMessageResp, error)
	EditMessage(ctx context.Context, req EditMessageReq) error
	Forward(ctx context.Context, req ForwardReq) (ForwardResp, error)
	Pin(ctx context.Context, req PinReq) error
	React(ctx context.Context, req ReactReq) error
	MarkRead(ctx context.Context, req MarkReadReq) error
	DeleteMessages(ctx context.Context, req DeleteMessagesReq) (DeleteMessagesResp, error)
	LeaveChat(ctx context.Context, req LeaveChatReq) error
	BlockUser(ctx context.Context, req BlockUserReq) error
	UnblockUser(ctx context.Context, req BlockUserReq) error
	ListSessions(ctx context.Context) ([]SessionRef, error)
	TerminateSession(ctx context.Context, req TerminateSessionReq) error
	Close() error
}

// DisplayName mirrors Python _display_title.
func DisplayName(firstName, lastName, username string, id int64) string {
	first := trim(firstName)
	last := trim(lastName)
	if first != "" && last != "" {
		return first + " " + last
	}
	if first != "" {
		return first
	}
	if last != "" {
		return last
	}
	if u := trim(username); u != "" {
		return "@" + u
	}
	return "user_" + itoa(id)
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
