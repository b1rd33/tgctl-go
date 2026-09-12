package client

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type writeLedgerInvoker struct {
	next     tg.Invoker
	db       *sql.DB
	sequence atomic.Uint64
	owner    string
	accepted atomic.Bool
	apply    func(context.Context, tg.UpdatesClass) error
}

func isMutation(method string) bool {
	for _, prefix := range []string{"MessagesSend", "MessagesForward", "MessagesEdit", "MessagesDelete", "MessagesUpdate", "MessagesCreate", "MessagesRead", "MessagesExport", "ChannelsEdit", "ChannelsDelete", "ChannelsLeave", "ChannelsRead", "ContactsBlock", "ContactsUnblock", "AccountResetAuthorization"} {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}
func (j *writeLedgerInvoker) Invoke(ctx context.Context, input bin.Encoder, result bin.Decoder) error {
	method := reflect.TypeOf(input).Elem().Name()
	scope := rpcPeerScope(input)
	if err := checkCooldown(ctx, j.db, scope); err != nil {
		return err
	}
	if !isMutation(method) {
		err := j.next.Invoke(ctx, input, result)
		if saveErr := recordCooldown(j.db, scope, err); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	if j.db == nil {
		return errors.New("mutation requires a durable account write ledger")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reserveWriteWindow(ctx, j.db); err != nil {
		return &safety.DefinitiveRejection{Err: err}
	}
	var encoded bin.Buffer
	if err := input.Encode(&encoded); err != nil {
		return err
	}
	request := dispatch.RequestIDFrom(ctx)
	if request == "" {
		request = j.owner
	}
	if request == "" {
		request = output.NewRequestID()
	}
	callID := fmt.Sprintf("%s/%d", request, j.sequence.Add(1))
	// Store the exact TL request (including random IDs) before the first RPC.
	// These bytes stay in the protected account database, never in audit logs.
	_, err := j.db.ExecContext(ctx, `INSERT INTO tg_write_calls(call_id,request_id,method,fingerprint,request,state) VALUES(?,?,?,?,?,'prepared')`, callID, request, method, fmt.Sprintf("%x", sha256.Sum256(encoded.Buf)), encoded.Buf)
	if err != nil {
		return errors.New("write ledger preparation failed; Telegram was not called")
	}
	err = j.next.Invoke(ctx, input, result)
	if saveErr := recordCooldown(j.db, scope, err); saveErr != nil {
		return &safety.UnknownWrite{Err: errors.Join(err, saveErr), OperationID: callID}
	}
	if err == nil && j.apply != nil {
		if box, ok := result.(*tg.UpdatesBox); ok {
			if applyErr := j.apply(ctx, box.Updates); applyErr != nil {
				return safety.NewCommittedWriteWithExtras("write accepted but update persistence failed", applyErr, nil)
			}
		}
	}
	state := "accepted"
	if err != nil {
		state = "unknown"
		if rpc, ok := tgerr.As(err); ok && rpc.Code >= 400 && rpc.Code < 500 {
			state = "rejected"
		}
	}
	var response []byte
	if err == nil {
		if encoder, ok := result.(bin.Encoder); ok {
			var buf bin.Buffer
			if encodeErr := encoder.Encode(&buf); encodeErr != nil {
				return safety.NewCommittedWriteWithExtras("write accepted but response could not be stored", encodeErr, map[string]any{"operation_id": callID})
			}
			response = append([]byte(nil), buf.Buf...)
		}
	}
	// Cancellation must not prevent recording an already returned outcome.
	_, saveErr := j.db.Exec(`UPDATE tg_write_calls SET state=?,response=? WHERE call_id=?`, state, response, callID)
	if saveErr != nil {
		if state != "accepted" {
			return &safety.UnknownWrite{Err: saveErr, OperationID: callID}
		}
		return safety.NewCommittedWriteWithExtras("write outcome could not be finalized; inspect the write ledger before retrying", saveErr, map[string]any{"outcome": state, "operation_id": callID})
	}
	if state == "accepted" {
		j.accepted.Store(true)
	}
	if state == "rejected" && !j.accepted.Load() {
		return &safety.DefinitiveRejection{Err: err}
	}
	if state == "unknown" {
		return &safety.UnknownWrite{Err: err, OperationID: callID}
	}
	return err
}
