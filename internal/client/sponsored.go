package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/safety"
	textutil "github.com/b1rd33/tgctl-go/internal/text"
	"github.com/gotd/td/tg"
)

// ReaderAPI is deliberately separate from the general command client. Only the
// human reader uses sponsored callbacks; fetching data never sends engagement.
type ReaderAPI interface {
	Client
	GetSponsoredMessages(context.Context, int64) (SponsoredBatch, error)
	DownloadReaderFile(context.Context, ReaderFile) ([]byte, error)
	ViewSponsoredMessage(context.Context, []byte) error
	ClickSponsoredMessage(context.Context, []byte, bool, bool) error
	ReportSponsoredMessage(context.Context, []byte, []byte) (SponsoredReport, error)
}

type ReaderFile struct {
	Kind      string                    `json:"kind"`
	MIME      string                    `json:"mime"`
	HasSound  bool                      `json:"has_sound"`
	Location  tg.InputFileLocationClass `json:"-"`
	Size      int64                     `json:"-"`
	SizeKnown bool                      `json:"-"`
}

type SponsoredAd struct {
	RandomID       []byte            `json:"-"`
	Title          string            `json:"title"`
	Text           string            `json:"text"`
	URL            string            `json:"url"`
	Button         string            `json:"button"`
	Recommended    bool              `json:"recommended"`
	CanReport      bool              `json:"can_report"`
	SponsorInfo    string            `json:"sponsor_info"`
	AdditionalInfo string            `json:"additional_info"`
	Entities       []textutil.Entity `json:"entities"`
	Colors         []string          `json:"colors"`
	Avatar         *ReaderFile       `json:"avatar,omitempty"`
	Media          *ReaderFile       `json:"media,omitempty"`
}

type SponsoredBatch struct {
	Ads          []SponsoredAd
	PostsBetween int
}
type SponsoredReportOption struct {
	Text  string `json:"text"`
	Value []byte `json:"-"`
}
type SponsoredReport struct {
	State   string                  `json:"state"`
	Title   string                  `json:"title,omitempty"`
	Options []SponsoredReportOption `json:"options,omitempty"`
}

const ReaderMediaLimit int64 = 20 << 20

func (g *GotdClient) GetSponsoredMessages(ctx context.Context, chatID int64) (SponsoredBatch, error) {
	peer, err := g.peerFromChatID(ctx, chatID)
	if err != nil {
		return SponsoredBatch{}, err
	}
	response, err := g.api.MessagesGetSponsoredMessages(ctx, &tg.MessagesGetSponsoredMessagesRequest{Peer: peer})
	if err != nil {
		return SponsoredBatch{}, mapRPCErr(err)
	}
	switch r := response.(type) {
	case *tg.MessagesSponsoredMessagesEmpty:
		return SponsoredBatch{}, nil
	case *tg.MessagesSponsoredMessages:
		if r == nil || len(r.Messages) > 100 {
			return SponsoredBatch{}, safety.NewBadArgs("invalid sponsored response")
		}
		out := SponsoredBatch{PostsBetween: r.PostsBetween}
		// Without an inter-post insertion surface only the first ad is displayed,
		// after the latest message. This also respects minimum posts_between.
		if len(r.Messages) == 0 {
			return out, nil
		}
		ad, err := g.readerAd(ctx, r.Messages[0])
		if err != nil {
			return SponsoredBatch{}, err
		}
		out.Ads = []SponsoredAd{ad}
		return out, nil
	default:
		return SponsoredBatch{}, safety.NewBadArgs("unsupported sponsored response")
	}
}

