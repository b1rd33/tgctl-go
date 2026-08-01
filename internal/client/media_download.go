package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

// ErrMediaDownloadNotImplemented marks the Task 6 intermediate boundary:
// lookup and metadata extraction succeeded, but no bytes were streamed and no
// destination was touched. Task 7 replaces this return with streaming.
var ErrMediaDownloadNotImplemented = errors.New("media download streaming is not implemented")

type mediaDownloadAPI interface {
	ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
	MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
}

type extractedDownloadMedia struct {
	MediaType string
	MIMEType  string
	Filename  string
	Size      int64
	File      messages.File
}

func (g *GotdClient) DownloadMedia(ctx context.Context, req DownloadMediaReq) (DownloadMediaResp, error) {
	if req.ChatID == 0 {
		return DownloadMediaResp{}, safety.NewBadArgs("chat_id cannot be 0")
	}
	if req.MessageID <= 0 || req.MessageID > math.MaxInt32 {
		return DownloadMediaResp{}, safety.NewBadArgs("message_id must be between 1 and %d", math.MaxInt32)
	}
	if req.MaxBytes < 0 {
		return DownloadMediaResp{}, safety.NewBadArgs("max_bytes cannot be negative")
	}
	if err := ctx.Err(); err != nil {
		return DownloadMediaResp{}, err
	}

	peer, err := g.peerFromChatID(ctx, req.ChatID)
	if err != nil {
		return DownloadMediaResp{}, err
	}
	api := g.mediaAPI
	if api == nil {
		api = g.api
	}
	if api == nil {
		return DownloadMediaResp{}, errors.New("Telegram media API is not initialized")
	}
	message, err := lookupExactMessage(ctx, api, peer, int(req.MessageID))
	if err != nil {
		return DownloadMediaResp{}, mapRPCErr(err)
	}
	media, err := extractDownloadMedia(message)
	if err != nil {
		return DownloadMediaResp{}, err
	}

	return DownloadMediaResp{
		ChatID: req.ChatID, MessageID: req.MessageID,
		MediaType: media.MediaType, MIMEType: media.MIMEType, Filename: media.Filename,
	}, ErrMediaDownloadNotImplemented
}

func lookupExactMessage(ctx context.Context, api mediaDownloadAPI, peer tg.InputPeerClass, messageID int) (*tg.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}}
	var (
		resp tg.MessagesMessagesClass
		err  error
	)
	if channel, ok := peer.(*tg.InputPeerChannel); ok {
		if channel == nil {
			return nil, safety.NewBadArgs("resolved channel peer is nil")
		}
		resp, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      ids,
		})
	} else {
		resp, err = api.MessagesGetMessages(ctx, ids)
	}
	if err != nil {
		return nil, err
	}

	items := messagesFromHistoryResp(resp)
	if len(items) != 1 {
		return nil, resolve.NewNotFound("message %d was not found", messageID)
	}
	message, ok := items[0].(*tg.Message)
	if !ok || message == nil || message.ID != messageID {
		return nil, resolve.NewNotFound("message %d was not found", messageID)
	}
	return message, nil
}

