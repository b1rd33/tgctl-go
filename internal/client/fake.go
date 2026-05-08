package client

import (
	"context"
	"errors"
)

// FakeClient is the test double for the Client interface. It records every
// call and lets tests assert on the captured arguments.
type FakeClient struct {
	Me       User
	NextErr  error
	Closed   bool
	Calls    []string
	Sent     []SendMessageReq
	Uploads  []UploadFileReq
	Edited   []EditMessageReq
	Forwards []ForwardReq
	Pins     []PinReq
	Reacts   []ReactReq
	Reads    []MarkReadReq
	Deletes  []DeleteMessagesReq
	Leaves   []LeaveChatReq
	Blocks   []BlockUserReq
	Unblocks []BlockUserReq
	Sessions []SessionRef
	Terms    []TerminateSessionReq
	Dialogs  []ChatInfo
	Contacts []ContactInfo

	Discoveries  []int
	ContactSyncs []bool
	Backfills    []BackfillReq
	BackfillRows []BackfillMessage
	Topics       []TopicInfo
	NextTopicID  int64
	Folders      []FolderInfo

	TopicCreates  []CreateTopicReq
	TopicEdits    []EditTopicReq
	TopicPins     []PinTopicReq
	FolderUpdates []FolderUpdateReq
	FolderDeletes []int64
	FolderReorders [][]int64
	PinnedLists   []int64
	AdminActions  []AdminActionReq
	Members       []MemberInfo
	ChatInfos     []ChatInfo

	// LastMessageID is the next id returned by SendMessage. Tests override this.
	NextMessageID int64
}

func (f *FakeClient) record(name string) error {
	f.Calls = append(f.Calls, name)
	if f.NextErr != nil {
		err := f.NextErr
		f.NextErr = nil
		return err
	}
	return nil
}

func (f *FakeClient) GetMe(_ context.Context) (User, error) {
	if err := f.record("GetMe"); err != nil {
		return User{}, err
	}
	if f.Me.ID == 0 {
		return User{}, errors.New("fake client: Me not set")
	}
	return f.Me, nil
}

func (f *FakeClient) SendMessage(_ context.Context, req SendMessageReq) (SendMessageResp, error) {
	if err := f.record("SendMessage"); err != nil {
		return SendMessageResp{}, err
	}
	f.Sent = append(f.Sent, req)
	id := f.NextMessageID
	if id == 0 {
		id = int64(1000 + len(f.Sent))
	}
	return SendMessageResp{MessageID: id, Date: "2026-05-08T12:00:00"}, nil
}

func (f *FakeClient) UploadFile(_ context.Context, req UploadFileReq) (UploadFileResp, error) {
	if err := f.record("UploadFile"); err != nil {
		return UploadFileResp{}, err
	}
	f.Uploads = append(f.Uploads, req)
	id := f.NextMessageID
	if id == 0 {
		id = int64(3000 + len(f.Uploads))
	}
	return UploadFileResp{MessageID: id, Date: "2026-05-08T12:00:00"}, nil
}

func (f *FakeClient) EditMessage(_ context.Context, req EditMessageReq) error {
	if err := f.record("EditMessage"); err != nil {
		return err
	}
	f.Edited = append(f.Edited, req)
	return nil
}

func (f *FakeClient) Forward(_ context.Context, req ForwardReq) (ForwardResp, error) {
	if err := f.record("Forward"); err != nil {
		return ForwardResp{}, err
	}
	f.Forwards = append(f.Forwards, req)
	out := make([]int64, len(req.MessageIDs))
	for i := range req.MessageIDs {
		out[i] = int64(2000 + len(f.Forwards)*10 + i)
	}
	return ForwardResp{MessageIDs: out}, nil
}

func (f *FakeClient) Pin(_ context.Context, req PinReq) error {
	if err := f.record("Pin"); err != nil {
		return err
	}
	f.Pins = append(f.Pins, req)
	return nil
}

func (f *FakeClient) React(_ context.Context, req ReactReq) error {
	if err := f.record("React"); err != nil {
		return err
	}
	f.Reacts = append(f.Reacts, req)
	return nil
}

func (f *FakeClient) MarkRead(_ context.Context, req MarkReadReq) error {
	if err := f.record("MarkRead"); err != nil {
		return err
	}
	f.Reads = append(f.Reads, req)
	return nil
}

func (f *FakeClient) DeleteMessages(_ context.Context, req DeleteMessagesReq) (DeleteMessagesResp, error) {
	if err := f.record("DeleteMessages"); err != nil {
		return DeleteMessagesResp{}, err
	}
	f.Deletes = append(f.Deletes, req)
	return DeleteMessagesResp{Deleted: len(req.MessageIDs)}, nil
}

func (f *FakeClient) LeaveChat(_ context.Context, req LeaveChatReq) error {
	if err := f.record("LeaveChat"); err != nil {
		return err
	}
	f.Leaves = append(f.Leaves, req)
	return nil
}

func (f *FakeClient) BlockUser(_ context.Context, req BlockUserReq) error {
	if err := f.record("BlockUser"); err != nil {
		return err
	}
	f.Blocks = append(f.Blocks, req)
	return nil
}

