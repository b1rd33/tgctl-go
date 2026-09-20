package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type snapshotUploadClient struct {
	*client.FakeClient
	failIndex bool
}

func (c *snapshotUploadClient) UploadFile(ctx context.Context, req client.UploadFileReq) (client.UploadFileResp, error) {
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return client.UploadFileResp{}, err
	}
	resp, err := c.FakeClient.UploadFile(ctx, req)
	if err != nil {
		return resp, err
	}
	resp.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	resp.Bytes = int64(len(data))
	resp.MediaIdentity = "document:42"
	// Prove indexing consumes snapshot metadata, not a post-send file read.
	if err := os.Remove(req.Path); err != nil {
		return resp, err
	}
	if c.failIndex {
		resp.SHA256 = "invalid"
	}
	return resp, nil
}

func TestUploadIndexesConfirmedSnapshotAndPreservesSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			cfg, fc, dir := setupWriteEnv(t)
			c := &snapshotUploadClient{FakeClient: fc, failIndex: fail}
			cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) { return c, nil }
			path := writeMediaFixture(t, "document.txt", []byte("snapshot bytes"))
			out, code := runRoot(t, cfg, "--account", "default", "upload-document", "1", path, "--allow-write", "--json")
			if code != 0 {
				t.Fatalf("%d %s", code, out)
			}
			if !strings.Contains(out, fmt.Sprintf(`"hash_indexed":%t`, !fail)) {
				t.Fatal(out)
			}
			db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			matches, err := store.FindMediaHash(context.Background(), db, fmt.Sprintf("%x", sha256.Sum256([]byte("snapshot bytes"))), 10)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if len(matches) != 0 || !strings.Contains(out, "do not resend") {
					t.Fatal(out)
				}
				return
			}
			if len(matches) != 1 || matches[0].Representation != "upload_original" {
				t.Fatalf("%+v", matches)
			}
		})
	}
}

func TestAlbumIndexesConfirmedOriginals(t *testing.T) {
	cfg, fc, dir := albumFakeConfig(t)
	first := writeAlbumFixture(t, "one.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "two.mp4", []byte("video"))
	for i, path := range []string{first, second} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fc.AlbumResp.Items[i].SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		fc.AlbumResp.Items[i].Bytes = int64(len(data))
		fc.AlbumResp.Items[i].MediaIdentity = fmt.Sprintf("photo:%d", i+1)
	}
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--allow-write", "--json")
	if code != 0 || !strings.Contains(out, `"hashes_indexed":2`) {
		t.Fatalf("%d %s", code, out)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range fc.AlbumResp.Items {
		matches, err := store.FindMediaHash(context.Background(), db, item.SHA256, 10)
		if err != nil || len(matches) != 1 || matches[0].MessageID != item.MessageID {
			t.Fatalf("%+v %v", matches, err)
		}
	}
}

func TestUnconfirmedUploadDoesNotCreateHashIndex(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	fc.NextErr = fmt.Errorf("transport outcome unknown")
	path := writeMediaFixture(t, "document.txt", []byte("unconfirmed"))
	out, code := runRoot(t, cfg, "upload-document", "1", path, "--allow-write", "--json")
	if code == 0 {
		t.Fatal(out)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='tg_media_hashes'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unconfirmed upload created hash state")
	}
}