func (g *GotdClient) readerAd(ctx context.Context, ad tg.SponsoredMessage) (SponsoredAd, error) {
	if len(ad.RandomID) == 0 || len(ad.RandomID) > 1024 {
		return SponsoredAd{}, safety.NewBadArgs("invalid sponsored identifier")
	}
	if _, ok := ad.GetMinDisplayDuration(); ok {
		return SponsoredAd{}, safety.NewBadArgs("video-overlay advertisements are not a chat reader surface")
	}
	for _, entity := range ad.Entities {
		if isTypedNil(entity) {
			return SponsoredAd{}, safety.NewBadArgs("invalid sponsored text entity")
		}
	}
	out := SponsoredAd{RandomID: append([]byte(nil), ad.RandomID...), Title: ad.Title, Text: ad.Message, URL: ad.URL, Button: ad.ButtonText, Recommended: ad.Recommended, CanReport: ad.CanReport, SponsorInfo: ad.SponsorInfo, AdditionalInfo: ad.AdditionalInfo, Entities: textutil.FromTelegram(ad.Entities)}
	var err error
	if photo, ok := ad.GetPhoto(); ok {
		out.Avatar, err = readerFile(&tg.MessageMediaPhoto{Photo: photo})
		if err != nil {
			return SponsoredAd{}, err
		}
	}
	if m, ok := ad.GetMedia(); ok {
		out.Media, err = readerFile(m)
		if err != nil {
			return SponsoredAd{}, err
		}
	}
	if color, ok := ad.GetColor(); ok {
		out.Colors, err = g.readerColors(ctx, color)
		if err != nil {
			return SponsoredAd{}, err
		}
	}
	return out, nil
}

func readerFile(m tg.MessageMediaClass) (*ReaderFile, error) {
	extracted, err := extractDownloadMedia(&tg.Message{Media: m})
	if err != nil {
		return nil, err
	}
	if extracted.MediaType != "photo" && extracted.MediaType != "video" && extracted.MediaType != "animation" {
		return nil, safety.NewBadArgs("unsupported sponsored media; required media will not be hidden")
	}
	if extracted.SizeKnown && (extracted.Size < 0 || extracted.Size > ReaderMediaLimit) {
		return nil, safety.NewBadArgs("sponsored media exceeds reader limit")
	}
	if extracted.MIMEType != "image/jpeg" && extracted.MIMEType != "image/png" && extracted.MIMEType != "video/mp4" && extracted.MIMEType != "video/webm" {
		return nil, safety.NewBadArgs("unsupported sponsored media format")
	}
	f := &ReaderFile{Kind: extracted.MediaType, MIME: extracted.MIMEType, Location: extracted.File.Location, Size: extracted.Size, SizeKnown: extracted.SizeKnown}
	if document, ok := m.(*tg.MessageMediaDocument); ok {
		if d, ok := document.Document.(*tg.Document); ok {
			for _, a := range d.Attributes {
				if v, ok := a.(*tg.DocumentAttributeVideo); ok {
					f.HasSound = !v.Nosound
				}
			}
		}
	}
	return f, nil
}

func (g *GotdClient) DownloadReaderFile(ctx context.Context, file ReaderFile) ([]byte, error) {
	if isTypedNil(file.Location) || file.SizeKnown && (file.Size < 0 || file.Size > ReaderMediaLimit) {
		return nil, safety.NewBadArgs("invalid reader media")
	}
	d := g.fileDownloader
	if d == nil {
		d = gotdFileDownloader{client: g.tgc, api: g.api}
	}
	var b bytes.Buffer
	w := &media.LimitWriter{W: &b, Max: ReaderMediaLimit}
	if err := d.Download(ctx, file.Location, w); err != nil {
		return nil, mapRPCErr(err)
	}
	if b.Len() == 0 || file.SizeKnown && int64(b.Len()) != file.Size {
		return nil, errors.New("reader media transfer was incomplete")
	}
	return b.Bytes(), nil
}

