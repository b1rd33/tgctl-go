package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

const (
	albumMinItems = 2
	albumMaxItems = 10
)

var albumMediaKinds = map[string]struct{}{
	"auto": {}, "photo": {}, "video": {}, "audio": {}, "document": {},
}

func albumFailure(stage string, position int, err error) error {
	if err == nil {
		err = errors.New("unknown failure")
	}
	return &AlbumUploadError{Stage: stage, Position: position, Err: err}
}

func (g *GotdClient) uploadAlbumAPI() (albumUploadAPI, error) {
	if g.albumAPI != nil {
		return g.albumAPI, nil
	}
	if g.api == nil {
		return nil, errors.New("Telegram client is not initialized")
	}
	return g.api, nil
}

type validatedAlbumItem struct {
	UploadAlbumItem
	sourcePath string
	path       string
	kind       string
}

func validateAlbumItems(req UploadAlbumReq) ([]validatedAlbumItem, int64, error) {
	if len(req.Items) < albumMinItems || len(req.Items) > albumMaxItems {
		return nil, 0, safety.NewBadArgs("album must contain 2 to 10 ordered items")
	}
	if req.MaxBytes < 0 {
		return nil, 0, safety.NewBadArgs("album max bytes must not be negative")
	}
	if req.MaxSizeMB < 0 {
		return nil, 0, safety.NewBadArgs("album max size must not be negative")
	}
	forcedKind := strings.ToLower(strings.TrimSpace(req.MediaKind))
	if forcedKind == "" {
		forcedKind = "auto"
	}
	if _, ok := albumMediaKinds[forcedKind]; !ok {
		return nil, 0, safety.NewBadArgs("unsupported album media kind %q", req.MediaKind)
	}
	maxBytes := req.MaxBytes
	if req.MaxSizeMB > 0 {
		mbBytes, err := media.MaxBytesFromMiB(req.MaxSizeMB)
		if err != nil {
			return nil, 0, err
		}
		if maxBytes == 0 || mbBytes < maxBytes {
			maxBytes = mbBytes
		}
	}
	items := make([]validatedAlbumItem, len(req.Items))
	for i, item := range req.Items {
		sourcePath := item.Path
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(item.MediaType))
		}
		if kind != "" && kind != "photo" && kind != "video" && kind != "audio" && kind != "document" {
			return nil, 0, safety.NewBadArgs("unsupported album media type %q at item %d", item.Kind, i)
		}
		if strings.TrimSpace(item.Path) == "" {
			return nil, 0, safety.NewBadArgs("album item %d path is required", i)
		}
		path, err := media.SafeUserPath(item.Path)
		if err != nil {
			return nil, 0, err
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, 0, safety.NewBadArgs("file does not exist: %s", path)
			}
			return nil, 0, err
		}
		if info.IsDir() {
			return nil, 0, safety.NewBadArgs("file is a directory: %s", path)
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			return nil, 0, safety.NewBadArgs("file %s exceeds album size limit", path)
		}
		detected, err := media.DetectType(path)
		if err != nil {
			return nil, 0, err
		}
		detectedKind := ""
		switch detected {
		case "photo", "image":
			detectedKind = "photo"
		case "video", "video_note":
			detectedKind = "video"
		case "audio", "voice":
			detectedKind = "audio"
		case "document":
			detectedKind = "document"
		default:
			return nil, 0, safety.NewBadArgs("unsupported album media type for %s", path)
		}
		if kind == "" {
			kind = detectedKind
		} else if kind == "audio" && detectedKind == "audio" {
			// Voice/audio containers are both represented as Telegram audio
			// documents; the requested kind remains the stable CLI value.
		} else if kind != detectedKind {
			return nil, 0, safety.NewBadArgs("unsupported %s MIME for %s", kind, path)
		}
		if forcedKind != "auto" && kind != forcedKind {
			return nil, 0, safety.NewBadArgs("album item %d is %s, but --media-kind=%s", i, kind, forcedKind)
		}
		item.Kind = kind
		item.MediaType = kind
		item.Path = path
		if item.Filename == "" {
			item.Filename = filepath.Base(path)
		}
		item.Filename = media.SanitizeDownloadName(item.Filename)
		items[i] = validatedAlbumItem{UploadAlbumItem: item, sourcePath: sourcePath, path: path, kind: kind}
	}
	containsAudio, containsDocument := false, false
	for _, item := range items {
		containsAudio = containsAudio || item.kind == "audio"
		containsDocument = containsDocument || item.kind == "document"
	}
	if containsAudio || containsDocument {
		wanted := "audio"
		if containsDocument {
			wanted = "document"
		}
		for i, item := range items {
			if item.kind != wanted {
				return nil, 0, safety.NewBadArgs("Telegram requires %s albums to contain only %s items (item %d is %s)", wanted, wanted, i, item.kind)
			}
		}
	}
	return items, maxBytes, nil
}

