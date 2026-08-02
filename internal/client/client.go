// Package client defines the narrow Telegram interface used by all command
// runners. Production wires this to gotd/td; tests use FakeClient.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/gotd/td/tg"
)

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

// UploadFileReq mirrors messages.SendMedia with an uploaded file.
type UploadFileReq struct {
	ChatID            int64
	Path              string
	Kind              string
	MediaType         string
	Caption           string
	ReplyTo           int64
	Silent            bool
	Filename          string
	SupportsStreaming bool
}

type UploadFileResp struct {
	MessageID int64
	Date      string
}

// UploadAlbumItem is one ordered local media file in an album. Telegram
// accepts photo/video albums and same-type audio/document albums.
type UploadAlbumItem struct {
	Path              string
	Kind              string
	MediaType         string
	Caption           string
	Filename          string
	SupportsStreaming bool
}

// UploadAlbumReq describes one album upload. Peer may be supplied by callers
// that already resolved it; normal production callers should provide ChatID
// and let the account entity cache resolve the peer.
type UploadAlbumReq struct {
	ChatID int64
	Peer   tg.InputPeerClass
	Items  []UploadAlbumItem
	// MediaKind optionally forces every item to be one of auto, photo, video,
	// audio, or document. Auto derives the kind from the local file.
	MediaKind         string
	Caption           string
	ReplyTo           int64
	Silent            bool
	SupportsStreaming bool
	MaxBytes          int64
	MaxSizeMB         int64
}

// UploadAlbumRequest is the descriptive name for UploadAlbumReq.
type UploadAlbumRequest = UploadAlbumReq

type UploadAlbumItemResp struct {
	Position        int    `json:"position"`
	MessageID       int64  `json:"message_id"`
	MediaType       string `json:"media_type"`
	SourcePath      string `json:"source_path"`
	CaptionPlaced   bool   `json:"caption_placed"`
	CaptionPosition int    `json:"caption_position,omitempty"`
	GroupedID       int64  `json:"grouped_id,omitempty"`
}

// UploadAlbumItemResponse is the descriptive name for UploadAlbumItemResp.
type UploadAlbumItemResponse = UploadAlbumItemResp

type UploadAlbumResp struct {
	ChatID     int64                 `json:"chat_id"`
	MessageIDs []int64               `json:"message_ids"`
	GroupedID  int64                 `json:"grouped_id,omitempty"`
	Items      []UploadAlbumItemResp `json:"items"`
}

// UploadAlbumResponse is the descriptive name for UploadAlbumResp.
type UploadAlbumResponse = UploadAlbumResp

// AlbumUploadError preserves the operation stage and item position without
// exposing request credentials or private Telegram payloads.
type AlbumUploadError struct {
	Stage    string
	Position int
	Err      error
}

func (e *AlbumUploadError) Error() string {
	if e == nil {
		return "album upload failed"
	}
	if e.Position >= 0 {
		return fmt.Sprintf("album %s failed for item %d: %v", e.Stage, e.Position, e.Err)
	}
	return fmt.Sprintf("album %s failed: %v", e.Stage, e.Err)
}

