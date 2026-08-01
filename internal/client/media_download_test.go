package client

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

func TestDownloadMediaContractAndFake(t *testing.T) {
	wantReq := DownloadMediaReq{
		ChatID: 42, MessageID: 99, OutputDir: "raw/output", MaxBytes: 1024, Overwrite: true,
	}
	wantResp := DownloadMediaResp{
		ChatID: 42, MessageID: 99, MediaType: "video", MIMEType: "video/mp4",
		Filename: "clip.mp4", Path: "/tmp/clip.mp4", Bytes: 123, Skipped: true,
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
	fake := &FakeClient{}
	const calls = 32
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_, _ = fake.DownloadMedia(context.Background(), DownloadMediaReq{ChatID: 1, MessageID: id})
		}(int64(i + 1))
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
	attrs := []tg.DocumentAttributeClass{
		&tg.DocumentAttributeAnimated{},
		&tg.DocumentAttributeFilename{FileName: "mixed.bin"},
		&tg.DocumentAttributeSticker{},
		&tg.DocumentAttributeAudio{Voice: true},
		&tg.DocumentAttributeVideo{},
		&tg.DocumentAttributeVideo{RoundMessage: true},
	}
	for _, ordered := range [][]tg.DocumentAttributeClass{attrs, reverseAttributes(attrs)} {
		got, err := extractDownloadMedia(documentMessage(21, "application/octet-stream", ordered...))
		if err != nil {
			t.Fatal(err)
		}
		if got.MediaType != "video_note" || got.Filename != "mixed.bin" {
			t.Fatalf("metadata = %#v, want video_note and Telegram filename", got)
		}
	}
}

func TestExtractDownloadMediaMalformedIsBadArgsWithoutPanic(t *testing.T) {
	var typedNilMessage *tg.Message
	var typedNilDocumentMedia *tg.MessageMediaDocument
	var typedNilPhotoMedia *tg.MessageMediaPhoto
	var typedNilDocument *tg.Document
	var typedNilPhoto *tg.Photo
	var typedNilFilename *tg.DocumentAttributeFilename

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
	}
	for _, req := range tests {
		_, err := (&GotdClient{}).DownloadMedia(context.Background(), req)
		var badArgs *safety.BadArgs
		if !errors.As(err, &badArgs) {
			t.Fatalf("request=%#v error=%T %v, want *safety.BadArgs", req, err, err)
		}
	}
}

func TestGotdDownloadMediaResolvesExtractsThenStopsWithoutFilesystemMutation(t *testing.T) {
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
	api := &fakeMediaDownloadAPI{resp: &tg.MessagesMessages{Messages: []tg.MessageClass{message}}}
	outputDir := filepath.Join(t.TempDir(), "must-not-be-created")
	g := &GotdClient{db: db, mediaAPI: api}

	got, err := g.DownloadMedia(context.Background(), DownloadMediaReq{
		ChatID: 321, MessageID: 77, OutputDir: outputDir, MaxBytes: 1234, Overwrite: true,
	})
	if !errors.Is(err, ErrMediaDownloadNotImplemented) {
		t.Fatalf("error = %v, want stable intermediate error", err)
	}
	want := DownloadMediaResp{
		ChatID: 321, MessageID: 77, MediaType: "video", MIMEType: "video/mp4", Filename: "telegram-name.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
	if _, statErr := os.Stat(outputDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("output directory was created or unexpected stat error: %v", statErr)
	}
	if api.channelReq == nil {
		t.Fatal("channel lookup was not invoked")
	}
	channel := api.channelReq.Channel.(*tg.InputChannel)
	if channel.ChannelID != 321 || channel.AccessHash != 654 {
		t.Fatalf("resolved channel = %#v", channel)
	}
}

func photoMessage(id int64) *tg.Message {
	return &tg.Message{Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{
		ID: id, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 100, H: 100, Size: 42}},
	}}}
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
	resp             tg.MessagesMessagesClass
	err              error
	returnContextErr bool
	channelReq       *tg.ChannelsGetMessagesRequest
	messageIDs       []tg.InputMessageClass
	messagesCalled   int
}

func (f *fakeMediaDownloadAPI) ChannelsGetMessages(ctx context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.channelReq = req
	if f.returnContextErr {
		return nil, ctx.Err()
	}
	return f.resp, f.err
}

func (f *fakeMediaDownloadAPI) MessagesGetMessages(ctx context.Context, ids []tg.InputMessageClass) (tg.MessagesMessagesClass, error) {
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