func (g *GotdClient) albumPeer(ctx context.Context, req UploadAlbumReq) (tg.InputPeerClass, error) {
	if req.Peer != nil {
		return req.Peer, nil
	}
	return g.peerFromChatID(ctx, req.ChatID)
}

func albumUploadedMedia(item validatedAlbumItem, file tg.InputFileClass) tg.InputMediaClass {
	if item.kind == "photo" {
		return &tg.InputMediaUploadedPhoto{File: file}
	}
	attrs := []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: item.Filename}}
	forceFile := false
	switch item.kind {
	case "video":
		attrs = append(attrs, &tg.DocumentAttributeVideo{SupportsStreaming: item.SupportsStreaming})
	case "audio":
		attrs = append(attrs, &tg.DocumentAttributeAudio{})
	case "document":
		forceFile = true
	}
	return &tg.InputMediaUploadedDocument{
		File: file, MimeType: mimeForUpload(item.kind, item.path), Attributes: attrs, ForceFile: forceFile,
	}
}

func reusableAlbumMedia(m tg.MessageMediaClass) (tg.InputMediaClass, string, error) {
	switch v := m.(type) {
	case *tg.MessageMediaPhoto:
		if v == nil || v.Photo == nil {
			return nil, "", errors.New("uploadMedia returned an empty photo")
		}
		photo, ok := v.Photo.(*tg.Photo)
		if !ok || photo == nil || photo.ID == 0 {
			return nil, "", errors.New("uploadMedia returned an unusable photo")
		}
		return &tg.InputMediaPhoto{ID: photo.AsInput()}, "photo", nil
	case *tg.MessageMediaDocument:
		if v == nil || v.Document == nil {
			return nil, "", errors.New("uploadMedia returned an empty document")
		}
		doc, ok := v.Document.(*tg.Document)
		if !ok || doc == nil || doc.ID == 0 {
			return nil, "", errors.New("uploadMedia returned an unusable document")
		}
		kind := "document"
		if v.Video || documentHasVideoAttribute(doc) {
			kind = "video"
		} else if documentHasAudioAttribute(doc) {
			kind = "audio"
		}
		return &tg.InputMediaDocument{ID: doc.AsInput()}, kind, nil
	default:
		return nil, "", fmt.Errorf("uploadMedia returned unsupported media %T", m)
	}
}

func documentHasVideoAttribute(doc *tg.Document) bool {
	if doc == nil {
		return false
	}
	for _, attr := range doc.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeVideo); ok {
			return true
		}
	}
	return false
}

func documentHasAudioAttribute(doc *tg.Document) bool {
	if doc == nil {
		return false
	}
	for _, attr := range doc.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeAudio); ok {
			return true
		}
	}
	return false
}

type albumUpdateData struct {
	mapping    map[int64]int64
	grouped    map[int64]int64
	messageIDs map[int64]struct{}
}

