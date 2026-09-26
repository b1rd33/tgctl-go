package reader

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	textutil "github.com/b1rd33/tgctl-go/internal/text"
)

type fakeBackend struct {
	pages, fresh, files int
	actions             []Action
	batch               *client.SponsoredBatch
	err                 error
	actionErr           error
	report              client.SponsoredReport
}

func syntheticAd() client.SponsoredAd {
	return client.SponsoredAd{RandomID: []byte("private-id"), Title: "Paper & Form", Text: "Thoughtful supplies for your next project. Explore the latest collection.", Button: "Explore collection", URL: "https://example.com", CanReport: true, SponsorInfo: "Synthetic sponsor", AdditionalInfo: "Test information"}
}
func (b *fakeBackend) Page(_ context.Context, offset int64, fresh bool) (SourcePage, error) {
	b.pages++
	if fresh {
		b.fresh++
	}
	return SourcePage{Page: Page{Title: "Studio updates", Kind: "channel", NextOffset: offset + 1, Limit: 50, Messages: []Message{{ID: 3, Text: "New material samples will arrive next week.", Date: "2026-09-26T12:18:00Z"}}}, Batch: b.batch}, b.err
}
func (b *fakeBackend) File(context.Context, client.ReaderFile) ([]byte, error) {
	b.files++
	return []byte("synthetic media"), b.err
}
func (b *fakeBackend) Action(_ context.Context, a Action) (client.SponsoredReport, error) {
	b.actions = append(b.actions, a)
	return b.report, b.actionErr
}
func setupReader(t *testing.T) (*Server, *fakeBackend) {
	t.Helper()
	b := &fakeBackend{batch: &client.SponsoredBatch{Ads: []client.SponsoredAd{syntheticAd()}}}
	s, e := New(b, "127.0.0.1:12345", time.Now().Add(time.Hour), func() {})
	if e != nil {
		t.Fatal(e)
	}
	return s, b
}
func request(s *Server, path, body string, modify ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://"+s.host+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:2222"
	r.Header.Set("Authorization", "Bearer "+s.token)
	r.Header.Set("Origin", "http://"+s.host)
	r.Header.Set("Content-Type", "application/json")
	for _, m := range modify {
		m(r)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func adPage(t *testing.T, s *Server) Page {
	t.Helper()
	w := request(s, "/api/page", "{}")
	if w.Code != 200 {
		t.Fatalf("page: %d %s", w.Code, w.Body)
	}
	var p Page
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(w.Body.String(), "private-id") {
		t.Fatal("private identifier leaked")
	}
	return p
}
func actionRequest(s *Server, kind, event, option string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"ad": s.ad.ad.ID, "action": kind, "event": event, "option": option})
	return request(s, "/api/event", string(body))
}

func TestReaderHTTPBoundary(t *testing.T) {
	for name, change := range map[string]func(*http.Request){"host": func(r *http.Request) { r.Host = "evil.test" }, "remote": func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1" }, "origin": func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, "missing origin": func(r *http.Request) { r.Header.Del("Origin") }, "token": func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, "cross-site": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, "query": func(r *http.Request) { r.URL.RawQuery = "token=x" }, "content type": func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, "method": func(r *http.Request) { r.Method = "GET" }} {
		t.Run(name, func(t *testing.T) {
			s, b := setupReader(t)
			w := request(s, "/api/page", "{}", change)
			if w.Code != 403 || b.pages != 0 {
				t.Fatalf("boundary: %d calls %d", w.Code, b.pages)
			}
		})
	}
	for _, body := range []string{`{"unknown":1}`, `{} {}`, strings.Repeat(" ", 4096) + `{}`, `{"offset":1}`} {
		s, b := setupReader(t)
		w := request(s, "/api/page", body)
		if w.Code != 400 || b.pages != 0 {
			t.Fatalf("invalid body admitted: %d", w.Code)
		}
	}
	s, _ := setupReader(t)
	w := request(s, "/", "", func(r *http.Request) { r.Method = "GET"; r.Header.Del("Authorization"); r.Header.Del("Origin") })
	if w.Code != 200 || !strings.Contains(w.Body.String(), "tgctl reader") || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("static safety headers")
	}
	if w = request(s, "/../../session", "{}"); w.Code != 404 {
		t.Fatal("unexpected static path")
	}
}

