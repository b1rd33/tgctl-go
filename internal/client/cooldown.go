package client

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/b1rd33/tgctl-go/internal/peerid"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"reflect"
	"time"
)

func rpcPeerScope(input bin.Encoder) string {
	v := reflect.ValueOf(input).Elem()
	for _, name := range []string{"Peer", "ToPeer", "Channel"} {
		f := v.FieldByName(name)
		if !f.IsValid() || !f.CanInterface() {
			continue
		}
		switch p := f.Interface().(type) {
		case *tg.InputPeerUser:
			return fmt.Sprintf("peer:%d", p.UserID)
		case *tg.InputPeerChat:
			return fmt.Sprintf("peer:%d", peerid.Chat(p.ChatID))
		case *tg.InputPeerChannel:
			return fmt.Sprintf("peer:%d", peerid.Channel(p.ChannelID))
		case *tg.InputChannel:
			return fmt.Sprintf("peer:%d", peerid.Channel(p.ChannelID))
		}
	}
	return "account"
}
func checkCooldown(ctx context.Context, db *sql.DB, scope string) error {
	if db == nil {
		return nil
	}
	var until sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(until_unix) FROM tg_rpc_cooldowns WHERE scope IN ('account',?)", scope).Scan(&until); err != nil {
		return err
	}
	if seconds := until.Int64 - time.Now().Unix(); seconds > 0 {
		return &safety.FloodWait{Seconds: int(seconds)}
	}
	return nil
}
func recordCooldown(db *sql.DB, scope string, err error) error {
	if db == nil || err == nil {
		return nil
	}
	seconds := 0
	if d, ok := tgerr.AsFloodWait(err); ok {
		seconds = int((d + time.Second - 1) / time.Second)
		scope = "account"
	} else if e, ok := tgerr.As(err); ok && e.Type == "SLOWMODE_WAIT" {
		seconds = e.Argument
	}
	if seconds <= 0 {
		return nil
	}
	_, saveErr := db.Exec(`INSERT INTO tg_rpc_cooldowns(scope,until_unix) VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET until_unix=MAX(until_unix,excluded.until_unix)`, scope, time.Now().Unix()+int64(seconds))
	return saveErr
}
func reserveWriteWindow(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, "DELETE FROM tg_write_window WHERE at_unix<=?", now-60); err != nil {
		return err
	}
	var count int
	var oldest sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),MIN(at_unix) FROM tg_write_window").Scan(&count, &oldest); err != nil {
		return err
	}
	if count >= 20 {
		return &safety.LocalRateLimited{Msg: "account write limit reached; retry after the recorded window", RetryAfterSeconds: float64(max(1, oldest.Int64+60-now))}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO tg_write_window(at_unix) VALUES(?)", now); err != nil {
		return err
	}
	return tx.Commit()
}