func collectAlbumUpdates(u tg.UpdatesClass) (albumUpdateData, error) {
	d := albumUpdateData{mapping: make(map[int64]int64), grouped: make(map[int64]int64), messageIDs: make(map[int64]struct{})}
	var updates []tg.UpdateClass
	switch v := u.(type) {
	case *tg.Updates:
		if v != nil {
			updates = v.Updates
		}
	case *tg.UpdatesCombined:
		if v != nil {
			updates = v.Updates
		}
	case *tg.UpdateShortSentMessage:
		if v != nil {
			d.messageIDs[int64(v.ID)] = struct{}{}
		}
		return d, nil
	case *tg.UpdateShort:
		if v != nil {
			updates = []tg.UpdateClass{v.Update}
		}
	default:
		return d, nil
	}
	for _, update := range updates {
		switch v := update.(type) {
		case *tg.UpdateMessageID:
			if v == nil || v.RandomID == 0 || v.ID <= 0 {
				return d, errors.New("response mapping contains an invalid message ID")
			}
			if old, ok := d.mapping[v.RandomID]; ok {
				if old != int64(v.ID) {
					return d, errors.New("response contains duplicate random ID mapping")
				}
				return d, errors.New("response contains duplicate random ID mapping")
			}
			for _, old := range d.mapping {
				if old == int64(v.ID) {
					return d, errors.New("response contains duplicate message ID mapping")
				}
			}
			d.mapping[v.RandomID] = int64(v.ID)
		case *tg.UpdateNewMessage:
			if v != nil {
				collectAlbumMessage(d, v.Message)
			}
		case *tg.UpdateNewChannelMessage:
			if v != nil {
				collectAlbumMessage(d, v.Message)
			}
		}
	}
	return d, nil
}

func collectAlbumMessage(d albumUpdateData, class tg.MessageClass) {
	msg, ok := class.(*tg.Message)
	if !ok || msg == nil || msg.ID <= 0 {
		return
	}
	id := int64(msg.ID)
	if _, exists := d.messageIDs[id]; exists {
		return
	}
	d.messageIDs[id] = struct{}{}
	if msg.GroupedID != 0 {
		d.grouped[id] = msg.GroupedID
	}
}

func extractAlbumResponse(u tg.UpdatesClass, randomIDs []int64, items []validatedAlbumItem) (UploadAlbumResp, error) {
	d, err := collectAlbumUpdates(u)
	if err != nil {
		return UploadAlbumResp{}, err
	}
	if len(randomIDs) == 1 && len(d.mapping) == 0 {
		for id := range d.messageIDs {
			d.mapping[randomIDs[0]] = id
			break
		}
	}
	if len(d.mapping) != len(randomIDs) {
		return UploadAlbumResp{}, errors.New("response is missing album message ID mapping")
	}
	resp := UploadAlbumResp{ChatID: 0, MessageIDs: make([]int64, len(randomIDs)), Items: make([]UploadAlbumItemResp, len(items))}
	var grouped int64
	consistent := true
	for i, random := range randomIDs {
		id, ok := d.mapping[random]
		if !ok || id <= 0 {
			return UploadAlbumResp{}, errors.New("response is missing album message ID mapping")
		}
		resp.MessageIDs[i] = id
		item := UploadAlbumItemResp{Position: i, MessageID: id, MediaType: items[i].kind, SourcePath: items[i].sourcePath, CaptionPlaced: items[i].Caption != ""}
		if item.CaptionPlaced {
			item.CaptionPosition = i
		}
		if gid := d.grouped[id]; gid != 0 {
			item.GroupedID = gid
			if grouped == 0 {
				grouped = gid
			} else if grouped != gid {
				consistent = false
			}
		} else {
			consistent = false
		}
		resp.Items[i] = item
	}
	if consistent {
		resp.GroupedID = grouped
	}
	return resp, nil
}

