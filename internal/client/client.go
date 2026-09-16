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
	ID           int64
	Username     string
	Phone        string
	FirstName    string
	LastName     string
	IsBot        bool
	Premium      bool
	PremiumKnown bool
	DisplayName  string
	RawJSON      string
}

// ResolvedPeer is the stable, marked identity returned by selector
// resolution. ChatID uses the same peer-id marking as the local cache:
// positive users, negative basic groups, and -100... channel IDs.
type ResolvedPeer struct {
	ChatID     int64  `json:"chat_id"`
	Kind       string `json:"kind"`
	Username   string `json:"username,omitempty"`
	Title      string `json:"title,omitempty"`
	AccessHash int64  `json:"-"`
	Self       bool   `json:"self,omitempty"`
}

type AccountLimits struct {
	Source             string         `json:"source"`
	FreshAt            string         `json:"fresh_at,omitempty"`
	Premium            *bool          `json:"premium"`
	PremiumKnown       bool           `json:"premium_known"`
	CaptionLength      int64          `json:"caption_length,omitempty"`
	UploadMaxFileParts int64          `json:"upload_max_fileparts,omitempty"`
	UploadMaxBytes     int64          `json:"upload_max_bytes,omitempty"`
	FoldersLimit       int64          `json:"folders_limit,omitempty"`
	FolderChatsLimit   int64          `json:"folder_chats_limit,omitempty"`
	PinnedDialogsLimit int64          `json:"pinned_dialogs_limit,omitempty"`
	Raw                map[string]any `json:"raw,omitempty"`
}

type PermissionInfo struct {
	Chat         ChatInfo             `json:"chat"`
	UserID       int64                `json:"user_id,omitempty"`
	Role         string               `json:"role"`
	AdminRights  *tg.ChatAdminRights  `json:"admin_rights,omitempty"`
	BannedRights *tg.ChatBannedRights `json:"banned_rights,omitempty"`
	Effective    map[string]bool      `json:"effective"`
	Advisory     bool                 `json:"advisory"`
}

type TopicHistoryReq struct {
	ChatID   int64
	TopicID  int64
	OffsetID int64
	Limit    int
}

type TopicHistoryResult struct {
	Topic TopicInfo
	Page  RemotePage
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
	Stage          string
	Position       int
	Err            error
	OutcomeUnknown bool
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
	PtsCount int
	Deleted  int
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
	Source               string               `json:"source,omitempty"`
	ReadInboxMaxID       int                  `json:"read_inbox_max_id"`
	ReadStateKnown       bool                 `json:"read_state_known"`
	UnreadCount          int                  `json:"unread_count"`
	FolderID             int                  `json:"folder_id"`
	TopMessageID         int                  `json:"top_message_id"`
	Creator              bool                 `json:"creator,omitempty"`
	Forum                bool                 `json:"forum,omitempty"`
	SlowmodeSeconds      int                  `json:"slowmode_seconds,omitempty"`
	SlowmodeNextSendDate int64                `json:"slowmode_next_send_date,omitempty"`
	SlowmodeKnown        bool                 `json:"slowmode_known,omitempty"`
	DefaultBannedRights  *tg.ChatBannedRights `json:"default_banned_rights,omitempty"`
	AdminRights          *tg.ChatAdminRights  `json:"admin_rights,omitempty"`
	ID                   int64                `json:"chat_id"`
	Type                 string               `json:"type"`
	Title                string               `json:"title"`
	Username             string               `json:"username"`
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
	AfterMessageID int64
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
	EditDate         int
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
	Deleted          bool
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
	Truncated       bool
	NextOffsetID    int64
	Messages        []BackfillMessage
	AlbumsSeen      int
	MediaDownloaded int
	MediaSkipped    int
	MediaFailed     int
	Warnings        []string
	MediaOutcomes   []BackfillMediaOutcome `json:"-"`
}

// RemotePage is an explicitly server-fetched page. It is deliberately
// separate from BackfillResult: remote reads do not persist messages or mark
// them read.
type RemotePage struct {
	Messages     []BackfillMessage
	Total        int
	TotalKnown   bool
	NextOffsetID int64
}

