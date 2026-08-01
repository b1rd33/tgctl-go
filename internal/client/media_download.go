package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

type fileDownloader interface {
	Download(context.Context, tg.InputFileLocationClass, io.Writer) error
}

type gotdFileDownloader struct {
	client *telegram.Client
	api    *tg.Client
}

func (d gotdFileDownloader) Download(ctx context.Context, location tg.InputFileLocationClass, out io.Writer) error {
	if d.client == nil || d.api == nil {
		return errors.New("Telegram media downloader is not initialized")
	}
	_, err := d.client.Downloader().Download(d.api, location).Stream(ctx, out)
	return err
}

type downloadDestination interface {
	io.Writer
	FinalPath() string
	ArtifactIdentity() media.ArtifactIdentity
	Commit() error
	Abort() error
}

type destinationOpener interface {
	Open(dir, name string, overwrite bool) (downloadDestination, error)
}

type atomicDestinationOpener struct{}

func (atomicDestinationOpener) Open(dir, name string, overwrite bool) (downloadDestination, error) {
	destination, err := media.OpenDestination(dir, name, overwrite)
	if err != nil {
		return nil, err
	}
	return &atomicDownloadDestination{destination: destination}, nil
}

type atomicDownloadDestination struct {
	destination *media.Destination
}

func (d *atomicDownloadDestination) Write(p []byte) (int, error) { return d.destination.File.Write(p) }
func (d *atomicDownloadDestination) FinalPath() string           { return d.destination.FinalPath }
func (d *atomicDownloadDestination) ArtifactIdentity() media.ArtifactIdentity {
	return d.destination.ArtifactIdentity()
}
func (d *atomicDownloadDestination) Commit() error { return d.destination.Commit() }
func (d *atomicDownloadDestination) Abort() error  { return d.destination.Abort() }