func (g *GotdClient) UploadAlbum(ctx context.Context, req UploadAlbumReq) (UploadAlbumResp, error) {
	if err := validateOptionalTelegramInt32(req.ReplyTo, "reply_to"); err != nil {
		return UploadAlbumResp{}, albumFailure("validation", -1, err)
	}
	if err := ctx.Err(); err != nil {
		return UploadAlbumResp{}, albumFailure("cancel", -1, err)
	}
	items, _, err := validateAlbumItems(req)
	if err != nil {
		return UploadAlbumResp{}, albumFailure("validation", -1, err)
	}
	if req.Caption != "" && items[0].Caption == "" {
		items[0].Caption = req.Caption
	}
	api, err := g.uploadAlbumAPI()
	if err != nil {
		return UploadAlbumResp{}, albumFailure("validation", -1, err)
	}
	peer, err := g.albumPeer(ctx, req)
	if err != nil {
		return UploadAlbumResp{}, albumFailure("validation", -1, err)
	}
	multi := make([]tg.InputSingleMedia, 0, len(items))
	randomIDs := make([]int64, 0, len(items))
	usedRandom := make(map[int64]struct{}, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return UploadAlbumResp{}, albumFailure("cancel", i, err)
		}
		file, err := uploader.NewUploader(api).FromPath(ctx, item.path)
		if err != nil {
			stage := "upload"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				stage = "cancel"
			}
			return UploadAlbumResp{}, albumFailure(stage, i, err)
		}
		uploaded := albumUploadedMedia(item, file)
		if req.SupportsStreaming {
			if doc, ok := uploaded.(*tg.InputMediaUploadedDocument); ok {
				for _, attr := range doc.Attributes {
					if video, ok := attr.(*tg.DocumentAttributeVideo); ok {
						video.SupportsStreaming = true
					}
				}
			}
		}
		mediaResp, err := api.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: peer, Media: uploaded})
		if err != nil {
			stage := "upload-media"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				stage = "cancel"
			}
			return UploadAlbumResp{}, albumFailure(stage, i, mapRPCErr(err))
		}
		if err := ctx.Err(); err != nil {
			return UploadAlbumResp{}, albumFailure("cancel", i, err)
		}
		reusable, reusableKind, err := reusableAlbumMedia(mediaResp)
		if err != nil {
			return UploadAlbumResp{}, albumFailure("conversion", i, err)
		}
		if reusableKind != item.kind {
			return UploadAlbumResp{}, albumFailure("conversion", i, fmt.Errorf("uploadMedia returned %s for requested %s", reusableKind, item.kind))
		}
		id := randomID()
		for id == 0 {
			id = randomID()
		}
		for _, exists := usedRandom[id]; exists; _, exists = usedRandom[id] {
			id = randomID()
			if id == 0 {
				continue
			}
		}
		usedRandom[id] = struct{}{}
		randomIDs = append(randomIDs, id)
		caption := item.Caption
		if i == 0 && caption == "" {
			caption = req.Caption
		}
		multi = append(multi, tg.InputSingleMedia{Media: reusable, RandomID: id, Message: caption})
	}
	if err := ctx.Err(); err != nil {
		return UploadAlbumResp{}, albumFailure("cancel", -1, err)
	}
	updates, err := api.MessagesSendMultiMedia(ctx, &tg.MessagesSendMultiMediaRequest{Peer: peer, ReplyTo: replyTo(req.ReplyTo), Silent: req.Silent, MultiMedia: multi})
	if err != nil {
		stage := "final-send"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			stage = "final-send-cancel"
		}
		return UploadAlbumResp{}, albumFailure(stage, -1, mapRPCErr(err))
	}
	if err := ctx.Err(); err != nil {
		return UploadAlbumResp{}, albumFailure("final-send-cancel", -1, err)
	}
	resp, err := extractAlbumResponse(updates, randomIDs, items)
	if err != nil {
		return UploadAlbumResp{}, albumFailure("final-send", -1, err)
	}
	resp.ChatID = req.ChatID
	return resp, nil
}

func replyTo(id int64) tg.InputReplyToClass {
	if id == 0 {
		return nil
	}
	return &tg.InputReplyToMessage{ReplyToMsgID: int(id)}
}
