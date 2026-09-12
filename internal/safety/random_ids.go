package safety

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

type operationIDs struct {
	mu   sync.Mutex
	seed string
	next uint64
}
type operationIDsKey struct{}

func WithOperationRandomIDs(ctx context.Context, seed string) context.Context {
	return context.WithValue(ctx, operationIDsKey{}, &operationIDs{seed: seed})
}

// NextOperationRandomID derives an ordered stream from the durable reservation
// request ID. The same frozen operation always has the same Telegram IDs.
func NextOperationRandomID(ctx context.Context) (int64, bool) {
	state, ok := ctx.Value(operationIDsKey{}).(*operationIDs)
	if !ok {
		return 0, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.next++
	var counter [8]byte
	binary.LittleEndian.PutUint64(counter[:], state.next)
	hash := sha256.Sum256(append([]byte(state.seed), counter[:]...))
	id := int64(binary.LittleEndian.Uint64(hash[:8]))
	if id == 0 {
		id = 1
	}
	return id, true
}
