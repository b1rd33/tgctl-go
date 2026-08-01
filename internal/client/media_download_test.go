package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestDownloadMediaContractAndFake(t *testing.T) {
	identityDir := t.TempDir()
	identityPath := filepath.Join(identityDir, "identity.bin")
	if err := os.WriteFile(identityPath, []byte("identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, _, err := media.CaptureArtifactIdentity(identityDir, identityPath)
	if err != nil {
		t.Fatal(err)
	}
	wantReq := DownloadMediaReq{
		ChatID: 42, MessageID: 99, OutputDir: "raw/output", MaxBytes: 1024, Overwrite: true,
	}
	wantResp := DownloadMediaResp{
		ChatID: 42, MessageID: 99, MediaType: "video", MIMEType: "video/mp4",
		Filename: "clip.mp4", Path: "/tmp/clip.mp4", Bytes: 123, Skipped: true,
		MessageDate:      time.Date(2026, 8, 1, 10, 11, 12, 0, time.UTC),
		ArtifactIdentity: identity,
	}
	fake := &FakeClient{DownloadResp: wantResp}

	got, err := fake.DownloadMedia(context.Background(), wantReq)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantResp) {
		t.Fatalf("response = %#v, want %#v", got, wantResp)
	}
	if !reflect.DeepEqual(fake.Downloads, []DownloadMediaReq{wantReq}) {
		t.Fatalf("downloads = %#v, want request recorded", fake.Downloads)
	}

	encoded, err := json.Marshal(wantResp)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"chat_id":42,"message_id":99,"media_type":"video","mime_type":"video/mp4","filename":"clip.mp4","media_path":"/tmp/clip.mp4","bytes":123,"skipped":true}`
	if string(encoded) != wantJSON {
		t.Fatalf("JSON = %s, want %s", encoded, wantJSON)
	}
}

func TestFakeClientDownloadMediaConcurrentRecording(t *testing.T) {
	fake := &FakeClient{Me: User{ID: 1}, ListenEvents: []ListenEvent{{UpdateKind: "message"}}}
	const calls = 32
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(6)
		go func(id int64) {
			defer wg.Done()
			_, _ = fake.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 1, MessageID: id})
		}(int64(i + 1))
		go func(id int64) {
			defer wg.Done()
			_, _ = fake.SendMessage(context.Background(), SendMessageReq{ChatID: 1, Text: "concurrent"})
		}(int64(i + 1))
		go func() {
			defer wg.Done()
			_, _ = fake.GetMe(context.Background())
		}()
		go func() {
			defer wg.Done()
			_, _ = fake.ListFolders(context.Background())
		}()
		go func() {
			defer wg.Done()
			_, _ = fake.ListenOnce(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = fake.React(context.Background(), ReactReq{ChatID: 1, MessageID: 1})
		}()
	}
	wg.Wait()
	if len(fake.Downloads) != calls {
		t.Fatalf("recorded %d downloads, want %d", len(fake.Downloads), calls)
	}
}

var _ Client = (*FakeClient)(nil)

func TestExtractDownloadMediaClassification(t *testing.T) {
	tests := []struct {
		name      string
		message   *tg.Message
		mediaType string
		mimeType  string
		filename  string
	}{
		{
			name:      "photo",
			message:   photoMessage(7),
			mediaType: "photo", mimeType: "image/jpeg", filename: "photo_7.jpg",
		},
		{
			name:      "round video",
			message:   documentMessage(8, "video/mp4", &tg.DocumentAttributeVideo{RoundMessage: true}),
			mediaType: "video_note", mimeType: "video/mp4", filename: "document_8.mp4",
		},
		{
			name:      "video",
			message:   documentMessage(9, "video/webm", &tg.DocumentAttributeVideo{}),
			mediaType: "video", mimeType: "video/webm", filename: "document_9.webm",
		},
		{
			name:      "voice",
			message:   documentMessage(10, "audio/ogg", &tg.DocumentAttributeAudio{Voice: true}),
			mediaType: "voice", mimeType: "audio/ogg", filename: "document_10.ogg",
		},
		{
			name:      "audio",
			message:   documentMessage(11, "audio/mpeg", &tg.DocumentAttributeAudio{}),
			mediaType: "audio", mimeType: "audio/mpeg", filename: "document_11.mp3",
		},
		{
			name:      "sticker",
			message:   documentMessage(12, "image/webp", &tg.DocumentAttributeSticker{}),
			mediaType: "sticker", mimeType: "image/webp", filename: "document_12.webp",
		},
		{
			name:      "animation",
			message:   documentMessage(13, "image/gif", &tg.DocumentAttributeAnimated{}),
			mediaType: "animation", mimeType: "image/gif", filename: "document_13.gif",
		},
		{
			name:      "document",
			message:   documentMessage(14, "application/pdf"),
			mediaType: "document", mimeType: "application/pdf", filename: "document_14.pdf",
		},
		{
			name:      "animated sticker fallback",
			message:   documentMessage(17, "application/x-tgsticker", &tg.DocumentAttributeSticker{}, &tg.DocumentAttributeAnimated{}),
			mediaType: "sticker", mimeType: "application/x-tgsticker", filename: "document_17.tgs",
		},
		{
			name:      "Telegram filename wins and remains raw metadata",
			message:   documentMessage(15, "application/pdf", &tg.DocumentAttributeFilename{FileName: "../raw report.pdf"}),
			mediaType: "document", mimeType: "application/pdf", filename: "../raw report.pdf",
		},
		{
			name:      "empty MIME is stable",
			message:   documentMessage(16, ""),
			mediaType: "document", mimeType: "application/octet-stream", filename: "document_16.bin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := (messages.Elem{Msg: tt.message}).File(); !ok {
				t.Fatal("fixture must be a gotd file message")
			}
			got, err := extractDownloadMedia(tt.message)
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaType != tt.mediaType || got.MIMEType != tt.mimeType || got.Filename != tt.filename {
				t.Fatalf("metadata = %#v, want type=%q mime=%q filename=%q", got, tt.mediaType, tt.mimeType, tt.filename)
			}
		})
	}
}

func TestExtractDownloadMediaAttributePrecedenceIsOrderIndependent(t *testing.T) {
	tests := []struct {
		name  string
		attrs []tg.DocumentAttributeClass
		want  string
	}{
		{name: "animation beats generic video", attrs: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}, &tg.DocumentAttributeVideo{}}, want: "animation"},
		{name: "sticker beats generic video", attrs: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{}, &tg.DocumentAttributeVideo{}}, want: "sticker"},
		{name: "sticker beats animation", attrs: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{}, &tg.DocumentAttributeAnimated{}}, want: "sticker"},
		{name: "round video beats sticker and animation", attrs: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{RoundMessage: true}, &tg.DocumentAttributeSticker{}, &tg.DocumentAttributeAnimated{}}, want: "video_note"},
		{name: "voice beats generic audio", attrs: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{}, &tg.DocumentAttributeAudio{Voice: true}}, want: "voice"},
	}
	for _, tt := range tests {
		for _, ordered := range [][]tg.DocumentAttributeClass{tt.attrs, reverseAttributes(tt.attrs)} {
			t.Run(tt.name, func(t *testing.T) {
				got, err := extractDownloadMedia(documentMessage(21, "application/octet-stream", ordered...))
				if err != nil {
					t.Fatal(err)
				}
				if got.MediaType != tt.want {
					t.Fatalf("metadata = %#v, want %s", got, tt.want)
				}
			})
		}
	}
}

func TestExtractDownloadMediaUsesMediaFlagsAsSemanticFallback(t *testing.T) {
	tests := []struct {
		name string
		set  func(*tg.MessageMediaDocument)
		want string
	}{
		{name: "round", set: func(m *tg.MessageMediaDocument) { m.Round = true }, want: "video_note"},
		{name: "voice", set: func(m *tg.MessageMediaDocument) { m.Voice = true }, want: "voice"},
		{name: "video", set: func(m *tg.MessageMediaDocument) { m.Video = true }, want: "video"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := documentMessage(22, "application/octet-stream")
			tt.set(message.Media.(*tg.MessageMediaDocument))
			got, err := extractDownloadMedia(message)
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaType != tt.want {
				t.Fatalf("media type = %q, want %q", got.MediaType, tt.want)
			}
		})
	}
}

func TestExtractDownloadMediaMalformedIsBadArgsWithoutPanic(t *testing.T) {
	var typedNilMessage *tg.Message
	var typedNilDocumentMedia *tg.MessageMediaDocument
	var typedNilPhotoMedia *tg.MessageMediaPhoto
	var typedNilDocument *tg.Document
	var typedNilPhoto *tg.Photo
	var typedNilFilename *tg.DocumentAttributeFilename
	var typedNilPhotoSize *tg.PhotoSize
	var typedNilCachedSize *tg.PhotoCachedSize
	var typedNilProgressiveSize *tg.PhotoSizeProgressive
	var typedNilPathSize *tg.PhotoPathSize
	var typedNilDocumentThumb *tg.PhotoSize
	var typedNilVideoThumb *tg.VideoSize
	var typedNilAltDocument *tg.Document
	var typedNilVideoCover *tg.Photo
	validSize := &tg.PhotoSize{Type: "x", W: 100, H: 100, Size: 42}
	documentWithNilThumb := documentMessage(2, "application/pdf")
	documentWithNilThumb.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Thumbs = []tg.PhotoSizeClass{typedNilDocumentThumb}
	documentWithNilVideoThumb := documentMessage(3, "video/mp4", &tg.DocumentAttributeVideo{})
	documentWithNilVideoThumb.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).VideoThumbs = []tg.VideoSizeClass{typedNilVideoThumb}
	documentWithNilAlt := documentMessage(4, "video/mp4", &tg.DocumentAttributeVideo{})
	documentWithNilAlt.Media.(*tg.MessageMediaDocument).AltDocuments = []tg.DocumentClass{typedNilAltDocument}
	documentWithNilCover := documentMessage(5, "video/mp4", &tg.DocumentAttributeVideo{})
	documentWithNilCover.Media.(*tg.MessageMediaDocument).VideoCover = typedNilVideoCover

	tests := []struct {
		name string
		msg  *tg.Message
	}{
		{name: "nil message", msg: nil},
		{name: "typed nil message", msg: typedNilMessage},
		{name: "no media", msg: &tg.Message{}},
		{name: "empty media", msg: &tg.Message{Media: &tg.MessageMediaEmpty{}}},
		{name: "unsupported media", msg: &tg.Message{Media: &tg.MessageMediaUnsupported{}}},
		{name: "typed nil document media", msg: &tg.Message{Media: typedNilDocumentMedia}},
		{name: "typed nil photo media", msg: &tg.Message{Media: typedNilPhotoMedia}},
		{name: "nil document", msg: &tg.Message{Media: &tg.MessageMediaDocument{}}},
		{name: "typed nil document", msg: &tg.Message{Media: &tg.MessageMediaDocument{Document: typedNilDocument}}},
		{name: "typed nil photo", msg: &tg.Message{Media: &tg.MessageMediaPhoto{Photo: typedNilPhoto}}},
		{name: "photo without downloadable size", msg: &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}},
		{name: "typed nil attribute", msg: documentMessage(1, "application/pdf", typedNilFilename)},
		{name: "typed nil photo size", msg: photoMessageWithSizes(1, validSize, typedNilPhotoSize)},
		{name: "typed nil cached size", msg: photoMessageWithSizes(1, validSize, typedNilCachedSize)},
		{name: "typed nil progressive size", msg: photoMessageWithSizes(1, validSize, typedNilProgressiveSize)},
		{name: "other typed nil photo size", msg: photoMessageWithSizes(1, validSize, typedNilPathSize)},
		{name: "typed nil document thumb", msg: documentWithNilThumb},
		{name: "typed nil document video thumb", msg: documentWithNilVideoThumb},
		{name: "typed nil alternative document", msg: documentWithNilAlt},
		{name: "typed nil video cover", msg: documentWithNilCover},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractDownloadMedia(tt.msg)
			var badArgs *safety.BadArgs
			if !errors.As(err, &badArgs) {
				t.Fatalf("error = %T %v, want *safety.BadArgs", err, err)
			}
		})
	}
}

func TestLookupExactMessageUsesChannelMethodAndPayload(t *testing.T) {
	api := &fakeMediaDownloadAPI{resp: oneMessageResponse(77)}
	peer := &tg.InputPeerChannel{ChannelID: 123, AccessHash: 456}
	got, err := lookupExactMessage(context.Background(), api, peer, 77)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 77 || api.channelReq == nil || api.messagesCalled != 0 {
		t.Fatalf("got=%#v channelReq=%#v messagesCalled=%d", got, api.channelReq, api.messagesCalled)
	}
	channel, ok := api.channelReq.Channel.(*tg.InputChannel)
	if !ok || channel.ChannelID != 123 || channel.AccessHash != 456 {
		t.Fatalf("channel payload = %#v", api.channelReq.Channel)
	}
	assertInputMessageID(t, api.channelReq.ID, 77)
}

func TestLookupExactMessageUsesMessagesMethodForNonChannels(t *testing.T) {
	peers := []tg.InputPeerClass{
		&tg.InputPeerUser{UserID: 1, AccessHash: 2},
		&tg.InputPeerChat{ChatID: 3},
		&tg.InputPeerEmpty{},
	}
	for _, peer := range peers {
		api := &fakeMediaDownloadAPI{resp: oneMessageResponse(88)}
		got, err := lookupExactMessage(context.Background(), api, peer, 88)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != 88 || api.channelReq != nil || api.messagesCalled != 1 {
			t.Fatalf("peer=%T got=%#v channelReq=%#v messagesCalled=%d", peer, got, api.channelReq, api.messagesCalled)
		}
		assertInputMessageID(t, api.messageIDs, 88)
	}
}

func TestLookupExactMessageRequiresOneConcreteExactMatch(t *testing.T) {
	var typedNilResponse *tg.MessagesMessages
	var typedNilMessage *tg.Message
	tests := []struct {
		name string
		resp tg.MessagesMessagesClass
	}{
		{name: "nil response", resp: nil},
		{name: "typed nil response", resp: typedNilResponse},
		{name: "empty", resp: &tg.MessagesMessages{}},
		{name: "deleted", resp: &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 77}}}},
		{name: "service", resp: &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.MessageService{ID: 77}}}},
		{name: "wrong id", resp: oneMessageResponse(76)},
		{name: "zero id", resp: oneMessageResponse(0)},
		{name: "multiple", resp: &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 77}, &tg.Message{ID: 77}}}},
		{name: "typed nil message", resp: &tg.MessagesMessages{Messages: []tg.MessageClass{typedNilMessage}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookupExactMessage(context.Background(), &fakeMediaDownloadAPI{resp: tt.resp}, &tg.InputPeerChat{ChatID: 1}, 77)
			var notFound *resolve.NotFound
			if !errors.As(err, &notFound) {
				t.Fatalf("error = %T %v, want *resolve.NotFound", err, err)
			}
		})
	}
}

func TestLookupExactMessagePropagatesAPIErrorsAndCancellation(t *testing.T) {
	sentinel := errors.New("telegram unavailable")
	_, err := lookupExactMessage(context.Background(), &fakeMediaDownloadAPI{err: sentinel}, &tg.InputPeerChat{ChatID: 1}, 77)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want sentinel", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lookupExactMessage(ctx, &fakeMediaDownloadAPI{returnContextErr: true}, &tg.InputPeerChat{ChatID: 1}, 77)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestGotdDownloadMediaValidatesBeforeLookup(t *testing.T) {
	tests := []DownloadMediaReq{
		{ChatID: 0, MessageID: 1},
		{ChatID: 1, MessageID: 0},
		{ChatID: 1, MessageID: -1},
		{ChatID: 1, MessageID: int64(math.MaxInt32) + 1},
		{ChatID: 1, MessageID: 1, MaxBytes: -1},
		{ChatID: 1, MessageID: 1, OutputDir: ""},
		{ChatID: 1, MessageID: 1, OutputDir: " \t"},
	}
	for _, req := range tests {
		_, err := (&GotdClient{}).DownloadMedia(context.Background(), req)
		var badArgs *safety.BadArgs
		if !errors.As(err, &badArgs) {
			t.Fatalf("request=%#v error=%T %v, want *safety.BadArgs", req, err, err)
		}
	}
}

func TestGotdDownloadMediaStreamsAtomicallyAndReturnsSafeMetadata(t *testing.T) {
	data := []byte("telegram media")
	db, err := store.Connect(filepath.Join(t.TempDir(), "account.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.UpsertEntity(db, 321, store.EntityChannel, 654); err != nil {
		t.Fatal(err)
	}
	message := documentMessage(44, "video/mp4",
		&tg.DocumentAttributeVideo{},
		&tg.DocumentAttributeFilename{FileName: "telegram-name.mp4"},
	)
	message.ID = 77
	message.Date = 1_700_000_123
	message.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Size = int64(len(data))
	api := &fakeMediaDownloadAPI{resp: &tg.MessagesMessages{Messages: []tg.MessageClass{message}}}
	outputDir := filepath.Join(t.TempDir(), "downloads")
	downloader := &recordingFileDownloader{chunks: [][]byte{data[:4], data[4:9], data[9:]}}
	g := &GotdClient{db: db, mediaAPI: api, fileDownloader: downloader}

	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{
		ChatID: 321, MessageID: 77, OutputDir: outputDir, MaxBytes: int64(len(data)), Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(outputDir, "telegram-name.mp4")
	want := DownloadMediaResp{
		ChatID: 321, MessageID: 77, MediaType: "video", MIMEType: "video/mp4", Filename: "telegram-name.mp4",
		Path: wantPath, Bytes: int64(len(data)), MessageDate: time.Unix(1_700_000_123, 0).UTC(),
	}
	identity := got.ArtifactIdentity
	got.ArtifactIdentity = media.ArtifactIdentity{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
	if _, err := media.InspectDownloadedArtifactWithIdentity(outputDir, wantPath, identity); err != nil {
		t.Fatalf("producer artifact identity did not validate: %v", err)
	}
	if !filepath.IsAbs(got.Path) {
		t.Fatalf("path = %q, want absolute", got.Path)
	}
	if contents, readErr := os.ReadFile(got.Path); readErr != nil || !bytes.Equal(contents, data) {
		t.Fatalf("download = %q, %v", contents, readErr)
	}
	if info, statErr := os.Stat(got.Path); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v, want 0600", info, statErr)
	}
	if api.channelReq == nil {
		t.Fatal("channel lookup was not invoked")
	}
	channel := api.channelReq.Channel.(*tg.InputChannel)
	if channel.ChannelID != 321 || channel.AccessHash != 654 {
		t.Fatalf("resolved channel = %#v", channel)
	}
	wantLocation := message.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).AsInputDocumentFileLocation()
	if locations := downloader.Locations(); len(locations) != 1 || !reflect.DeepEqual(locations[0], wantLocation) {
		t.Fatalf("locations = %#v, want %#v", locations, wantLocation)
	}
}

func TestGotdDownloadMediaZeroByteAndPhotoLocation(t *testing.T) {
	message := photoMessageWithSizes(91, &tg.PhotoSize{Type: "x", W: 1, H: 1, Size: 0})
	message.ID = 17
	g, outputDir, downloader := downloadTestClient(t, message, nil)
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 17, OutputDir: outputDir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != 0 || got.Filename != "photo_91.jpg" {
		t.Fatalf("response = %#v", got)
	}
	if contents, err := os.ReadFile(got.Path); err != nil || len(contents) != 0 {
		t.Fatalf("zero-byte download = %q, %v", contents, err)
	}
	wantLocation := message.Media.(*tg.MessageMediaPhoto).Photo.(*tg.Photo)
	locations := downloader.Locations()
	if len(locations) != 1 {
		t.Fatalf("locations = %#v", locations)
	}
	photoLocation, ok := locations[0].(*tg.InputPhotoFileLocation)
	if !ok || photoLocation.ID != wantLocation.ID || photoLocation.ThumbSize != "x" {
		t.Fatalf("location = %#v", locations[0])
	}
}

func TestGotdDownloadMediaPhotoSizeMatchesSelectedLocation(t *testing.T) {
	message := photoMessageWithSizes(92,
		&tg.PhotoSize{Type: "x", W: 100, H: 100, Size: 9},
		&tg.PhotoSize{Type: "y", W: 200, H: 200, Size: 4},
	)
	message.ID = 33
	g, outputDir, downloader := downloadTestClient(t, message, []byte("data"))
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 33, OutputDir: outputDir, MaxBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != 4 {
		t.Fatalf("bytes = %d, want selected thumbnail size 4", got.Bytes)
	}
	locations := downloader.Locations()
	if len(locations) != 1 {
		t.Fatalf("locations = %#v, want one selected y thumbnail", locations)
	}
	location, ok := locations[0].(*tg.InputPhotoFileLocation)
	if !ok || location.ThumbSize != "y" {
		t.Fatalf("locations = %#v, want selected y thumbnail", locations)
	}
}

func TestGotdDownloadMediaKnownSizeLimitRejectsBeforeDestinationAndDownloader(t *testing.T) {
	message := documentMessageWithSize(18, 10, "application/pdf", &tg.DocumentAttributeFilename{FileName: "large.pdf"})
	g, outputDir, downloader := downloadTestClient(t, message, []byte("unused"))
	_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 18, OutputDir: outputDir, MaxBytes: 9})
	var badArgs *safety.BadArgs
	if !errors.As(err, &badArgs) || !errors.Is(err, media.ErrLimitExceeded) {
		t.Fatalf("error = %T %v, want BadArgs and ErrLimitExceeded", err, err)
	}
	if downloader.Calls() != 0 {
		t.Fatalf("downloader calls = %d, want 0", downloader.Calls())
	}
	if _, statErr := os.Stat(outputDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output directory stat = %v, want not exist", statErr)
	}
}

func TestGotdDownloadMediaUnknownSizeLimitIsEnforcedWhileStreaming(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "exact limit", data: []byte("1234")},
		{name: "one over", data: []byte("12345"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := documentMessageWithSize(19, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "unknown.bin"})
			g, outputDir, _ := downloadTestClient(t, message, tc.data)
			got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 19, OutputDir: outputDir, MaxBytes: 4})
			if tc.wantErr {
				if !errors.Is(err, media.ErrLimitExceeded) {
					t.Fatalf("error = %v, want ErrLimitExceeded", err)
				}
				assertDownloadDirClean(t, outputDir, "unknown.bin")
				return
			}
			if err != nil || got.Bytes != 4 {
				t.Fatalf("response=%#v error=%v", got, err)
			}
		})
	}
}

func TestGotdDownloadMediaAuthoritativeSizeDetectsTruncation(t *testing.T) {
	message := documentMessageWithSize(20, 5, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "short.bin"})
	g, outputDir, _ := downloadTestClient(t, message, []byte("1234"))
	_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 20, OutputDir: outputDir})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
	}
	assertDownloadDirClean(t, outputDir, "short.bin")
}

func TestGotdDownloadMediaTransferErrorsAndCancellationCleanParts(t *testing.T) {
	transferErr := errors.New("telegram transfer failed")
	for _, tc := range []struct {
		name       string
		downloader *recordingFileDownloader
		want       error
	}{
		{name: "midstream error", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("partial")}, err: transferErr}, want: transferErr},
		{name: "cancellation", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("partial")}, cancelAfterWrite: true}, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := documentMessageWithSize(21, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "failed.bin"})
			g, outputDir, _ := downloadTestClientWithDownloader(t, message, tc.downloader)
			_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 21, OutputDir: outputDir})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			assertDownloadDirClean(t, outputDir, "failed.bin")
		})
	}
}

func TestGotdDownloadMediaJoinsTransferAndAbortCleanupErrors(t *testing.T) {
	primary := errors.New("download failed")
	cleanup := errors.New("remove part failed")
	destination := &fakeDownloadDestination{abortErr: errors.Join(media.ErrCleanupIncomplete, cleanup)}
	message := documentMessageWithSize(22, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "failed.bin"})
	g, _, _ := downloadTestClientWithDownloader(t, message, &recordingFileDownloader{err: primary})
	g.destinationOpener = fakeDestinationOpener{destination: destination}
	_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 22, OutputDir: t.TempDir()})
	if !errors.Is(err, primary) || !errors.Is(err, cleanup) || !errors.Is(err, media.ErrCleanupIncomplete) {
		t.Fatalf("error = %v, want primary and cleanup errors", err)
	}
	if destination.abortCalls != 1 || destination.commitCalls != 0 {
		t.Fatalf("destination calls abort=%d commit=%d", destination.abortCalls, destination.commitCalls)
	}
}

func TestGotdDownloadMediaJoinsCommitAndAbortErrors(t *testing.T) {
	commitErr := errors.New("commit failed")
	cleanupErr := errors.New("abort failed")
	destination := &fakeDownloadDestination{
		path:      filepath.Join(t.TempDir(), "commit.bin"),
		commitErr: commitErr,
		abortErr:  errors.Join(media.ErrCleanupIncomplete, cleanupErr),
	}
	message := documentMessageWithSize(30, 4, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "commit.bin"})
	g, _, _ := downloadTestClientWithDownloader(t, message, &recordingFileDownloader{chunks: [][]byte{[]byte("data")}})
	g.destinationOpener = fakeDestinationOpener{destination: destination}
	_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 30, OutputDir: t.TempDir()})
	if !errors.Is(err, commitErr) || !errors.Is(err, cleanupErr) || !errors.Is(err, media.ErrCleanupIncomplete) {
		t.Fatalf("error = %v, want commit and cleanup errors", err)
	}
	if destination.commitCalls != 1 || destination.abortCalls != 1 {
		t.Fatalf("destination calls commit=%d abort=%d", destination.commitCalls, destination.abortCalls)
	}
}

func TestGotdDownloadMediaPublishedCommitErrorSignalsConservativeArtifact(t *testing.T) {
	commitErr := errors.New("directory sync failed after publish")
	outputDir := t.TempDir()
	path := filepath.Join(outputDir, "published.bin")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, _, err := media.CaptureArtifactIdentity(outputDir, path)
	if err != nil {
		t.Fatal(err)
	}
	destination := &fakeDownloadDestination{
		path:      path,
		identity:  identity,
		commitErr: commitErr,
		abortErr:  media.ErrDestinationCommitted,
	}
	message := documentMessageWithSize(32, 4, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "published.bin"})
	g, _, _ := downloadTestClientWithDownloader(t, message, &recordingFileDownloader{chunks: [][]byte{[]byte("data")}})
	g.destinationOpener = fakeDestinationOpener{destination: destination}
	resp, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 32, OutputDir: outputDir})
	var committed *CommittedMediaDownloadError
	if !errors.Is(err, commitErr) || !errors.Is(err, media.ErrDestinationCommitted) || !errors.Is(err, media.ErrCleanupIncomplete) {
		t.Fatalf("error = %v, want commit, committed, and cleanup-incomplete errors", err)
	}
	if !errors.As(err, &committed) || !reflect.DeepEqual(committed.Response, resp) || resp.Path != path || resp.Bytes != 4 || resp.Skipped {
		t.Fatalf("response=%#v committed=%#v error=%v", resp, committed, err)
	}
	if _, err := media.InspectDownloadedArtifactWithIdentity(outputDir, path, resp.ArtifactIdentity); err != nil {
		t.Fatalf("committed response identity did not validate: %v", err)
	}
}

func TestGotdDownloadMediaExistingRegularSkipsWithoutDownloading(t *testing.T) {
	message := documentMessageWithSize(23, 3, "text/plain", &tg.DocumentAttributeFilename{FileName: "existing.txt"})
	g, outputDir, downloader := downloadTestClient(t, message, []byte("new"))
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(outputDir, "existing.txt")
	if err := os.WriteFile(final, []byte("old data"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 23, OutputDir: outputDir})
	if err != nil || !got.Skipped || got.Path != final || got.Bytes != 8 {
		t.Fatalf("response=%#v error=%v", got, err)
	}
	if _, err := media.InspectDownloadedArtifactWithIdentity(outputDir, final, got.ArtifactIdentity); err != nil {
		t.Fatalf("collision artifact identity did not validate: %v", err)
	}
	if downloader.Calls() != 0 {
		t.Fatalf("downloader calls = %d", downloader.Calls())
	}
	assertFileState(t, final, "old data", 0o640)
	assertNoDownloadArtifacts(t, outputDir)
}

func TestGotdDownloadMediaInitialCollisionUsesAnchoredSnapshotAfterSwap(t *testing.T) {
	for _, swapKind := range []string{"final", "parent"} {
		t.Run(swapKind, func(t *testing.T) {
			message := documentMessageWithSize(34, -1, "text/plain", &tg.DocumentAttributeFilename{FileName: "existing.txt"})
			g, outputDir, downloader := downloadTestClient(t, message, []byte("unused"))
			if err := os.MkdirAll(outputDir, 0o700); err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(outputDir, "existing.txt")
			if err := os.WriteFile(final, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(victim, []byte("outside victim data"), 0o600); err != nil {
				t.Fatal(err)
			}
			g.destinationOpener = swapAfterCollisionOpener{swap: func() { swapCollisionPath(t, swapKind, outputDir, final) }}

			got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 34, OutputDir: outputDir})
			if err != nil || !got.Skipped || got.Path != final || got.Bytes != 3 {
				t.Fatalf("response=%#v error=%v, want anchored original size=3", got, err)
			}
			if downloader.Calls() != 0 {
				t.Fatalf("downloader calls = %d", downloader.Calls())
			}
			assertFileState(t, victim, "outside victim data", 0o600)
		})
	}
}

func TestGotdDownloadMediaCollisionCleanupFailureIsNotSkipped(t *testing.T) {
	message := documentMessageWithSize(39, -1, "text/plain", &tg.DocumentAttributeFilename{FileName: "existing.txt"})
	g, _, downloader := downloadTestClient(t, message, []byte("unused"))
	cleanupErr := errors.New("close anchored directory failed")
	g.destinationOpener = fakeDestinationOpener{err: errors.Join(
		&media.DestinationExistsError{FinalPath: "/logical/existing.txt", Size: 3},
		media.ErrCleanupIncomplete,
		cleanupErr,
	)}
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 39, OutputDir: t.TempDir()})
	var collision *media.DestinationExistsError
	if got.Skipped || !errors.As(err, &collision) || !errors.Is(err, media.ErrCleanupIncomplete) || !errors.Is(err, cleanupErr) {
		t.Fatalf("response=%#v error=%v collision=%#v, want collision+cleanup error without skip", got, err, collision)
	}
	if downloader.Calls() != 0 {
		t.Fatalf("downloader calls = %d", downloader.Calls())
	}
}

func TestGotdDownloadMediaOverwriteSuccessAndFailuresPreserveOriginal(t *testing.T) {
	transferErr := errors.New("transfer failed")
	for _, tc := range []struct {
		name       string
		downloader *recordingFileDownloader
		max        int64
		wantErr    error
		want       string
	}{
		{name: "success", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("new")}}, want: "new"},
		{name: "transfer failure", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("ne")}, err: transferErr}, wantErr: transferErr, want: "old"},
		{name: "cancellation", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("ne")}, cancelAfterWrite: true}, wantErr: context.Canceled, want: "old"},
		{name: "limit failure", downloader: &recordingFileDownloader{chunks: [][]byte{[]byte("new!")}}, max: 3, wantErr: media.ErrLimitExceeded, want: "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := documentMessageWithSize(24, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "same.bin"})
			g, outputDir, _ := downloadTestClientWithDownloader(t, message, tc.downloader)
			if err := os.MkdirAll(outputDir, 0o700); err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(outputDir, "same.bin")
			if err := os.WriteFile(final, []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}
			_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 24, OutputDir: outputDir, Overwrite: true, MaxBytes: tc.max})
			if tc.wantErr == nil && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			wantMode := os.FileMode(0o600)
			if tc.want != "new" {
				wantMode = 0o640
			}
			assertFileState(t, final, tc.want, wantMode)
			assertNoDownloadArtifacts(t, outputDir)
		})
	}
}

func TestGotdDownloadMediaUnsafeExistingTargetsAreNeverSkipped(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			message := documentMessageWithSize(25, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "target.bin"})
			g, outputDir, downloader := downloadTestClient(t, message, []byte("new"))
			if err := os.MkdirAll(outputDir, 0o700); err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(outputDir, "target.bin")
			var referent string
			if kind == "directory" {
				if err := os.Mkdir(final, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				referent = filepath.Join(t.TempDir(), "referent")
				if err := os.WriteFile(referent, []byte("safe"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(referent, final); err != nil {
					t.Fatal(err)
				}
			}
			got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 25, OutputDir: outputDir})
			if !errors.Is(err, media.ErrUnsafeDestination) || got.Skipped {
				t.Fatalf("response=%#v error=%v", got, err)
			}
			if downloader.Calls() != 0 {
				t.Fatalf("downloader calls = %d", downloader.Calls())
			}
			if referent != "" {
				assertFileState(t, referent, "safe", 0o640)
			}
		})
	}
}

func TestGotdDownloadMediaCollisionDuringTransferReturnsSafeSkip(t *testing.T) {
	message := documentMessageWithSize(26, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "race.bin"})
	outputRoot := t.TempDir()
	outputDir := filepath.Join(outputRoot, "downloads")
	final := filepath.Join(outputDir, "race.bin")
	downloader := &recordingFileDownloader{chunks: [][]byte{[]byte("loser")}, afterWrite: func() error {
		return os.WriteFile(final, []byte("winner"), 0o640)
	}}
	g, _, _ := downloadTestClientAt(t, message, downloader, outputDir)
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 26, OutputDir: outputDir})
	if err != nil || !got.Skipped || got.Bytes != 6 {
		t.Fatalf("response=%#v error=%v", got, err)
	}
	if _, err := media.InspectDownloadedArtifactWithIdentity(outputDir, final, got.ArtifactIdentity); err != nil {
		t.Fatalf("commit collision identity did not validate: %v", err)
	}
	assertFileState(t, final, "winner", 0o640)
	assertNoDownloadArtifacts(t, outputDir)
}

func TestGotdDownloadMediaCommitCollisionUsesAnchoredSnapshotAfterSwap(t *testing.T) {
	for _, swapKind := range []string{"final", "parent"} {
		t.Run(swapKind, func(t *testing.T) {
			message := documentMessageWithSize(35, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "race.bin"})
			outputDir := filepath.Join(t.TempDir(), "downloads")
			final := filepath.Join(outputDir, "race.bin")
			victim := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(victim, []byte("outside victim data"), 0o600); err != nil {
				t.Fatal(err)
			}
			downloader := &recordingFileDownloader{chunks: [][]byte{[]byte("loser")}, afterWrite: func() error {
				return os.WriteFile(final, []byte("winner"), 0o600)
			}}
			g, _, _ := downloadTestClientAt(t, message, downloader, outputDir)
			g.destinationOpener = swapCommitCollisionOpener{swap: func() { swapCollisionPath(t, swapKind, outputDir, final) }}

			got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 35, OutputDir: outputDir})
			if err != nil || !got.Skipped || got.Path != final || got.Bytes != 6 {
				t.Fatalf("response=%#v error=%v, want anchored winner size=6", got, err)
			}
			assertFileState(t, victim, "outside victim data", 0o600)
		})
	}
}

func TestGotdDownloadMediaMapsTransferRPCErrors(t *testing.T) {
	message := documentMessageWithSize(36, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "rpc.bin"})
	g, outputDir, _ := downloadTestClientWithDownloader(t, message, &recordingFileDownloader{err: tgerr.New(420, "FLOOD_WAIT_7")})
	_, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 36, OutputDir: outputDir})
	var floodWait *safety.FloodWait
	if !errors.As(err, &floodWait) || floodWait.Seconds != 7 {
		t.Fatalf("error = %T %v, want FloodWait(7)", err, err)
	}
	assertDownloadDirClean(t, outputDir, "rpc.bin")
}

func TestGotdDownloadMediaJoinsConcurrentCancellationAndMappedTransferError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	message := documentMessageWithSize(37, -1, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "cancel-rpc.bin"})
	downloader := &recordingFileDownloader{
		chunks:     [][]byte{[]byte("partial")},
		afterWrite: func() error { cancel(); return nil },
		err:        tgerr.New(420, "FLOOD_WAIT_8"),
	}
	g, outputDir, _ := downloadTestClientWithDownloader(t, message, downloader)
	_, err := g.DownloadMedia(ctx, DownloadMediaReq{ChatID: 321, MessageID: 37, OutputDir: outputDir})
	var floodWait *safety.FloodWait
	if !errors.As(err, &floodWait) || floodWait.Seconds != 8 || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %T %v, want FloodWait(8) joined with canceled", err, err)
	}
	assertDownloadDirClean(t, outputDir, "cancel-rpc.bin")
}

func TestGotdDownloadMediaCancellationAfterSuccessfulStreamAbortsBeforeCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	message := documentMessageWithSize(38, 4, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "cancel-after.bin"})
	downloader := &recordingFileDownloader{
		chunks:     [][]byte{[]byte("data")},
		afterWrite: func() error { cancel(); return nil },
	}
	g, outputDir, _ := downloadTestClientWithDownloader(t, message, downloader)
	_, err := g.DownloadMedia(ctx, DownloadMediaReq{ChatID: 321, MessageID: 38, OutputDir: outputDir})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	assertDownloadDirClean(t, outputDir, "cancel-after.bin")
}

func TestGotdDownloadMediaRelativeOutputAndTraversalNameStayContained(t *testing.T) {
	message := documentMessageWithSize(27, 4, "text/plain", &tg.DocumentAttributeFilename{FileName: "../../escape.txt"})
	absOutput := filepath.Join(t.TempDir(), "relative-download")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relOutput, err := filepath.Rel(wd, absOutput)
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := downloadTestClientAt(t, message, &recordingFileDownloader{chunks: [][]byte{[]byte("safe")}}, relOutput)
	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 27, OutputDir: relOutput})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.Path) || filepath.Dir(got.Path) != absOutput || got.Filename != "escape.txt" {
		t.Fatalf("response = %#v, want absolute contained sanitized path", got)
	}
	assertFileState(t, got.Path, "safe", 0o600)
}

func TestGotdDownloadMediaConcurrentCallsHaveIsolatedDestinations(t *testing.T) {
	message := documentMessageWithSize(28, 4, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "same.bin"})
	downloader := &recordingFileDownloader{chunks: [][]byte{[]byte("data")}}
	g, _, _ := downloadTestClientWithDownloader(t, message, downloader)
	const calls = 12
	results := make(chan error, calls)
	for i := 0; i < calls; i++ {
		dir := filepath.Join(t.TempDir(), fmt.Sprintf("d-%d", i))
		go func() {
			resp, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 28, OutputDir: dir})
			if err == nil {
				contents, readErr := os.ReadFile(resp.Path)
				if readErr != nil || string(contents) != "data" {
					err = fmt.Errorf("read %q: %w", contents, readErr)
				}
			}
			results <- err
		}()
	}
	for i := 0; i < calls; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if downloader.Calls() != calls {
		t.Fatalf("calls = %d, want %d", downloader.Calls(), calls)
	}
}

func TestGotdDownloadMediaConcurrentSameTargetPublishesOnce(t *testing.T) {
	message := documentMessageWithSize(31, 4, "application/octet-stream", &tg.DocumentAttributeFilename{FileName: "same.bin"})
	downloader := &recordingFileDownloader{
		chunks:       [][]byte{[]byte("data")},
		waitForCalls: 2,
		allCalled:    make(chan struct{}),
	}
	g, outputDir, _ := downloadTestClientWithDownloader(t, message, downloader)
	results := make(chan DownloadMediaResp, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := g.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 321, MessageID: 31, OutputDir: outputDir})
			results <- resp
			errs <- err
		}()
	}
	skipped := 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if (<-results).Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("skipped calls = %d, want exactly one", skipped)
	}
	assertFileState(t, filepath.Join(outputDir, "same.bin"), "data", 0o600)
	assertNoDownloadArtifacts(t, outputDir)
}

func TestGotdFileDownloaderRejectsUninitializedAdapter(t *testing.T) {
	var out bytes.Buffer
	err := (gotdFileDownloader{}).Download(context.Background(), &tg.InputDocumentFileLocation{}, &out)
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("error = %v, want initialization error", err)
	}
}

var _ fileDownloader = gotdFileDownloader{}

func downloadTestClient(t *testing.T, message *tg.Message, data []byte) (*GotdClient, string, *recordingFileDownloader) {
	t.Helper()
	downloader := &recordingFileDownloader{chunks: [][]byte{data}}
	return downloadTestClientWithDownloader(t, message, downloader)
}

func downloadTestClientWithDownloader(t *testing.T, message *tg.Message, downloader *recordingFileDownloader) (*GotdClient, string, *recordingFileDownloader) {
	t.Helper()
	return downloadTestClientAt(t, message, downloader, filepath.Join(t.TempDir(), "downloads"))
}

func downloadTestClientAt(t *testing.T, message *tg.Message, downloader *recordingFileDownloader, outputDir string) (*GotdClient, string, *recordingFileDownloader) {
	t.Helper()
	db, err := store.Connect(filepath.Join(t.TempDir(), "account.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.UpsertEntity(db, 321, store.EntityChannel, 654); err != nil {
		t.Fatal(err)
	}
	api := &fakeMediaDownloadAPI{resp: &tg.MessagesMessages{Messages: []tg.MessageClass{message}}}
	return &GotdClient{db: db, mediaAPI: api, fileDownloader: downloader}, outputDir, downloader
}

func documentMessageWithSize(id, size int64, mime string, attrs ...tg.DocumentAttributeClass) *tg.Message {
	message := documentMessage(id, mime, attrs...)
	message.ID = int(id)
	message.Media.(*tg.MessageMediaDocument).Document.(*tg.Document).Size = size
	return message
}

func assertDownloadDirClean(t *testing.T, dir, finalName string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, finalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final exists or stat failed: %v", err)
	}
	assertNoDownloadArtifacts(t, dir)
}

func assertNoDownloadArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") || strings.Contains(entry.Name(), ".tgctl-") {
			t.Fatalf("staged artifact remains: %s", entry.Name())
		}
	}
}

func assertFileState(t *testing.T, path, want string, wantMode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != wantMode {
		t.Fatalf("%s mode = %#o, want %#o", path, info.Mode().Perm(), wantMode)
	}
}

type recordingFileDownloader struct {
	mu               sync.Mutex
	chunks           [][]byte
	err              error
	cancelAfterWrite bool
	afterWrite       func() error
	locations        []tg.InputFileLocationClass
	calls            int
	waitForCalls     int
	allCalled        chan struct{}
	allCalledOnce    sync.Once
}

func (d *recordingFileDownloader) Download(ctx context.Context, location tg.InputFileLocationClass, out io.Writer) error {
	d.mu.Lock()
	d.calls++
	d.locations = append(d.locations, location)
	chunks := make([][]byte, len(d.chunks))
	for i := range d.chunks {
		chunks[i] = append([]byte(nil), d.chunks[i]...)
	}
	afterWrite := d.afterWrite
	cancelAfterWrite := d.cancelAfterWrite
	wantErr := d.err
	waitForCalls := d.waitForCalls
	allCalled := d.allCalled
	if waitForCalls > 0 && d.calls >= waitForCalls {
		d.allCalledOnce.Do(func() { close(allCalled) })
	}
	d.mu.Unlock()
	if waitForCalls > 0 {
		select {
		case <-allCalled:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := out.Write(chunk); err != nil {
			return err
		}
	}
	if afterWrite != nil {
		if err := afterWrite(); err != nil {
			return err
		}
	}
	if cancelAfterWrite {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()
		return cancelCtx.Err()
	}
	return wantErr
}

func (d *recordingFileDownloader) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *recordingFileDownloader) Locations() []tg.InputFileLocationClass {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]tg.InputFileLocationClass(nil), d.locations...)
}

type fakeDownloadDestination struct {
	bytes.Buffer
	path        string
	identity    media.ArtifactIdentity
	commitErr   error
	abortErr    error
	commitCalls int
	abortCalls  int
}

func (d *fakeDownloadDestination) FinalPath() string { return d.path }
func (d *fakeDownloadDestination) ArtifactIdentity() media.ArtifactIdentity {
	return d.identity
}
func (d *fakeDownloadDestination) Commit() error { d.commitCalls++; return d.commitErr }
func (d *fakeDownloadDestination) Abort() error  { d.abortCalls++; return d.abortErr }

type fakeDestinationOpener struct {
	destination downloadDestination
	err         error
}

func (o fakeDestinationOpener) Open(string, string, bool) (downloadDestination, error) {
	return o.destination, o.err
}

type swapAfterCollisionOpener struct{ swap func() }

func (o swapAfterCollisionOpener) Open(dir, name string, overwrite bool) (downloadDestination, error) {
	destination, err := (atomicDestinationOpener{}).Open(dir, name, overwrite)
	if errors.Is(err, media.ErrDestinationExists) {
		o.swap()
	}
	return destination, err
}

type swapCommitCollisionOpener struct{ swap func() }

func (o swapCommitCollisionOpener) Open(dir, name string, overwrite bool) (downloadDestination, error) {
	destination, err := (atomicDestinationOpener{}).Open(dir, name, overwrite)
	if err != nil {
		return nil, err
	}
	return &swapCommitCollisionDestination{downloadDestination: destination, swap: o.swap}, nil
}

type swapCommitCollisionDestination struct {
	downloadDestination
	swap func()
}

func (d *swapCommitCollisionDestination) Commit() error {
	err := d.downloadDestination.Commit()
	if errors.Is(err, media.ErrDestinationExists) {
		d.swap()
	}
	return err
}

func swapCollisionPath(t *testing.T, kind, outputDir, final string) {
	t.Helper()
	switch kind {
	case "final":
		if err := os.Rename(final, final+".anchored"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(final, []byte("replacement path data"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "parent":
		if err := os.Rename(outputDir, outputDir+".anchored"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(outputDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(final, []byte("replacement path data"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown swap kind %q", kind)
	}
}

func photoMessage(id int64) *tg.Message {
	return photoMessageWithSizes(id, &tg.PhotoSize{Type: "x", W: 100, H: 100, Size: 42})
}

func photoMessageWithSizes(id int64, sizes ...tg.PhotoSizeClass) *tg.Message {
	return &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: id, Sizes: sizes}}}
}

func documentMessage(id int64, mime string, attrs ...tg.DocumentAttributeClass) *tg.Message {
	return &tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{
		ID: id, MimeType: mime, Size: 42, Attributes: attrs,
	}}}
}

func reverseAttributes(in []tg.DocumentAttributeClass) []tg.DocumentAttributeClass {
	out := append([]tg.DocumentAttributeClass(nil), in...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

type fakeMediaDownloadAPI struct {
	mu               sync.Mutex
	resp             tg.MessagesMessagesClass
	err              error
	returnContextErr bool
	channelReq       *tg.ChannelsGetMessagesRequest
	messageIDs       []tg.InputMessageClass
	messagesCalled   int
}

func (f *fakeMediaDownloadAPI) ChannelsGetMessages(ctx context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channelReq = req
	if f.returnContextErr {
		return nil, ctx.Err()
	}
	return f.resp, f.err
}

func (f *fakeMediaDownloadAPI) MessagesGetMessages(ctx context.Context, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messagesCalled++
	f.messageIDs = ids
	if f.returnContextErr {
		return nil, ctx.Err()
	}
	return f.resp, f.err
}

func oneMessageResponse(id int) tg.MessagesMessagesClass {
	return &tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: id}}}
}

func assertInputMessageID(t *testing.T, ids []tg.InputMessageClass, want int) {
	t.Helper()
	if len(ids) != 1 {
		t.Fatalf("IDs = %#v, want one", ids)
	}
	id, ok := ids[0].(*tg.InputMessageID)
	if !ok || id == nil || id.ID != want {
		t.Fatalf("ID payload = %#v, want InputMessageID(%d)", ids[0], want)
	}
}