type RemoteHistoryReq struct {
	ChatID     int64
	OffsetID   int64
	OffsetDate int64
	Limit      int
	MinID      int64
	MaxID      int64
}

type RemoteSearchReq struct {
	ChatID   int64
	SenderID int64
	Query    string
	Filter   string
	MinDate  int64
	MaxDate  int64
	OffsetID int64
	Limit    int
	MinID    int64
	MaxID    int64
	TopMsgID int64
}

type RepliesReq struct {
	ChatID   int64
	RootID   int64
	OffsetID int64
	Limit    int
}

type DiscussionInfo struct {
	OriginalChatID    int64             `json:"original_chat_id"`
	OriginalMessageID int64             `json:"original_message_id"`
	DiscussionChatID  int64             `json:"discussion_chat_id,omitempty"`
	MaxID             int64             `json:"max_id,omitempty"`
	ReadInboxMaxID    int64             `json:"read_inbox_max_id,omitempty"`
	ReadOutboxMaxID   int64             `json:"read_outbox_max_id,omitempty"`
	UnreadCount       int               `json:"unread_count"`
	Messages          []BackfillMessage `json:"messages"`
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
	Shared         bool
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

// PeerFolderReq moves one explicitly resolved peer between Telegram's inbox
// (folder 0) and archive (folder 1). It is separate from custom DialogFilter
// folders, which use FolderUpdateReq.
type PeerFolderReq struct {
	ChatID   int64
	FolderID int
}

// PeerNotifySettingsReq changes only the per-peer mute deadline. MuteUntil is
// a Unix timestamp; zero explicitly removes the peer-specific mute.
type PeerNotifySettingsReq struct {
	ChatID    int64
	MuteUntil int
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
	EventID       int64  `json:"event_id,omitempty"`
	IsOutgoing    bool   `json:"is_outgoing"`
	ReplyToMsgID  int64  `json:"reply_to_msg_id,omitempty"`
	EditDate      int    `json:"edit_date,omitempty"`
	ReadMaxID     int    `json:"read_max_id,omitempty"`
	UpdateKind    string `json:"update_kind"`
	ChatID        int64  `json:"chat_id"`
	MessageID     int64  `json:"message_id"`
	SenderID      int64  `json:"sender_id,omitempty"`
	Date          string `json:"date,omitempty"`
	Text          string `json:"text,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
	MediaIdentity string `json:"media_identity,omitempty"`
	GroupedID     int64  `json:"grouped_id,omitempty"`
	Deleted       bool   `json:"deleted,omitempty"`
}

// TerminateSessionReq mirrors account.ResetAuthorization.
type TerminateSessionReq struct {
	Hash int64
}

// Client is the narrow API command runners use.
type Client interface {
	GetMe(ctx context.Context) (User, error)
	ResolveSelector(ctx context.Context, selector string) (ResolvedPeer, error)
	GetAccountLimits(ctx context.Context) (AccountLimits, error)
	RemoteHistory(ctx context.Context, req RemoteHistoryReq) (RemotePage, error)
	RemoteSearch(ctx context.Context, req RemoteSearchReq) (RemotePage, error)
	RemoteGetMessage(ctx context.Context, chatID, messageID int64) (*BackfillMessage, error)
	GetChatPermissions(ctx context.Context, chatID, userID int64) (PermissionInfo, error)
	GetReplies(ctx context.Context, req RepliesReq) (RemotePage, error)
	TopicHistory(ctx context.Context, req TopicHistoryReq) (TopicHistoryResult, error)
	GetDiscussionMessage(ctx context.Context, chatID, messageID int64) (DiscussionInfo, error)
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
	SetPeerFolder(ctx context.Context, req PeerFolderReq) error
	SetPeerNotifySettings(ctx context.Context, req PeerNotifySettingsReq) error
	ListPinnedMessages(ctx context.Context, chatID int64) ([]PinnedMessage, error)
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

type PinnedMessage struct {
	MessageID int64  `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	Date      string `json:"date"`
}
