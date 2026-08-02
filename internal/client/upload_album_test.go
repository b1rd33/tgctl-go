package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type albumRPCFake struct {
	calls            []string
	uploadMedia      []tg.InputMediaClass
	sendReq          *tg.MessagesSendMultiMediaRequest
	uploadErrAt      int
	uploadMediaErrAt int
	failUploadMedia  bool
	uploadMediaResp  []tg.MessageMediaClass
	afterUploadMedia func()
	afterSend        func()
	rawSendResponse  bool
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
		resp := f.uploadMediaResp[i]
		if f.afterUploadMedia != nil {
			f.afterUploadMedia()
		}
		return resp, nil
	}
	if f.afterUploadMedia != nil {
		f.afterUploadMedia()
	}
	return &tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: int64(i + 1)}}, nil
}

func (f *albumRPCFake) MessagesSendMultiMedia(_ context.Context, req *tg.MessagesSendMultiMediaRequest) (tg.UpdatesClass, error) {
	f.calls = append(f.calls, "send-multi-media")
	f.sendReq = req
	if f.afterSend != nil {
		f.afterSend()
	}
	if f.rawSendResponse {
		return f.sendResp, f.sendErr
	}
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

func TestUploadAlbumAcceptsExactTwoAndTenItems(t *testing.T) {
	for _, count := range []int{2, 10} {
		t.Run(fmt.Sprintf("items-%d", count), func(t *testing.T) {
			names := make([]string, count)
			for i := range names {
				if i%2 == 0 {
					names[i] = fmt.Sprintf("%02d.jpg", i)
				} else {
					names[i] = fmt.Sprintf("%02d.mp4", i)
				}
			}
			paths := writeAlbumFixtures(t, names...)
			items := make([]UploadAlbumItem, count)
			mediaResp := make([]tg.MessageMediaClass, count)
			ids := make([]int, count)
			for i := range paths {
				kind := "photo"
				if i%2 == 1 {
					kind = "video"
				}
				items[i] = UploadAlbumItem{Path: paths[i], Kind: kind}
				if kind == "photo" {
					mediaResp[i] = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(i + 1)}}
				} else {
					mediaResp[i] = &tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: int64(i + 1)}}
				}
				ids[i] = i + 100
			}
			api := &albumRPCFake{uploadMediaResp: mediaResp, sendResp: albumUpdates(ids...)}
			resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: items})
			if err != nil || len(resp.Items) != count || len(resp.MessageIDs) != count {
				t.Fatalf("count=%d response=%#v err=%v", count, resp, err)
			}
		})
	}
}

func TestUploadAlbumVideoOnly(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.mp4", "two.mp4")
	api := &albumRPCFake{uploadMediaResp: []tg.MessageMediaClass{
		&tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: 1}},
		&tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: 2}},
	}, sendResp: albumUpdates(501, 502)}
	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "video"}, {Path: paths[1], Kind: "video"}}})
	if err != nil || len(resp.Items) != 2 || resp.Items[0].MediaType != "video" || resp.Items[1].MediaType != "video" {
		t.Fatalf("response=%#v err=%v", resp, err)
	}
}

func TestUploadAlbumRecognizesVideoAttributeWhenResponseVideoFlagIsUnset(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.mp4", "two.jpg")
	api := &albumRPCFake{uploadMediaResp: []tg.MessageMediaClass{
		&tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{}}}},
		&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2}},
	}, sendResp: albumUpdates(601, 602)}

	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{
		ChatID: 1,
		Peer:   &tg.InputPeerChat{ChatID: 1},
		Items:  []UploadAlbumItem{{Path: paths[0], Kind: "video"}, {Path: paths[1], Kind: "photo"}},
	})
	if err != nil {
		t.Fatalf("video attribute should classify as video: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0].MediaType != "video" || resp.Items[1].MediaType != "photo" {
		t.Fatalf("response=%#v", resp)
	}
}

