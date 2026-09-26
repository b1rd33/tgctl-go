// Package reader serves a short-lived, account-bound human reader on loopback.
package reader

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
)

//go:embed web/*
var assets embed.FS

type Page struct {
	Title      string    `json:"title"`
	Kind       string    `json:"kind"`
	Messages   []Message `json:"messages"`
	NextOffset int64     `json:"next_offset"`
	Ad         *Ad       `json:"ad,omitempty"`
	Limit      int       `json:"limit"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type Message struct {
	ID      int64  `json:"id"`
	Author  string `json:"author,omitempty"`
	Text    string `json:"text"`
	Date    string `json:"date"`
	Media   string `json:"media,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}
type Ad struct {
	client.SponsoredAd
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}
type SourcePage struct {
	Page
	Batch *client.SponsoredBatch
}
type Action struct {
	Kind              string
	RandomID          []byte
	Option            []byte
	Media, Fullscreen bool
	Key               string
}
type Backend interface {
	Page(context.Context, int64, bool) (SourcePage, error)
	File(context.Context, client.ReaderFile) ([]byte, error)
	Action(context.Context, Action) (client.SponsoredReport, error)
}

type adState struct {
	ad            Ad
	files         map[string][]byte
	viewAttempted bool
	options       map[string][]byte
}
type Server struct {
	backend     Backend
	host, token string
	gate        chan struct{}
	ad          *adState
	cacheUntil  time.Time
	offsets     map[int64]bool
	events      map[string]string
	now         func() time.Time
	stop        context.CancelFunc
	expires     time.Time
}

func New(backend Backend, host string, expires time.Time, stop context.CancelFunc) (*Server, error) {
	ip, port, err := net.SplitHostPort(host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || ip != "127.0.0.1" || portErr != nil || portNumber < 1 || portNumber > 65535 || backend == nil || stop == nil {
		return nil, errors.New("reader must bind IPv4 loopback")
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	return &Server{backend: backend, gate: make(chan struct{}, 1), host: host, token: token, offsets: map[int64]bool{0: true}, events: map[string]string{}, now: time.Now, stop: stop, expires: expires}, nil
}
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (s *Server) URL() string { return "http://" + s.host + "/#" + s.token }

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	h := &http.Server{Handler: s, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.Shutdown(closeCtx)
			_ = h.Close()
		case <-done:
		}
	}()
	err := h.Serve(listener)
	close(done)
	s.gate <- struct{}{}
	s.ad = nil
	s.events = nil
	<-s.gate
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' blob:; media-src 'self' blob:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if r.Host != s.host || !net.ParseIP(ip).IsLoopback() || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+s.host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !s.now().Before(s.expires) {
		http.Error(w, "Reader session expired", http.StatusGone)
		return
	}
	static := map[string]string{"/": "web/index.html", "/app.js": "web/app.js", "/logic.js": "web/logic.js", "/style.css": "web/style.css"}
	if name, ok := static[r.URL.Path]; ok {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", 405)
			return
		}
		b, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		types := map[string]string{"/": "text/html; charset=utf-8", "/app.js": "text/javascript; charset=utf-8", "/logic.js": "text/javascript; charset=utf-8", "/style.css": "text/css; charset=utf-8"}
		w.Header().Set("Content-Type", types[r.URL.Path])
		_, _ = w.Write(b)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
		http.Error(w, "Forbidden", 403)
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Origin") != "http://"+s.host || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "Forbidden", 403)
		return
	}
	var in struct {
		Offset     int64  `json:"offset"`
		Ad         string `json:"ad"`
		Part       string `json:"part"`
		Event      string `json:"event"`
		Action     string `json:"action"`
		Option     string `json:"option"`
		Media      bool   `json:"media"`
		Fullscreen bool   `json:"fullscreen"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		http.Error(w, "Invalid request", 400)
		return
	}
	if r.URL.Path == "/api/close" {
		s.stop()
		writeJSON(w, map[string]bool{"closed": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		http.Error(w, "Reader request canceled", 408)
		return
	}
	defer func() { <-s.gate }()
	if ctx.Err() != nil {
		http.Error(w, "Reader request canceled", 408)
		return
	}
	switch r.URL.Path {
	case "/api/page":
		if !s.offsets[in.Offset] {
			http.Error(w, "Invalid continuation", 400)
			return
		}
		fresh := !s.now().Before(s.cacheUntil)
		page, err := s.backend.Page(ctx, in.Offset, fresh)
		if err != nil {
			fail(w, err)
			return
		}
		if fresh {
			s.ad = nil
			if page.Batch != nil && len(page.Batch.Ads) > 0 {
				ad := page.Batch.Ads[0]
				if err := validateAd(ad, page.Kind); err != nil {
					fail(w, err)
					return
				}
				id, err := newToken()
				if err != nil {
					fail(w, err)
					return
				}
				s.ad = &adState{ad: Ad{SponsoredAd: ad, ID: id, ExpiresAt: s.now().Add(5 * time.Minute)}, files: map[string][]byte{}, options: map[string][]byte{}}
			}
			s.cacheUntil = s.now().Add(5 * time.Minute)
		}
		if page.NextOffset > 0 && len(s.offsets) < 21 {
			s.offsets[page.NextOffset] = true
		} else {
			page.NextOffset = 0
		}
		if s.ad != nil {
			page.Ad = &s.ad.ad
		}
		page.ExpiresAt = s.expires
		writeJSON(w, page.Page)
	case "/api/asset":
		if !s.validAd(in.Ad) {
			http.Error(w, "Advertisement expired; refresh", 410)
			return
		}
		var file *client.ReaderFile
		if in.Part == "avatar" {
			file = s.ad.ad.Avatar
		}
		if in.Part == "media" {
			file = s.ad.ad.Media
		}
		if file == nil {
			http.Error(w, "Media unavailable", 404)
			return
		}
		b, ok := s.ad.files[in.Part]
		if !ok {
			var err error
			b, err = s.backend.File(ctx, *file)
			if err != nil {
				fail(w, err)
				return
			}
			if len(b) == 0 || int64(len(b)) > client.ReaderMediaLimit {
				http.Error(w, "Media limit exceeded", 413)
				return
			}
			s.ad.files[in.Part] = b
		}
		w.Header().Set("Content-Type", file.MIME)
		_, _ = w.Write(b)
	case "/api/event":
		if !s.validAd(in.Ad) {
			http.Error(w, "Advertisement expired; refresh", 410)
			return
		}
		if !eventID.MatchString(in.Event) || len(s.events) >= 1000 {
			http.Error(w, "Invalid event", 400)
			return
		}
		for _, part := range []struct {
			name string
			file *client.ReaderFile
		}{{"avatar", s.ad.ad.Avatar}, {"media", s.ad.ad.Media}} {
			if part.file != nil && s.ad.files[part.name] == nil {
				http.Error(w, "Advertisement media is not ready", 409)
				return
			}
		}
		if in.Action != "view" && in.Action != "click" && in.Action != "report" {
			http.Error(w, "Invalid action", 400)
			return
		}
		if (in.Media || in.Fullscreen) && in.Action != "click" || in.Media && s.ad.ad.Media == nil || in.Fullscreen && (s.ad.ad.Media == nil || s.ad.ad.Media.Kind == "photo") {
			http.Error(w, "Invalid interaction flags", 400)
			return
		}
		if in.Action == "report" && !s.ad.ad.CanReport {
			http.Error(w, "Reporting unavailable", 400)
			return
		}
		option, ok := s.ad.options[in.Option]
		if in.Option != "" && (!ok || in.Action != "report") {
			http.Error(w, "Invalid report option", 400)
			return
		}
		if in.Action == "view" && s.ad.viewAttempted {
			http.Error(w, "View already attempted; not repeated", 409)
			return
		}
		fingerprint := fmt.Sprintf("%s/%s/%t/%t/%s", in.Ad, in.Action, in.Media, in.Fullscreen, in.Option)
		if _, exists := s.events[in.Event]; exists {
			http.Error(w, "Event already attempted; not repeated", 409)
			return
		}
		s.events[in.Event] = fingerprint
		if in.Action == "view" {
			s.ad.viewAttempted = true
		}
		digest := sha256.Sum256(append(append([]byte(in.Action+"/"), s.ad.ad.RandomID...), []byte("/"+in.Event)...))
		if in.Action == "view" {
			digest = sha256.Sum256(append([]byte("view/"), s.ad.ad.RandomID...))
		}
		result, err := s.backend.Action(ctx, Action{Kind: in.Action, RandomID: s.ad.ad.RandomID, Option: option, Media: in.Media, Fullscreen: in.Fullscreen, Key: "reader-" + hex.EncodeToString(digest[:])})
		if err != nil {
			fail(w, err)
			return
		}
		options := []map[string]string{}
		if in.Action == "report" {
			s.ad.options = map[string][]byte{}
			for i, o := range result.Options {
				key := strconv.Itoa(i)
				s.ad.options[key] = o.Value
				options = append(options, map[string]string{"id": key, "text": o.Text})
			}
		}
		writeJSON(w, map[string]any{"ok": true, "state": result.State, "title": result.Title, "options": options})
		if result.State == "reported" || result.State == "hidden" {
			s.ad = nil
		}
	default:
		http.NotFound(w, r)
	}
}

var eventID = regexp.MustCompile(`^[a-zA-Z0-9-]{16,64}$`)

func (s *Server) validAd(id string) bool {
	return s.ad != nil && s.ad.ad.ID == id && s.now().Before(s.cacheUntil)
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	code, _, _ := dispatch.Classify(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(502)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Reader request failed: " + code.String() + ". Required content or account access may be unavailable. Refresh messages or close this reader; uncertain actions are not retried."})
}
func safeURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.User == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && !strings.ContainsAny(raw, "\r\n\x00\t \\")
}
func validateAd(ad client.SponsoredAd, kind string) error {
	if !safeURL(ad.URL) || ad.Title == "" || ad.Text == "" || (kind != "bot" && ad.Button == "") || len(ad.Text) > 64<<10 || len(ad.Entities) > 100 || len(ad.RandomID) == 0 || len(ad.RandomID) > 1024 || !utf8.ValidString(ad.Text) {
		return errors.New("unsupported sponsored message; required content cannot be omitted")
	}
	units := utf16.Encode([]rune(ad.Text))
	boundary := func(n int) bool {
		return n == 0 || n == len(units) || !(units[n-1] >= 0xd800 && units[n-1] <= 0xdbff && units[n] >= 0xdc00 && units[n] <= 0xdfff)
	}
	for _, e := range ad.Entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset > len(units) || e.Length > len(units)-e.Offset || !boundary(e.Offset) || !boundary(e.Offset+e.Length) {
			return errors.New("invalid sponsored text boundary")
		}
		switch e.Type {
		case "bold", "italic", "underline", "strike", "spoiler", "code", "pre", "blockquote", "url", "text_url", "mention":
		default:
			return errors.New("unsupported sponsored text entity")
		}
		if e.Type == "text_url" && !safeURL(e.URL) {
			return errors.New("unsafe sponsored link")
		}
	}
	return nil
}
