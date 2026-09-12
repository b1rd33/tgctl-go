package client

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// randomID returns a random int64 suitable for messages.SendMessage.RandomID.
func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}

func operationRandomID(ctx context.Context) int64 {
	if id, ok := safety.NextOperationRandomID(ctx); ok {
		return id
	}
	return randomID()
}