func TestUploadAlbumCorrelatesOrderedMessagesWhenRandomMappingsAreOmitted(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}},
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2}},
		},
		sendResp: &tg.Updates{Updates: []tg.UpdateClass{
			&tg.UpdateNewMessage{Message: &tg.Message{ID: 701, GroupedID: 77}},
			&tg.UpdateNewMessage{Message: &tg.Message{ID: 702, GroupedID: 77}},
		}},
	}
	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{
		ChatID: 1,
		Peer:   &tg.InputPeerChat{ChatID: 1},
		Items:  []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}},
	})
	if err != nil {
		t.Fatalf("ordered update response should be accepted: %v", err)
	}
	if !reflect.DeepEqual(resp.MessageIDs, []int64{701, 702}) || resp.GroupedID != 77 {
		t.Fatalf("response=%#v", resp)
	}
}

func TestUploadAlbumRejectsUnsupportedAndOversizedBeforeTransport(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	for name, req := range map[string]UploadAlbumReq{
		"unsupported": {ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "document"}, {Path: paths[1], Kind: "photo"}}},
		"audio-mixed": {ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "audio"}, {Path: paths[1], Kind: "photo"}}},
		"oversized":   {ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, MaxBytes: 1, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}},
	} {
		api := &albumRPCFake{}
		_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), req)
		if err == nil || len(api.calls) != 0 {
			t.Fatalf("%s err=%v calls=%#v", name, err, api.calls)
		}
	}
}

func TestUploadAlbumSupportsAudioAndDocumentItems(t *testing.T) {
	dir := t.TempDir()
	audioOne := filepath.Join(dir, "one.mp3")
	audioTwo := filepath.Join(dir, "two.mp3")
	docOne := filepath.Join(dir, "one.pdf")
	docTwo := filepath.Join(dir, "two.pdf")
	for _, path := range []string{audioOne, audioTwo, docOne, docTwo} {
		if err := os.WriteFile(path, []byte("not a recognized container"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		paths []string
		kind  string
		resp  []tg.MessageMediaClass
	}{
		"audio": {
			paths: []string{audioOne, audioTwo}, kind: "audio",
			resp: []tg.MessageMediaClass{
				&tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{}}}},
				&tg.MessageMediaDocument{Document: &tg.Document{ID: 2, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{}}}},
			},
		},
		"document": {
			paths: []string{docOne, docTwo}, kind: "document",
			resp: []tg.MessageMediaClass{
				&tg.MessageMediaDocument{Document: &tg.Document{ID: 3}},
				&tg.MessageMediaDocument{Document: &tg.Document{ID: 4}},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			api := &albumRPCFake{uploadMediaResp: tc.resp, sendResp: albumUpdates(801, 802)}
			items := []UploadAlbumItem{{Path: tc.paths[0], Kind: tc.kind}, {Path: tc.paths[1], Kind: tc.kind}}
			_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: items, MediaKind: tc.kind})
			if err != nil {
				t.Fatal(err)
			}
			if len(api.uploadMedia) != 2 || len(api.uploadMedia) == 0 {
				t.Fatalf("upload media calls=%d", len(api.uploadMedia))
			}
			for i, media := range api.uploadMedia {
				doc, ok := media.(*tg.InputMediaUploadedDocument)
				if !ok {
					t.Fatalf("item %d upload media=%T, want document", i, media)
				}
				if name == "audio" {
					foundAudio := false
					for _, attr := range doc.Attributes {
						if _, ok := attr.(*tg.DocumentAttributeAudio); ok {
							foundAudio = true
							break
						}
					}
					if !foundAudio {
						t.Fatalf("item %d missing audio attribute: %#v", i, doc.Attributes)
					}
				} else if !doc.ForceFile {
					t.Fatalf("document item %d was not forced as a file", i)
				}
			}
		})
	}
}