type mediaDownloadAPI interface {
	ChannelsGetMessages(context.Context, *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
	MessagesGetMessages(context.Context, []tg.InputMessageClass) (tg.MessagesMessagesClass, error)
}

type extractedDownloadMedia struct {
	MediaType string
	MIMEType  string
	Filename  string
	Size      int64
	SizeKnown bool
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
	if strings.TrimSpace(req.OutputDir) == "" {
		return DownloadMediaResp{}, safety.NewBadArgs("output_dir cannot be blank")
	}
	if err := ctx.Err(); err != nil {
		return DownloadMediaResp{}, err
	}
	absOutputDir, err := filepath.Abs(filepath.Clean(req.OutputDir))
	if err != nil {
		return DownloadMediaResp{}, fmt.Errorf("resolve output directory: %w", err)
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
	extracted, err := extractDownloadMedia(message)
	if err != nil {
		return DownloadMediaResp{}, err
	}

	safeName := media.SanitizeDownloadName(extracted.Filename)
	resp := DownloadMediaResp{
		ChatID: req.ChatID, MessageID: req.MessageID,
		MediaType: extracted.MediaType, MIMEType: extracted.MIMEType, Filename: safeName,
	}
	if message.Date > 0 {
		resp.MessageDate = time.Unix(int64(message.Date), 0).UTC()
	}
	if extracted.SizeKnown && req.MaxBytes > 0 && extracted.Size > req.MaxBytes {
		return DownloadMediaResp{}, errors.Join(
			media.ErrLimitExceeded,
			safety.NewBadArgs("media size %d exceeds max_bytes %d", extracted.Size, req.MaxBytes),
		)
	}

	downloader := g.fileDownloader
	if downloader == nil {
		downloader = gotdFileDownloader{client: g.tgc, api: g.api}
	}
	opener := g.destinationOpener
	if opener == nil {
		opener = atomicDestinationOpener{}
	}

	destination, err := opener.Open(absOutputDir, extracted.Filename, req.Overwrite)
	if err != nil {
		var collision *media.DestinationExistsError
		if !req.Overwrite && errors.As(err, &collision) {
			return collisionDownloadResponse(resp, err)
		}
		return DownloadMediaResp{}, err
	}

	limitWriter := &media.LimitWriter{W: destination, Max: req.MaxBytes}
	rawTransferErr := downloader.Download(ctx, extracted.File.Location, limitWriter)
	ctxErr := ctx.Err()
	var transferErr error
	if rawTransferErr != nil {
		transferErr = mapRPCErr(rawTransferErr)
		if ctxErr != nil {
			transferErr = errors.Join(transferErr, ctxErr)
		}
	} else {
		transferErr = ctxErr
	}
	if transferErr == nil && extracted.SizeKnown && limitWriter.N != extracted.Size {
		if limitWriter.N < extracted.Size {
			transferErr = fmt.Errorf("downloaded %d of %d authoritative bytes: %w", limitWriter.N, extracted.Size, io.ErrUnexpectedEOF)
		} else {
			transferErr = fmt.Errorf("downloaded %d bytes, authoritative size is %d", limitWriter.N, extracted.Size)
		}
	}
	if transferErr == nil {
		transferErr = ctx.Err()
	}
	if transferErr != nil {
		return DownloadMediaResp{}, abortDownload(destination, transferErr)
	}

	if err := destination.Commit(); err != nil {
		var collision *media.DestinationExistsError
		if !req.Overwrite && errors.As(err, &collision) {
			return collisionDownloadResponse(resp, err)
		}
		commitErr := abortDownload(destination, err)
		if errors.Is(commitErr, media.ErrDestinationCommitted) {
			resp.Path = destination.FinalPath()
			resp.Bytes = limitWriter.N
			resp.Skipped = false
			resp.ArtifactIdentity = destination.ArtifactIdentity()
			return resp, &CommittedMediaDownloadError{Response: resp, Err: commitErr}
		}
		return DownloadMediaResp{}, commitErr
	}
	resp.Path = destination.FinalPath()
	resp.Bytes = limitWriter.N
	resp.ArtifactIdentity = destination.ArtifactIdentity()
	return resp, nil
}

func abortDownload(destination downloadDestination, primary error) error {
	cleanupErr := destination.Abort()
	if errors.Is(cleanupErr, media.ErrDestinationCommitted) {
		cleanupErr = errors.Join(media.ErrCleanupIncomplete, cleanupErr)
	}
	return errors.Join(primary, cleanupErr)
}

func collisionDownloadResponse(resp DownloadMediaResp, collisionErr error) (DownloadMediaResp, error) {
	if errors.Is(collisionErr, media.ErrCleanupIncomplete) {
		return DownloadMediaResp{}, collisionErr
	}
	var collision *media.DestinationExistsError
	if !errors.As(collisionErr, &collision) {
		return DownloadMediaResp{}, collisionErr
	}
	resp.Path = collision.FinalPath
	resp.Bytes = collision.Size
	resp.Skipped = true
	resp.ArtifactIdentity = collision.Identity
	return resp, nil
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
		size, sizeKnown := selectedPhotoSize(photo.Sizes, file.Location)
		return extractedDownloadMedia{
			MediaType: "photo", MIMEType: file.MIMEType, Filename: file.Name,
			Size: size, SizeKnown: sizeKnown, File: file,
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
			Size: document.Size, SizeKnown: document.Size >= 0, File: file,
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

func selectedPhotoSize(sizes []tg.PhotoSizeClass, location tg.InputFileLocationClass) (int64, bool) {
	photoLocation, ok := location.(*tg.InputPhotoFileLocation)
	if !ok || photoLocation == nil {
		return 0, false
	}
	for _, size := range sizes {
		switch size := size.(type) {
		case *tg.PhotoSize:
			if size != nil && size.Type == photoLocation.ThumbSize && size.Size >= 0 {
				return int64(size.Size), true
			}
		case *tg.PhotoSizeProgressive:
			if size != nil && size.Type == photoLocation.ThumbSize && len(size.Sizes) > 0 {
				largest := -1
				for _, candidate := range size.Sizes {
					if candidate > largest {
						largest = candidate
					}
				}
				if largest >= 0 {
					return int64(largest), true
				}
			}
		case *tg.PhotoCachedSize:
			if size != nil && size.Type == photoLocation.ThumbSize {
				return int64(len(size.Bytes)), true
			}
		}
	}
	return 0, false
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
