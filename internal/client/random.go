package client

import (
	"crypto/rand"
	"encoding/binary"
)

// randomID returns a random int64 suitable for messages.SendMessage.RandomID.
func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}