func (e *AlbumUploadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// DownloadMediaReq identifies one Telegram message whose file attachment
// should be downloaded.
type DownloadMediaReq struct {
	ChatID    int64
	MessageID int64
	OutputDir string
	MaxBytes  int64
	Overwrite bool
}

type DownloadMediaResp struct {
	ChatID      int64     `json:"chat_id"`
	MessageID   int64     `json:"message_id"`
	MediaType   string    `json:"media_type"`
	MIMEType    string    `json:"mime_type"`
	Filename    string    `json:"filename"`
	Path        string    `json:"media_path"`
	Bytes       int64     `json:"bytes"`
	Skipped     bool      `json:"skipped"`
	MessageDate time.Time `json:"-"`
	// ArtifactIdentity binds Path to the directory entry committed or safely
	// inspected by the producer. It is intentionally omitted from external JSON.
	ArtifactIdentity media.ArtifactIdentity `json:"-"`
}

// CommittedMediaDownloadError reports a failure discovered after the media
// artifact was published. Response carries producer-issued identity and
// metadata so the command layer can independently validate recovery details.
type CommittedMediaDownloadError struct {
	Response DownloadMediaResp
	Err      error
}

func (e *CommittedMediaDownloadError) Error() string {
	if e == nil || e.Err == nil {
		return "media download committed but finalization failed"
	}
	return "media download committed but finalization failed: " + e.Err.Error()
}

func (e *CommittedMediaDownloadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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
	Hash       int64  `json:"hash"`
	DeviceName string `json:"device_name"`
	Platform   string `json:"platform"`
	IsCurrent  bool   `json:"is_current"`
}

type ChatInfo struct {
	ID       int64  `json:"chat_id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type ContactInfo struct {
	UserID    int64
	Phone     string
	FirstName string
	LastName  string
	Username  string
	IsMutual  bool
}

type BackfillReq struct {
	ChatID         int64
	Limit          int
	Throttle       time.Duration
	DownloadMedia  bool
	MediaDir       string
	MaxMediaBytes  int64
	OverwriteMedia bool
}

// MaxBackfillMessages bounds one command's in-memory history result. Telegram
// pages are still fetched in batches of 100, but the aggregate is retained
// until the command writes it to SQLite.
const MaxBackfillMessages = 10_000

type BackfillMessage struct {
	ChatID           int64
	MessageID        int64
	SenderID         int64
	Date             string
	Text             string
	IsOutgoing       bool
	ReplyToMsgID     int64
	HasMedia         bool
	GroupedID        int64
	MediaType        string
	MediaPath        string
	MediaIdentity    string
	MediaDisposition BackfillMediaStatus
	RawJSON          string
}

type BackfillMediaStatus string

const (
	BackfillMediaNone        BackfillMediaStatus = "none"
	BackfillMediaDownloaded  BackfillMediaStatus = "downloaded"
	BackfillMediaSkipped     BackfillMediaStatus = "skipped"
	BackfillMediaFailed      BackfillMediaStatus = "failed"
	BackfillMediaUnsupported BackfillMediaStatus = "unsupported"
	BackfillMediaMalformed   BackfillMediaStatus = "malformed"
)

type BackfillMediaOutcome struct {
	ChatID        int64
	MessageID     int64
	MediaIdentity string
	Status        BackfillMediaStatus
	MediaType     string
	MediaPath     string
	Bytes         int64
	ErrorCode     string
	Committed     bool
}

type BackfillResult struct {
	Messages        []BackfillMessage
	AlbumsSeen      int
	MediaDownloaded int
	MediaSkipped    int
	MediaFailed     int
	Warnings        []string
	MediaOutcomes   []BackfillMediaOutcome `json:"-"`
}

type TopicInfo struct {
	ID           int64
	Title        string
	IconEmojiID  int64
	Closed       bool
	Hidden       bool
	TopMessageID int64
	UnreadCount  int
}

type CreateTopicReq struct {
	ChatID      int64
	Title       string
	IconColor   int
	IconEmojiID int64
}

type CreateTopicResp struct {
	TopicID int64
	Title   string
}

type EditTopicReq struct {
	ChatID      int64
	TopicID     int64
	Title       string
	IconEmojiID int64
}

type PinTopicReq struct {
	ChatID  int64
	TopicID int64
	Pinned  bool
}

type FolderInfo struct {
	ID             int64
	Title          string
	Emoji          string
	IncludeChatIDs []int64
	ExcludeChatIDs []int64
	IsDefault      bool
}

type FolderUpdateReq struct {
	ID             int64
	Title          string
	Emoji          string
	IncludeChatIDs []int64
	ExcludeChatIDs []int64
}

type AdminActionReq struct {
	Action string
	ChatID int64
	UserID int64
	Value  string
	Path   string
	Flags  map[string]bool
}

type InviteLinkResp struct {
	Link string
}

type MemberInfo struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type ListenEvent struct {
	UpdateKind string `json:"update_kind"`
	ChatID     int64  `json:"chat_id"`
	MessageID  int64  `json:"message_id"`
	SenderID   int64  `json:"sender_id,omitempty"`
	Date       string `json:"date,omitempty"`
	Text       string `json:"text,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	GroupedID  int64  `json:"grouped_id,omitempty"`
	Deleted    bool   `json:"deleted,omitempty"`
}

// TerminateSessionReq mirrors account.ResetAuthorization.
type TerminateSessionReq struct {
	Hash int64
}

// Client is the narrow API command runners use.
type Client interface {
	GetMe(ctx context.Context) (User, error)
	SendMessage(ctx context.Context, req SendMessageReq) (SendMessageResp, error)
	UploadFile(ctx context.Context, req UploadFileReq) (UploadFileResp, error)
	UploadAlbum(ctx context.Context, req UploadAlbumReq) (UploadAlbumResp, error)
	DownloadMedia(ctx context.Context, req DownloadMediaReq) (DownloadMediaResp, error)
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
	DiscoverDialogs(ctx context.Context, limit int) ([]ChatInfo, error)
	SyncContacts(ctx context.Context) ([]ContactInfo, error)
	BackfillMessages(ctx context.Context, req BackfillReq) (BackfillResult, error)
	ListTopics(ctx context.Context, chatID int64, limit int, query string) ([]TopicInfo, error)
	CreateTopic(ctx context.Context, req CreateTopicReq) (CreateTopicResp, error)
	EditTopic(ctx context.Context, req EditTopicReq) error
	PinTopic(ctx context.Context, req PinTopicReq) error
	ListFolders(ctx context.Context) ([]FolderInfo, error)
	UpdateFolder(ctx context.Context, req FolderUpdateReq) error
	DeleteFolder(ctx context.Context, id int64) error
	ReorderFolders(ctx context.Context, ids []int64) error
	ListPinnedDialogs(ctx context.Context, chatID int64) ([]ChatInfo, error)
	AdminAction(ctx context.Context, req AdminActionReq) (InviteLinkResp, error)
	ListChatMembers(ctx context.Context, chatID int64, limit int) ([]MemberInfo, error)
	GetChatsInfo(ctx context.Context, ids []int64) ([]ChatInfo, error)
	ListenOnce(ctx context.Context) (ListenEvent, error)
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
