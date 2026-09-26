package reader

// Explicit manual browser fixture. It cannot connect to Telegram or open any
// account state. All advertisements and callbacks are synthetic.
import (
	"context"
	"encoding/json"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/client"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type browserFixture struct {
	dir     string
	actions []Action
}

func (b *browserFixture) Page(_ context.Context, offset int64, _ bool) (SourcePage, error) {
	var options struct {
		Empty, Fail bool
		Kind        string
		Ad          *client.SponsoredAd
	}
	data, _ := os.ReadFile(filepath.Join(b.dir, "options.json"))
	_ = json.Unmarshal(data, &options)
	if options.Fail {
		return SourcePage{}, errors.New("synthetic failure")
	}
	p := Page{Title: "Studio updates", Kind: "channel", Limit: 50, NextOffset: 1, Messages: []Message{{ID: 1, Text: "The autumn collection is ready for review.", Date: "2026-09-26T10:32:00+03:00"}, {ID: 2, Text: "Please check the updated delivery schedule before Friday.", Date: "2026-09-26T11:05:00+03:00"}, {ID: 3, Text: "New material samples will arrive next week.", Date: "2026-09-26T12:18:00+03:00"}}}
	if offset > 0 {
		p.Messages = []Message{{ID: 0, Text: "Earlier update.", Date: "2026-09-25T09:00:00+03:00"}}
		p.NextOffset = 0
	}
	if options.Empty {
		p.Messages = []Message{}
		p.NextOffset = 0
		return SourcePage{Page: p}, nil
	}
	if options.Kind != "" {
		p.Kind = options.Kind
	}
	ad := syntheticAd()
	if options.Ad != nil {
		ad = *options.Ad
		ad.RandomID = []byte("synthetic-custom")
	}
	return SourcePage{Page: p, Batch: &client.SponsoredBatch{Ads: []client.SponsoredAd{ad}}}, nil
}
func (b *browserFixture) File(context.Context, client.ReaderFile) ([]byte, error) {
	return os.ReadFile(filepath.Join(b.dir, "media"))
}
func (b *browserFixture) Action(_ context.Context, a Action) (client.SponsoredReport, error) {
	b.actions = append(b.actions, a)
	data, _ := json.Marshal(b.actions)
	if err := os.WriteFile(filepath.Join(b.dir, "actions.json"), data, 0600); err != nil {
		return client.SponsoredReport{}, err
	}
	if a.Kind == "report" {
		if len(a.Option) == 0 {
			return client.SponsoredReport{State: "choose", Title: "Why this ad?", Options: []client.SponsoredReportOption{{Text: "Not relevant", Value: []byte("synthetic-choice")}}}, nil
		}
		return client.SponsoredReport{State: "reported"}, nil
	}
	return client.SponsoredReport{}, nil
}
func TestBrowserFixture(t *testing.T) {
	dir := os.Getenv("TGCTL_READER_BROWSER_DIR")
	if dir == "" {
		t.Skip("explicit synthetic browser fixture only")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("fixture directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s, err := New(&browserFixture{dir: dir}, listener.Addr().String(), time.Now().Add(30*time.Minute), cancel)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "url"), []byte(s.URL()), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Serve(ctx, listener); err != nil {
		t.Fatal(err)
	}
}
