package client

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
)

func TestSponsoredFetchHasNoEngagementAndPreservesFields(t *testing.T) {
	calls := 0
	g := &GotdClient{resolvedPeers: map[int64]tg.InputPeerClass{7: &tg.InputPeerUser{UserID: 7, AccessHash: 70}}, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		req, ok := in.(*tg.MessagesGetSponsoredMessagesRequest)
		if !ok {
			t.Fatalf("unexpected engagement: %T", in)
		}
		if req.MsgID != 0 {
			t.Fatal("chat request used video overlay")
		}
		ad := tg.SponsoredMessage{RandomID: []byte("opaque"), Title: "Sponsor", Message: "text", URL: "https://example.com", ButtonText: "Explore", Recommended: true, CanReport: true, SponsorInfo: "Sponsor", AdditionalInfo: "Additional", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Length: 4}}}
		out.(*tg.MessagesSponsoredMessagesBox).SponsoredMessages = &tg.MessagesSponsoredMessages{PostsBetween: 10, Messages: []tg.SponsoredMessage{ad, ad}}
		return nil
	}))}
	batch, err := g.GetSponsoredMessages(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(batch.Ads) != 1 || batch.PostsBetween != 10 {
		t.Fatal("fetch semantics")
	}
	a := batch.Ads[0]
	if string(a.RandomID) != "opaque" || !a.Recommended || !a.CanReport || a.AdditionalInfo != "Additional" || len(a.Entities) != 1 {
		t.Fatal("required metadata lost")
	}
}
func TestSponsoredEmptyAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		g := &GotdClient{resolvedPeers: map[int64]tg.InputPeerClass{7: &tg.InputPeerUser{UserID: 7}}, api: tg.NewClient(invokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			if fail {
				return context.Canceled
			}
			out.(*tg.MessagesSponsoredMessagesBox).SponsoredMessages = &tg.MessagesSponsoredMessagesEmpty{}
			return nil
		}))}
		batch, err := g.GetSponsoredMessages(context.Background(), 7)
		if (err != nil) != fail || len(batch.Ads) != 0 {
			t.Fatal("empty/error response")
		}
	}
}
func TestSponsoredActionsPreserveFlagsAndRejectFalse(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		calls := 0
		g := &GotdClient{api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			calls++
			switch r := in.(type) {
			case *tg.MessagesViewSponsoredMessageRequest:
				if string(r.RandomID) != "ad" {
					t.Fatal("view identifier")
				}
			case *tg.MessagesClickSponsoredMessageRequest:
				if string(r.RandomID) != "ad" || !r.Media || !r.Fullscreen {
					t.Fatal("click flags")
				}
			default:
				t.Fatalf("request %T", in)
			}
			if confirmed {
				out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
			} else {
				out.(*tg.BoolBox).Bool = &tg.BoolFalse{}
			}
			return nil
		}))}
		if err := g.ViewSponsoredMessage(context.Background(), []byte("ad")); (err == nil) != confirmed {
			t.Fatal("view confirmation")
		}
		if err := g.ClickSponsoredMessage(context.Background(), []byte("ad"), true, true); (err == nil) != confirmed {
			t.Fatal("click confirmation")
		}
		if g.ViewSponsoredMessage(context.Background(), nil) == nil || calls != 2 {
			t.Fatal("invalid identifier dispatched")
		}
	}
}
func TestSponsoredReportNegotiationAndFailures(t *testing.T) {
	results := []tg.ChannelsSponsoredMessageReportResultClass{&tg.ChannelsSponsoredMessageReportResultChooseOption{Title: "Reason", Options: []tg.SponsoredMessageReportOption{{Text: "Other", Option: []byte("opaque")}}}, &tg.ChannelsSponsoredMessageReportResultReported{}, &tg.ChannelsSponsoredMessageReportResultAdsHidden{}, &tg.ChannelsSponsoredMessageReportResultChooseOption{}}
	for i, result := range results {
		g := &GotdClient{api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			r := in.(*tg.MessagesReportSponsoredMessageRequest)
			if string(r.RandomID) != "ad" || string(r.Option) != "choice" {
				t.Fatal("report identity")
			}
			out.(*tg.ChannelsSponsoredMessageReportResultBox).SponsoredMessageReportResult = result
			return nil
		}))}
		got, err := g.ReportSponsoredMessage(context.Background(), []byte("ad"), []byte("choice"))
		if i == 3 {
			if err == nil {
				t.Fatal("empty options accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got.State != []string{"choose", "reported", "hidden"}[i] {
			t.Fatal("report state")
		}
		if i == 0 && string(got.Options[0].Value) != "opaque" {
			t.Fatal("option lost")
		}
	}
}
func TestSponsoredMediaLimitsAndIncompleteTransfers(t *testing.T) {
	file := ReaderFile{Location: &tg.InputDocumentFileLocation{}, SizeKnown: true, Size: 4}
	for _, tt := range []struct {
		data string
		err  error
		ok   bool
	}{{"data", nil, true}, {"dat", nil, false}, {"", nil, false}, {"data", errors.New("interrupted"), false}} {
		g := &GotdClient{fileDownloader: &recordingFileDownloader{chunks: [][]byte{[]byte(tt.data)}, err: tt.err}}
		_, err := g.DownloadReaderFile(context.Background(), file)
		if (err == nil) != tt.ok {
			t.Fatal("transfer completion")
		}
	}
	g := &GotdClient{}
	file.Size = ReaderMediaLimit + 1
	if _, err := g.DownloadReaderFile(context.Background(), file); err == nil {
		t.Fatal("oversized accepted")
	}
	if _, err := readerFile(&tg.MessageMediaUnsupported{}); err == nil {
		t.Fatal("unsupported media accepted")
	}
}
func TestSponsoredRequiredContentAndColors(t *testing.T) {
	g := &GotdClient{}
	for _, a := range []tg.SponsoredMessage{{}, {RandomID: []byte("ad"), Entities: []tg.MessageEntityClass{(*tg.MessageEntityBold)(nil)}}} {
		if _, err := g.readerAd(context.Background(), a); err == nil {
			t.Fatal("invalid ad accepted")
		}
	}
	overlay := tg.SponsoredMessage{RandomID: []byte("ad")}
	overlay.SetMinDisplayDuration(5)
	if _, err := g.readerAd(context.Background(), overlay); err == nil {
		t.Fatal("video overlay accepted")
	}
	pattern := &tg.PeerColor{BackgroundEmojiID: 12}
	if _, err := g.readerColors(context.Background(), pattern); err == nil {
		t.Fatal("required pattern omitted")
	}
	base := &tg.PeerColor{}
	base.SetColor(5)
	colors, err := g.readerColors(context.Background(), base)
	if err != nil || len(colors) != 1 || colors[0] != "#368ad1" {
		t.Fatal("base palette")
	}
}