func (g *GotdClient) readerColors(ctx context.Context, raw tg.PeerColorClass) ([]string, error) {
	p, ok := raw.(*tg.PeerColor)
	if !ok || p == nil || p.BackgroundEmojiID != 0 {
		return nil, safety.NewBadArgs("unsupported sponsored color pattern")
	}
	id, present := p.GetColor()
	if !present {
		return nil, nil
	}
	base := []string{"#cc5049", "#d67722", "#955cdb", "#40a920", "#309eba", "#368ad1", "#c7508b"}
	if id >= 0 && id < len(base) {
		return []string{base[id]}, nil
	}
	result, err := g.api.HelpGetPeerColors(ctx, 0)
	if err != nil {
		return nil, mapRPCErr(err)
	}
	if colors, ok := result.(*tg.HelpPeerColors); ok && colors != nil {
		for _, option := range colors.Colors {
			if option.ColorID != id {
				continue
			}
			if palette, ok := option.Colors.(*tg.HelpPeerColorSet); ok && palette != nil && len(palette.Colors) > 0 && len(palette.Colors) <= 3 {
				out := make([]string, 0, len(palette.Colors))
				for _, c := range palette.Colors {
					if c < 0 || c > 0xffffff {
						return nil, safety.NewBadArgs("invalid sponsored color")
					}
					out = append(out, fmt.Sprintf("#%06x", c))
				}
				return out, nil
			}
		}
	}
	return nil, safety.NewBadArgs("sponsored palette is unavailable")
}

func validSponsoredID(id []byte) error {
	if len(id) == 0 || len(id) > 1024 {
		return safety.NewBadArgs("invalid sponsored identifier")
	}
	return nil
}
func (g *GotdClient) ViewSponsoredMessage(ctx context.Context, id []byte) error {
	if err := validSponsoredID(id); err != nil {
		return err
	}
	ok, err := g.api.MessagesViewSponsoredMessage(ctx, id)
	if err != nil {
		return mapRPCErr(err)
	}
	if !ok {
		return errors.New("Telegram did not confirm the sponsored view")
	}
	return nil
}
func (g *GotdClient) ClickSponsoredMessage(ctx context.Context, id []byte, media, fullscreen bool) error {
	if err := validSponsoredID(id); err != nil {
		return err
	}
	ok, err := g.api.MessagesClickSponsoredMessage(ctx, &tg.MessagesClickSponsoredMessageRequest{RandomID: id, Media: media, Fullscreen: fullscreen})
	if err != nil {
		return mapRPCErr(err)
	}
	if !ok {
		return errors.New("Telegram did not confirm the sponsored click")
	}
	return nil
}
func (g *GotdClient) ReportSponsoredMessage(ctx context.Context, id, option []byte) (SponsoredReport, error) {
	if err := validSponsoredID(id); err != nil {
		return SponsoredReport{}, err
	}
	if len(option) > 4096 {
		return SponsoredReport{}, safety.NewBadArgs("invalid sponsored report option")
	}
	r, err := g.api.MessagesReportSponsoredMessage(ctx, &tg.MessagesReportSponsoredMessageRequest{RandomID: id, Option: option})
	if err != nil {
		return SponsoredReport{}, mapRPCErr(err)
	}
	switch v := r.(type) {
	case *tg.ChannelsSponsoredMessageReportResultChooseOption:
		if v == nil {
			return SponsoredReport{}, safety.NewBadArgs("invalid report options")
		}
		out := SponsoredReport{State: "choose", Title: v.Title}
		if len(v.Options) == 0 || len(v.Options) > 30 {
			return SponsoredReport{}, safety.NewBadArgs("too many sponsored report options")
		}
		for _, o := range v.Options {
			if len(o.Option) == 0 || len(o.Option) > 4096 || o.Text == "" {
				return SponsoredReport{}, safety.NewBadArgs("invalid sponsored report option")
			}
			out.Options = append(out.Options, SponsoredReportOption{Text: o.Text, Value: append([]byte(nil), o.Option...)})
		}
		return out, nil
	case *tg.ChannelsSponsoredMessageReportResultReported:
		return SponsoredReport{State: "reported"}, nil
	case *tg.ChannelsSponsoredMessageReportResultAdsHidden:
		return SponsoredReport{State: "hidden"}, nil
	default:
		return SponsoredReport{}, safety.NewBadArgs("unsupported sponsored report response")
	}
}