func (f *FakeClient) UnblockUser(_ context.Context, req BlockUserReq) error {
	if err := f.record("UnblockUser"); err != nil {
		return err
	}
	f.Unblocks = append(f.Unblocks, req)
	return nil
}

func (f *FakeClient) ListSessions(_ context.Context) ([]SessionRef, error) {
	if err := f.record("ListSessions"); err != nil {
		return nil, err
	}
	return f.Sessions, nil
}

func (f *FakeClient) TerminateSession(_ context.Context, req TerminateSessionReq) error {
	if err := f.record("TerminateSession"); err != nil {
		return err
	}
	f.Terms = append(f.Terms, req)
	return nil
}

func (f *FakeClient) DiscoverDialogs(_ context.Context, limit int) ([]ChatInfo, error) {
	if err := f.record("DiscoverDialogs"); err != nil {
		return nil, err
	}
	f.Discoveries = append(f.Discoveries, limit)
	return f.Dialogs, nil
}

func (f *FakeClient) SyncContacts(_ context.Context) ([]ContactInfo, error) {
	if err := f.record("SyncContacts"); err != nil {
		return nil, err
	}
	f.ContactSyncs = append(f.ContactSyncs, true)
	return f.Contacts, nil
}

func (f *FakeClient) BackfillMessages(_ context.Context, req BackfillReq) ([]BackfillMessage, error) {
	if err := f.record("BackfillMessages"); err != nil {
		return nil, err
	}
	f.Backfills = append(f.Backfills, req)
	return f.BackfillRows, nil
}

func (f *FakeClient) ListTopics(_ context.Context, chatID int64, limit int, query string) ([]TopicInfo, error) {
	if err := f.record("ListTopics"); err != nil {
		return nil, err
	}
	return f.Topics, nil
}

func (f *FakeClient) CreateTopic(_ context.Context, req CreateTopicReq) (CreateTopicResp, error) {
	if err := f.record("CreateTopic"); err != nil {
		return CreateTopicResp{}, err
	}
	f.TopicCreates = append(f.TopicCreates, req)
	id := f.NextTopicID
	if id == 0 {
		id = int64(100 + len(f.TopicCreates))
	}
	return CreateTopicResp{TopicID: id, Title: req.Title}, nil
}

func (f *FakeClient) EditTopic(_ context.Context, req EditTopicReq) error {
	if err := f.record("EditTopic"); err != nil {
		return err
	}
	f.TopicEdits = append(f.TopicEdits, req)
	return nil
}

func (f *FakeClient) PinTopic(_ context.Context, req PinTopicReq) error {
	if err := f.record("PinTopic"); err != nil {
		return err
	}
	f.TopicPins = append(f.TopicPins, req)
	return nil
}

func (f *FakeClient) ListFolders(_ context.Context) ([]FolderInfo, error) {
	if err := f.record("ListFolders"); err != nil {
		return nil, err
	}
	return f.Folders, nil
}

func (f *FakeClient) UpdateFolder(_ context.Context, req FolderUpdateReq) error {
	if err := f.record("UpdateFolder"); err != nil {
		return err
	}
	f.FolderUpdates = append(f.FolderUpdates, req)
	return nil
}

func (f *FakeClient) DeleteFolder(_ context.Context, id int64) error {
	if err := f.record("DeleteFolder"); err != nil {
		return err
	}
	f.FolderDeletes = append(f.FolderDeletes, id)
	return nil
}

func (f *FakeClient) ReorderFolders(_ context.Context, ids []int64) error {
	if err := f.record("ReorderFolders"); err != nil {
		return err
	}
	cp := append([]int64(nil), ids...)
	f.FolderReorders = append(f.FolderReorders, cp)
	return nil
}

func (f *FakeClient) ListPinnedDialogs(_ context.Context, chatID int64) ([]ChatInfo, error) {
	if err := f.record("ListPinnedDialogs"); err != nil {
		return nil, err
	}
	f.PinnedLists = append(f.PinnedLists, chatID)
	return f.Dialogs, nil
}

func (f *FakeClient) AdminAction(_ context.Context, req AdminActionReq) (InviteLinkResp, error) {
	if err := f.record("AdminAction"); err != nil {
		return InviteLinkResp{}, err
	}
	f.AdminActions = append(f.AdminActions, req)
	return InviteLinkResp{Link: "https://t.me/+fake"}, nil
}

func (f *FakeClient) ListChatMembers(_ context.Context, chatID int64, limit int) ([]MemberInfo, error) {
	if err := f.record("ListChatMembers"); err != nil {
		return nil, err
	}
	return f.Members, nil
}

func (f *FakeClient) GetChatsInfo(_ context.Context, ids []int64) ([]ChatInfo, error) {
	if err := f.record("GetChatsInfo"); err != nil {
		return nil, err
	}
	if len(f.ChatInfos) != 0 {
		return f.ChatInfos, nil
	}
	out := make([]ChatInfo, len(ids))
	for i, id := range ids {
		out[i] = ChatInfo{ID: id}
	}
	return out, nil
}

func (f *FakeClient) Close() error {
	f.Closed = true
	return nil
}
