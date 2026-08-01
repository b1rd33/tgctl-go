package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

type albumRPCFake struct {
	calls            []string
	uploadMedia      []tg.InputMediaClass
	sendReq          *tg.MessagesSendMultiMediaRequest
	uploadErrAt      int
	uploadMediaErrAt int
	failUploadMedia  bool
	uploadMediaResp  []tg.MessageMediaClass
	sendResp         tg.UpdatesClass
	sendErr          error
	partErr          error
}

func (f *albumRPCFake) UploadSaveFilePart(context.Context, *tg.UploadSaveFilePartRequest) (bool, error) {
	f.calls = append(f.calls, "upload-part")
	if f.partErr != nil {
		return false, f.partErr
	}
	return true, nil
}

func (f *albumRPCFake) UploadSaveBigFilePart(context.Context, *tg.UploadSaveBigFilePartRequest) (bool, error) {
	f.calls = append(f.calls, "upload-big-part")
	if f.partErr != nil {
		return false, f.partErr
	}
	return true, nil
}

func (f *albumRPCFake) MessagesUploadMedia(_ context.Context, req *tg.MessagesUploadMediaRequest) (tg.MessageMediaClass, error) {
	f.calls = append(f.calls, "upload-media")
	f.uploadMedia = append(f.uploadMedia, req.Media)
	i := len(f.uploadMedia) - 1
	if f.failUploadMedia && f.uploadMediaErrAt == i {
		return nil, errors.New("uploadMedia failed")
	}
	if i < len(f.uploadMediaResp) {
		return f.uploadMediaResp[i], nil
	}
	return &tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: int64(i + 1)}}, nil
}

func (f *albumRPCFake) MessagesSendMultiMedia(_ context.Context, req *tg.MessagesSendMultiMediaRequest) (tg.UpdatesClass, error) {
	f.calls = append(f.calls, "send-multi-media")
	f.sendReq = req
	return bindAlbumUpdates(f.sendResp, req.MultiMedia), f.sendErr
}

func bindAlbumUpdates(u tg.UpdatesClass, media []tg.InputSingleMedia) tg.UpdatesClass {
	if u == nil {
		return u
	}
	var updates []tg.UpdateClass
	switch v := u.(type) {
	case *tg.Updates:
		updates = v.Updates
	case *tg.UpdatesCombined:
		updates = v.Updates
	default:
		return u
	}
	i := 0
	for _, update := range updates {
		if m, ok := update.(*tg.UpdateMessageID); ok && i < len(media) {
			m.RandomID = media[i].RandomID
			i++
		}
	}
	return u
}

func writeAlbumFixtures(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(names))
	for i, name := range names {
		path := filepath.Join(dir, name)
		var contents []byte
		if strings.HasSuffix(name, ".jpg") {
			contents = []byte{0xff, 0xd8, 0xff, 0xd9}
		} else {
			contents = []byte("....ftypisom....")
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		paths[i] = path
	}
	return paths
}

func albumUpdates(ids ...int) tg.UpdatesClass {
	updates := make([]tg.UpdateClass, 0, len(ids)*2)
	for i, id := range ids {
		updates = append(updates, &tg.UpdateMessageID{ID: id, RandomID: int64(100 + i)})
	}
	for _, id := range ids {
		updates = append(updates, &tg.UpdateNewMessage{Message: &tg.Message{ID: id, GroupedID: 777}})
	}
	return &tg.Updates{Updates: updates}
}

func TestUploadAlbumRejectsBoundsBeforeTransport(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	for _, items := range [][]UploadAlbumItem{
		{{Path: paths[0], Kind: "photo"}},
		{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}, {Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}, {Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}, {Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}, {Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}, {Path: paths[0], Kind: "photo"}},
	} {
		api := &albumRPCFake{}
		_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: items})
		if err == nil || !strings.Contains(err.Error(), "2") {
			t.Fatalf("items=%d err=%v", len(items), err)
		}
		if len(api.calls) != 0 {
			t.Fatalf("validation made transport calls: %#v", api.calls)
		}
	}
}

func TestUploadAlbumPrevalidatesEveryFileBeforeTransport(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{
		{Path: paths[0], Kind: "photo"}, {Path: filepath.Join(t.TempDir(), "missing.jpg"), Kind: "photo"},
	}})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err=%v, want missing-file validation", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("partial validation made transport calls: %#v", api.calls)
	}
}

