package client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"
)

func TestUploadSnapshotReturnsExactDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	data := []byte("the bytes actually uploaded")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := uploadSnapshot(context.Background(), &albumRPCFake{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if result.File == nil || result.Bytes != int64(len(data)) || result.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("%+v", result)
	}
}

func TestUploadResponseRetainsMediaIdentity(t *testing.T) {
	updates := &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 7, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 42}}}}}}
	data, err := collectAlbumUpdates(updates)
	if err != nil || data.identities[7] != "photo:42" {
		t.Fatalf("%+v %v", data, err)
	}
}