func TestReaderCacheExpiryPaginationAndNoImplicitEngagement(t *testing.T) {
	s, b := setupReader(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	first := adPage(t, s)
	for i := 0; i < 3; i++ {
		if adPage(t, s).Ad.ID != first.Ad.ID {
			t.Fatal("ad cache replaced early")
		}
	}
	if b.fresh != 1 || len(b.actions) != 0 {
		t.Fatal("fetch produced engagement or bypassed cache")
	}
	if w := request(s, "/api/page", `{"offset":1}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "/api/page", `{"offset":999}`); w.Code != 400 {
		t.Fatal("unissued offset accepted")
	}
	for i := 2; i <= 20; i++ {
		body, _ := json.Marshal(map[string]int{"offset": i})
		if request(s, "/api/page", string(body)).Code != 200 {
			t.Fatal("valid bounded continuation")
		}
	}
	if request(s, "/api/page", `{"offset":21}`).Code != 400 {
		t.Fatal("unbounded continuation")
	}
	now = now.Add(5 * time.Minute)
	if actionRequest(s, "view", "event-00000000001", "").Code != 410 {
		t.Fatal("expired ad admitted")
	}
	next := adPage(t, s)
	if next.Ad.ID == first.Ad.ID || b.fresh != 2 {
		t.Fatal("expired cache not replaced")
	}
	now = s.expires
	if request(s, "/api/page", "{}").Code != 410 {
		t.Fatal("expired reader admitted")
	}
}

func TestReaderUnknownAndConcurrentEventsAreNotRetried(t *testing.T) {
	s, b := setupReader(t)
	adPage(t, s)
	b.actionErr = errors.New("private failure")
	const bodyEvent = "event-00000000001"
	w := actionRequest(s, "view", bodyEvent, "")
	if w.Code != 502 || strings.Contains(w.Body.String(), "private failure") {
		t.Fatal("unknown failure leaked or succeeded")
	}
	b.actionErr = nil
	if actionRequest(s, "view", "event-00000000002", "").Code != 409 {
		t.Fatal("view attempted twice")
	}
	if actionRequest(s, "click", bodyEvent, "").Code != 409 {
		t.Fatal("nonce reused")
	}
	body, _ := json.Marshal(map[string]string{"ad": s.ad.ad.ID, "action": "click", "event": "event-00000000003"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); request(s, "/api/event", string(body)) }()
	}
	wg.Wait()
	if len(b.actions) != 2 {
		t.Fatalf("concurrent duplicates reached backend: %d", len(b.actions))
	}
}

func TestReaderAssetsAndReportOptions(t *testing.T) {
	s, b := setupReader(t)
	b.batch.Ads[0].Avatar = &client.ReaderFile{Kind: "photo", MIME: "image/png"}
	adPage(t, s)
	if actionRequest(s, "view", "event-00000000001", "").Code != 409 {
		t.Fatal("view before media")
	}
	body, _ := json.Marshal(map[string]string{"ad": s.ad.ad.ID, "part": "avatar"})
	for i := 0; i < 2; i++ {
		if w := request(s, "/api/asset", string(body)); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
			t.Fatal(w.Code)
		}
	}
	if b.files != 1 {
		t.Fatal("asset not cached")
	}
	if actionRequest(s, "report", "event-00000000002", "forged").Code != 400 {
		t.Fatal("forged option accepted")
	}
	b.report = client.SponsoredReport{State: "choose", Title: "Reason", Options: []client.SponsoredReportOption{{Text: "Not relevant", Value: []byte("private-option")}}}
	w := actionRequest(s, "report", "event-00000000003", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-option") {
		t.Fatal("report options exposed")
	}
	b.report = client.SponsoredReport{State: "reported"}
	w = actionRequest(s, "report", "event-00000000004", "0")
	if w.Code != 200 || s.ad != nil || string(b.actions[1].Option) != "private-option" {
		t.Fatal("report continuation lost")
	}
}

func TestReaderUnsupportedRequiredContentFailsClosed(t *testing.T) {
	for _, mutate := range []func(*client.SponsoredAd){func(a *client.SponsoredAd) { a.URL = "javascript:alert(1)" }, func(a *client.SponsoredAd) {
		a.Text = "😀"
		a.Entities = []textutil.Entity{{Type: "bold", Offset: 1, Length: 1}}
	}, func(a *client.SponsoredAd) { a.Entities = []textutil.Entity{{Type: "customemoji", Length: 1}} }, func(a *client.SponsoredAd) { a.Button = "" }} {
		s, b := setupReader(t)
		mutate(&b.batch.Ads[0])
		w := request(s, "/api/page", "{}")
		if w.Code != 502 || strings.Contains(w.Body.String(), "Studio updates") || len(b.actions) != 0 {
			t.Fatal("unsupported ad silently omitted")
		}
	}
	a := syntheticAd()
	a.Button = ""
	if validateAd(a, "bot") != nil {
		t.Fatal("bot advertisement requires nonexistent CTA")
	}
}

func TestReaderCancellationAndClose(t *testing.T) {
	s, _ := setupReader(t)
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := request(s, "/api/page", "{}", func(r *http.Request) { *r = *r.WithContext(ctx) })
	<-s.gate
	if w.Code != 408 {
		t.Fatal("canceled queue did not stop")
	}
	stopped := false
	s.stop = func() { stopped = true }
	if request(s, "/api/close", "{}").Code != 200 || !stopped {
		t.Fatal("close did not stop")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	s, _ = New(&fakeBackend{}, listener.Addr().String(), time.Now().Add(time.Hour), cancel)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not close")
	}
}
