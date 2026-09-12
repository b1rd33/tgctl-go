package client

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
)

var ErrRecoveryIncomplete = errors.New("update recovery incomplete")

// updateStorage is both the checkpoint store and the persistence barrier. gotd
// v0.144.0 can call checkpoint setters after a handler error, so a failed apply
// permanently closes this runtime's barrier and aborts subsequent checkpoints.
type updateStorage struct {
	db      *sql.DB
	mu      sync.Mutex
	failure error
	failed  chan struct{}
}

func newUpdateStorage(db *sql.DB) *updateStorage {
	return &updateStorage{db: db, failed: make(chan struct{})}
}
func (s *updateStorage) failLocked(err error) error {
	if s.failure == nil {
		s.failure = fmt.Errorf("%w: %v", ErrRecoveryIncomplete, err)
		close(s.failed)
	}
	return s.failure
}
func (s *updateStorage) fail(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failLocked(err)
}
func (s *updateStorage) err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.failure }
func (s *updateStorage) exec(ctx context.Context, q string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	result, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return s.failLocked(err)
	}
	if strings.HasPrefix(q, "UPDATE ") {
		n, err := result.RowsAffected()
		if err != nil {
			return s.failLocked(err)
		}
		if n != 1 {
			return s.failLocked(errors.New("checkpoint state does not exist"))
		}
	}
	return nil
}
func (s *updateStorage) GetState(ctx context.Context, user int64) (updates.State, bool, error) {
	var v updates.State
	err := s.db.QueryRowContext(ctx, "SELECT pts,qts,date,seq FROM tg_update_state WHERE user_id=?", user).Scan(&v.Pts, &v.Qts, &v.Date, &v.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func (s *updateStorage) SetState(ctx context.Context, user int64, v updates.State) error {
	return s.exec(ctx, `INSERT INTO tg_update_state(user_id,pts,qts,date,seq) VALUES(?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET pts=excluded.pts,qts=excluded.qts,date=excluded.date,seq=excluded.seq`, user, v.Pts, v.Qts, v.Date, v.Seq)
}
func (s *updateStorage) SetPts(ctx context.Context, user int64, v int) error {
	return s.exec(ctx, "UPDATE tg_update_state SET pts=? WHERE user_id=?", v, user)
}
func (s *updateStorage) SetQts(ctx context.Context, user int64, v int) error {
	return s.exec(ctx, "UPDATE tg_update_state SET qts=? WHERE user_id=?", v, user)
}
func (s *updateStorage) SetDate(ctx context.Context, user int64, v int) error {
	return s.exec(ctx, "UPDATE tg_update_state SET date=? WHERE user_id=?", v, user)
}
func (s *updateStorage) SetSeq(ctx context.Context, user int64, v int) error {
	return s.exec(ctx, "UPDATE tg_update_state SET seq=? WHERE user_id=?", v, user)
}
func (s *updateStorage) SetDateSeq(ctx context.Context, user int64, date, seq int) error {
	return s.exec(ctx, "UPDATE tg_update_state SET date=?,seq=? WHERE user_id=?", date, seq, user)
}
func (s *updateStorage) GetChannelPts(ctx context.Context, user, channel int64) (int, bool, error) {
	var pts int
	err := s.db.QueryRowContext(ctx, "SELECT pts FROM tg_channel_state WHERE user_id=? AND channel_id=?", user, channel).Scan(&pts)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return pts, err == nil, err
}
func (s *updateStorage) SetChannelPts(ctx context.Context, user, channel int64, pts int) error {
	return s.exec(ctx, `INSERT INTO tg_channel_state(user_id,channel_id,pts) VALUES(?,?,?) ON CONFLICT(user_id,channel_id) DO UPDATE SET pts=excluded.pts`, user, channel, pts)
}
func (s *updateStorage) ForEachChannels(ctx context.Context, user int64, f func(context.Context, int64, int) error) error {
	rows, err := s.db.QueryContext(ctx, "SELECT channel_id,pts FROM tg_channel_state WHERE user_id=?", user)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var pts int
		if err := rows.Scan(&id, &pts); err != nil {
			return err
		}
		if err := f(ctx, id, pts); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (s *updateStorage) SetChannelAccessHash(ctx context.Context, user, channel, hash int64) error {
	return s.exec(ctx, `INSERT INTO tg_channel_state(user_id,channel_id,access_hash) VALUES(?,?,?) ON CONFLICT(user_id,channel_id) DO UPDATE SET access_hash=excluded.access_hash`, user, channel, hash)
}
func (s *updateStorage) GetChannelAccessHash(ctx context.Context, user, channel int64) (int64, bool, error) {
	var hash sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT access_hash FROM tg_channel_state WHERE user_id=? AND channel_id=?", user, channel).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return hash.Int64, hash.Valid && err == nil, err
}

func (s *updateStorage) Handle(ctx context.Context, u tg.UpdatesClass) (resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	defer func() {
		if resultErr != nil {
			resultErr = s.failLocked(resultErr)
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, event := range listenEventsFromUpdates(u) {
		if err := persistUpdateEvent(tx, event); err != nil {
			return err
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO tg_event_outbox(event) VALUES(?)", string(encoded)); err != nil {
			return err
		}
	}
	var users []tg.UserClass
	var chats []tg.ChatClass
	switch v := u.(type) {
	case *tg.Updates:
		users = v.Users
		chats = v.Chats
	case *tg.UpdatesCombined:
		users = v.Users
		chats = v.Chats
	}
	for _, u := range users {
		if v, ok := u.(*tg.User); ok && !v.Min {
			if err := store.UpsertEntity(tx, v.ID, store.EntityUser, v.AccessHash); err != nil {
				return err
			}
		}
	}
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			if !v.Min {
				if err := store.UpsertEntity(tx, v.ID, store.EntityChannel, v.AccessHash); err != nil {
					return err
				}
			}
		case *tg.Chat:
			if err := store.UpsertEntity(tx, v.ID, store.EntityChat, 0); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func persistUpdateEvent(tx *sql.Tx, e ListenEvent) error {
	if e.UpdateKind == "read_inbox" {
		_, err := tx.Exec(`INSERT INTO tg_read_state(chat_id,max_id) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET max_id=MAX(max_id,excluded.max_id),updated_at=CURRENT_TIMESTAMP`, e.ChatID, e.ReadMaxID)
		return err
	}
	if e.UpdateKind == "unsupported_expiring_message" {
		if err := purgeExpiringMedia(tx, e.ChatID, e.MessageID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO tg_messages(chat_id,message_id,deleted,date,is_outgoing) VALUES(?,?,1,'',0) ON CONFLICT(chat_id,message_id) DO UPDATE SET deleted=1,text=NULL,raw_json=NULL,media_path=NULL,media_id=NULL,media_type=NULL,has_media=0,sender_id=NULL,reply_to_msg_id=NULL`, e.ChatID, e.MessageID)
		return err
	}
	if e.Deleted {
		if e.ChatID == 0 {
			if _, err := tx.Exec("INSERT OR IGNORE INTO tg_common_deleted(message_id) VALUES(?)", e.MessageID); err != nil {
				return err
			}
			_, err := tx.Exec("UPDATE tg_messages SET deleted=1 WHERE message_id=? AND chat_id > -1000000000000", e.MessageID)
			return err
		}
		_, err := tx.Exec(`INSERT INTO tg_messages(chat_id,message_id,deleted,date,is_outgoing) VALUES(?,?,1,'',0) ON CONFLICT(chat_id,message_id) DO UPDATE SET deleted=1`, e.ChatID, e.MessageID)
		return err
	}
	if e.ChatID == 0 || e.MessageID == 0 {
		return nil
	}
	var sender, reply *int64
	if e.SenderID != 0 {
		sender = &e.SenderID
	}
	if e.ReplyToMsgID != 0 {
		reply = &e.ReplyToMsgID
	}
	return store.UpsertLiveMessage(tx, store.LiveMessage{ChatID: e.ChatID, MessageID: e.MessageID, SenderID: sender, Date: e.Date, Text: &e.Text, IsOutgoing: e.IsOutgoing, ReplyToMsgID: reply, HasMedia: e.MediaType != "", MediaType: &e.MediaType, MediaIdentity: &e.MediaIdentity, GroupedID: e.GroupedID, EditDate: e.EditDate})
}

type recoveryAPI struct {
	*tg.Client
	storage *updateStorage
}

func (a recoveryAPI) UpdatesGetDifference(ctx context.Context, r *tg.UpdatesGetDifferenceRequest) (tg.UpdatesDifferenceClass, error) {
	v, err := a.Client.UpdatesGetDifference(ctx, r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, a.storage.fail(err)
	}
	if _, tooLong := v.(*tg.UpdatesDifferenceTooLong); tooLong {
		return nil, a.storage.fail(errors.New("server cannot supply the complete global update gap"))
	}
	return v, nil
}
func (a recoveryAPI) UpdatesGetChannelDifference(ctx context.Context, r *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	v, err := a.Client.UpdatesGetChannelDifference(ctx, r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, a.storage.fail(err)
	}
	if _, tooLong := v.(*tg.UpdatesChannelDifferenceTooLong); tooLong {
		return nil, a.storage.fail(errors.New("server cannot supply the complete channel update gap"))
	}
	return v, nil
}

func ApplyListenEvent(db *sql.DB, event ListenEvent) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := persistUpdateEvent(tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

// Only remove CLI-managed files under this cache's media directory. Explicit
// external download/export destinations remain user-managed and are unsupported
// for expiring content; fresh expiring downloads are rejected before transfer.
func purgeExpiringMedia(tx *sql.Tx, chat, message int64) error {
	var path sql.NullString
	err := tx.QueryRow("SELECT media_path FROM tg_messages WHERE chat_id=? AND message_id=?", chat, message).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) || !path.Valid || path.String == "" {
		return nil
	}
	if err != nil {
		return err
	}
	var references int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE media_path=? AND NOT(chat_id=? AND message_id=?)", path.String, chat, message).Scan(&references); err != nil {
		return err
	}
	if references > 0 {
		return nil
	}
	rows, err := tx.Query("PRAGMA database_list")
	if err != nil {
		return err
	}
	var dbPath string
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			rows.Close()
			return err
		}
		if name == "main" {
			dbPath = file
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if dbPath == "" {
		return nil
	}
	root := filepath.Join(filepath.Dir(dbPath), "media")
	realRoot, err := filepath.EvalSymlinks(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(path.String)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return nil
	}
	info, err := os.Lstat(realPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("expiring media path is not a regular managed file")
	}
	return os.Remove(realPath)
}