func TestUploadAlbumUploadsInOrderAndMapsUpdates(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.mp4")
	originalFirst := filepath.Dir(paths[0]) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(paths[0])
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}},
			&tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: 22}},
		},
		sendResp: albumUpdates(501, 502),
	}
	resp, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, ReplyTo: 9, Silent: true, SupportsStreaming: true, Items: []UploadAlbumItem{
		{Path: originalFirst, Kind: "photo", Caption: "album caption"}, {Path: paths[1], Kind: "video"},
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
	if got := resp.Items[0].SourcePath; got != originalFirst {
		t.Fatalf("source path=%q, want caller path %q", got, originalFirst)
	}
	videoUpload, ok := api.uploadMedia[1].(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("uploaded video media=%T", api.uploadMedia[1])
	}
	streaming := false
	for _, attr := range videoUpload.Attributes {
		if video, ok := attr.(*tg.DocumentAttributeVideo); ok {
			streaming = video.SupportsStreaming
		}
	}
	if !streaming {
		t.Fatal("album-level streaming option was not applied")
	}
	if got := api.calls[len(api.calls)-1]; got != "send-multi-media" {
		t.Fatalf("calls=%v", api.calls)
	}
	wantCalls := []string{"upload-part", "upload-media", "upload-part", "upload-media", "send-multi-media"}
	if len(api.calls) != len(wantCalls) {
		t.Fatalf("call count=%v want %v", api.calls, wantCalls)
	}
	for i := range wantCalls {
		if api.calls[i] != wantCalls[i] {
			t.Fatalf("call order=%v want %v", api.calls, wantCalls)
		}
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

func TestUploadAlbumRejectsDuplicateMapping(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}},
		},
		sendResp: &tg.Updates{Updates: []tg.UpdateClass{
			&tg.UpdateMessageID{ID: 701, RandomID: 100}, &tg.UpdateMessageID{ID: 702, RandomID: 100},
		}}, rawSendResponse: true,
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
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

func TestUploadAlbumRejectsUploadMediaKindMismatch(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaDocument{Video: true, Document: &tg.Document{ID: 99}},
		},
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "conversion") {
		t.Fatalf("err=%v", err)
	}
	for _, call := range api.calls {
		if call == "send-multi-media" {
			t.Fatalf("kind mismatch reached final send: %#v", api.calls)
		}
	}
}

func TestUploadAlbumIgnoresNilMessageUpdatesSafely(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2}},
		},
		sendResp:        &tg.Updates{Updates: []tg.UpdateClass{(*tg.UpdateNewMessage)(nil)}},
		rawSendResponse: true,
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("err=%v", err)
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

func TestUploadAlbumTypedFinalSendRPCErrorIsDefinitive(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}},
			&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}},
		},
		sendResp: albumUpdates(501, 502),
		sendErr:  tgerr.New(400, "MEDIA_EMPTY"),
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(context.Background(), UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	var albumErr *AlbumUploadError
	if !errors.As(err, &albumErr) || albumErr.OutcomeUnknown {
		t.Fatalf("err=%v, want definitive final-send error", err)
	}
}

func TestUploadAlbumContextCancellationAfterFinalSendIsAmbiguous(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	api := &albumRPCFake{
		uploadMediaResp: []tg.MessageMediaClass{&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}, &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 22}}},
		sendResp:        albumUpdates(501, 502),
		afterSend:       cancel,
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(ctx, UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	var albumErr *AlbumUploadError
	if !errors.As(err, &albumErr) || albumErr.Stage != "final-send-cancel" {
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

func TestUploadAlbumCancellationStopsBeforeNextItemAndFinalSend(t *testing.T) {
	paths := writeAlbumFixtures(t, "one.jpg", "two.jpg")
	ctx, cancel := context.WithCancel(context.Background())
	api := &albumRPCFake{
		uploadMediaResp:  []tg.MessageMediaClass{&tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 11}}},
		afterUploadMedia: cancel,
	}
	_, err := (&GotdClient{albumAPI: api}).UploadAlbum(ctx, UploadAlbumReq{ChatID: 1, Peer: &tg.InputPeerChat{ChatID: 1}, Items: []UploadAlbumItem{{Path: paths[0], Kind: "photo"}, {Path: paths[1], Kind: "photo"}}})
	if err == nil || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("err=%v", err)
	}
	if len(api.calls) != 2 || api.calls[0] != "upload-part" || api.calls[1] != "upload-media" {
		t.Fatalf("calls=%#v", api.calls)
	}
}