func extractDownloadMedia(message *tg.Message) (extractedDownloadMedia, error) {
	if message == nil {
		return extractedDownloadMedia{}, safety.NewBadArgs("message has no downloadable media")
	}

	switch media := message.Media.(type) {
	case *tg.MessageMediaPhoto:
		if media == nil {
			return extractedDownloadMedia{}, safety.NewBadArgs("message has malformed photo media")
		}
		photo, ok := media.Photo.(*tg.Photo)
		if !ok || photo == nil {
			return extractedDownloadMedia{}, safety.NewBadArgs("message has no downloadable photo")
		}
		if err := validateNoTypedNil("photo sizes", photo.Sizes); err != nil {
			return extractedDownloadMedia{}, err
		}
		if err := validateNoTypedNil("photo video sizes", photo.VideoSizes); err != nil {
			return extractedDownloadMedia{}, err
		}
		if media.Video != nil && isTypedNil(media.Video) {
			return extractedDownloadMedia{}, safety.NewBadArgs("photo contains a malformed video document")
		}
		file, ok := (messages.Elem{Msg: message}).File()
		if !ok {
			return extractedDownloadMedia{}, safety.NewBadArgs("message photo has no downloadable file")
		}
		file.Name = fmt.Sprintf("photo_%d.jpg", photo.ID)
		file.MIMEType = "image/jpeg"
		return extractedDownloadMedia{
			MediaType: "photo", MIMEType: file.MIMEType, Filename: file.Name,
			Size: largestPhotoSize(photo.Sizes), File: file,
		}, nil

	case *tg.MessageMediaDocument:
		if media == nil {
			return extractedDownloadMedia{}, safety.NewBadArgs("message has malformed document media")
		}
		document, ok := media.Document.(*tg.Document)
		if !ok || document == nil {
			return extractedDownloadMedia{}, safety.NewBadArgs("message has no downloadable document")
		}
		if err := validateNoTypedNil("document attributes", document.Attributes); err != nil {
			return extractedDownloadMedia{}, err
		}
		if err := validateNoTypedNil("document thumbnails", document.Thumbs); err != nil {
			return extractedDownloadMedia{}, err
		}
		if err := validateNoTypedNil("document video thumbnails", document.VideoThumbs); err != nil {
			return extractedDownloadMedia{}, err
		}
		if err := validateNoTypedNil("alternative documents", media.AltDocuments); err != nil {
			return extractedDownloadMedia{}, err
		}
		if media.VideoCover != nil && isTypedNil(media.VideoCover) {
			return extractedDownloadMedia{}, safety.NewBadArgs("document contains a malformed video cover")
		}

		mediaType, filename, err := classifyDocument(media, document.Attributes)
		if err != nil {
			return extractedDownloadMedia{}, err
		}
		file, ok := (messages.Elem{Msg: message}).File()
		if !ok {
			return extractedDownloadMedia{}, safety.NewBadArgs("message document has no downloadable file")
		}
		mimeType := document.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if filename == "" {
			filename = fmt.Sprintf("document_%d%s", document.ID, extensionForMedia(mimeType, mediaType))
		}
		file.Name = filename
		file.MIMEType = mimeType
		return extractedDownloadMedia{
			MediaType: mediaType, MIMEType: mimeType, Filename: filename,
			Size: document.Size, File: file,
		}, nil
	default:
		return extractedDownloadMedia{}, safety.NewBadArgs("message has no downloadable file media")
	}
}

func classifyDocument(media *tg.MessageMediaDocument, attributes []tg.DocumentAttributeClass) (mediaType, filename string, err error) {
	var roundVideo, video, voice, audio, sticker, animated bool
	if media != nil {
		roundVideo = media.Round
		video = media.Video
		voice = media.Voice
	}
	for _, attribute := range attributes {
		if isTypedNil(attribute) {
			return "", "", safety.NewBadArgs("document contains a malformed attribute")
		}
		switch attribute := attribute.(type) {
		case *tg.DocumentAttributeVideo:
			video = true
			roundVideo = roundVideo || attribute.RoundMessage
		case *tg.DocumentAttributeAudio:
			audio = true
			voice = voice || attribute.Voice
		case *tg.DocumentAttributeSticker:
			sticker = true
		case *tg.DocumentAttributeAnimated:
			animated = true
		case *tg.DocumentAttributeFilename:
			if filename == "" && attribute.FileName != "" {
				filename = attribute.FileName
			}
		}
	}

	// Telegram's semantic attributes outrank its generic content flags. Round
	// video is the one exception because it is itself the most specific form.
	switch {
	case roundVideo:
		mediaType = "video_note"
	case sticker:
		mediaType = "sticker"
	case animated:
		mediaType = "animation"
	case voice:
		mediaType = "voice"
	case video:
		mediaType = "video"
	case audio:
		mediaType = "audio"
	default:
		mediaType = "document"
	}
	return mediaType, filename, nil
}

func validateNoTypedNil[T any](name string, values []T) error {
	for _, value := range values {
		if isTypedNil(any(value)) {
			return safety.NewBadArgs("%s contain a malformed value", name)
		}
	}
	return nil
}

func isTypedNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}

func largestPhotoSize(sizes []tg.PhotoSizeClass) int64 {
	var largest int64
	for _, size := range sizes {
		switch size := size.(type) {
		case *tg.PhotoSize:
			if size != nil && int64(size.Size) > largest {
				largest = int64(size.Size)
			}
		case *tg.PhotoSizeProgressive:
			if size != nil {
				for _, candidate := range size.Sizes {
					if int64(candidate) > largest {
						largest = int64(candidate)
					}
				}
			}
		case *tg.PhotoCachedSize:
			if size != nil && int64(len(size.Bytes)) > largest {
				largest = int64(len(size.Bytes))
			}
		}
	}
	return largest
}

func extensionForMedia(mimeType, mediaType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/mpeg":
		return ".mpeg"
	case "video/ogg", "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/aac":
		return ".aac"
	case "audio/webm":
		return ".webm"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "application/x-tgsticker":
		return ".tgs"
	case "text/plain":
		return ".txt"
	}
	switch mediaType {
	case "video", "video_note":
		return ".mp4"
	case "voice":
		return ".ogg"
	case "audio":
		return ".mp3"
	case "sticker":
		return ".webp"
	case "animation":
		return ".gif"
	default:
		return ".bin"
	}
}