func TestUploadAlbumUploadsInOrderAndMapsUpdates(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.mp4")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}},
			&tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: 22}},
		},
		sendResp: albumUpdates(501, 502),
	}
	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, ReplyTo: 9, Silent: true, Items: []UploadAlbumItem{
		{Path: paths[0], Kind: "photo", Caption: "album caption"}, {Path: paths[1], Kind: "video"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.MessageIDs, []int64{501, 502}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("message ids=%v want %v", got, want)
	}
	if resp.GroupedID != 777 || len(resp.Items) != 2 || resp.Items[0].Position != 0 || resp.Items[1].Position != 1 {
		t.Fatalf("response=%#v", resp)
	}
	if api.sendReq == nil || api.sendReq.ReplyTo == nil || !api.sendReq.Silent || len(api.sendReq.MultiMedia) != 2 {
		t.Fatalf("send request=%#v", api.sendReq)
	}
	if api.sendReq.MultiMedia[0].Message != "album caption" || api.sendReq.MultiMedia[1].Message != "" {
		t.Fatalf("captions=%q,%q", api.sendReq.MultiMedia[0].Message, api.sendReq.MultiMedia[1].Message)
	}
	if api.sendReq.MultiMedia[0].RandomID == 0 || api.sendReq.MultiMedia[0].RandomID == api.sendReq.MultiMedia[1].RandomID {
		t.Fatalf("random ids=%d,%d", api.sendReq.MultiMedia[0].RandomID, api.sendReq.MultiMedia[1].RandomID)
	}
	if _, ok := api.sendReq.MultiMedia[0].Media.(*tg.InputMediaPhoto); !ok {
		t.Fatalf("photo media=%T", api.sendReq.MultiMedia[0].Media)
	}
	if _, ok := api.sendReq.MultiMedia[1].Media.(*tg.InputMediaDocument); !ok {
		t.Fatalf("video media=%T", api.sendReq.MultiMedia[1].Media)
	}
	if got := api.calls[len(api.calls)-1]; got != "send-multi-media" {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestUploadAlbumExtractsUpdatesCombined(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}},
		},
		sendResp: &tg.UpdatesCombined{Updates: []tg.UpdateClass{
			&tg.UpdateMessageID{ID: 701, RandomID: 100}, &tg.UpdateMessageID{ID: 702, RandomID: 101},
			&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 701, GroupedID: 123}}, &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 702, GroupedID: 123}},
		}},
	}
	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err != nil || len(resp.Items) != 2 || resp.Items[0].MessageID != 701 || resp.Items[1].MessageID != 702 || resp.GroupedID != 123 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestUploadAlbumDoesNotSendAfterUploadFailure(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{partErr: errors.New("part failed")}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "upload") {
		t.Fatalf("err=%v", err)
	}
	for _, call := range api.calls {
		if call == "send-multi-media" {
			t.Fatalf("final send after upload failure: %#v", api.calls)
		}
	}
}

func TestUploadAlbumRejectsMissingMapping(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}},
		},
		sendResp: &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 701, RandomID: 100}}},
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadAlbumUploadMediaAndConversionFailuresDoNotSend(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	base := UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}}
	for name, api := range map[string]*albumRPCFake{
		"upload-media": {failUploadMedia: true, uploadMediaErrAt: 0},
		"conversion":   {uploadMediaResp: []tg.MessageMediaClass{&tg.MessageMediaPhoto{Photo: &tg.PhotoEmpty{ID: 99}}}},
	} {
		_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), base)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s err=%v", name, err)
		}
		for _, call := range api.calls {
			if call == "send-multi-media" {
				t.Fatalf("%s reached final send: %#v", name, api.calls)
			}
		}
	}
}

func TestUploadAlbumFinalSendErrorIsClassified(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}}},
		sendResp:        albumUpdates(501, 502),
		sendErr:         errors.New("send failed"),
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "final-send") {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadAlbumCancelledBeforeNetwork(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &albumRPCFake{}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(ctx, UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "cancel") || len(api.calls) != 0 {
		t.Fatalf("err=%v calls=%#v", err, api.calls)
	}
}
