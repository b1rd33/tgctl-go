package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func writeAlbumFixture(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func albumFakeConfig(t *testing.T) (CommandsConfig, *client.FakeClient, string) {
	t.Helper()
	cfg, fake, dir := setupWriteEnv(t)
	fake.AlbumResp = client.UploadAlbumResp{
		ChatID:     1,
		MessageIDs: []int64{7001, 7002},
		GroupedID:  9001,
		Items: []client.UploadAlbumItemResp{
			{Position: 0, MessageID: 7001, MediaType: "photo"},
			{Position: 1, MessageID: 7002, MediaType: "video"},
		},
	}
	return cfg, fake, dir
}

func TestUploadAlbumDryRunDoesNotCallTelegramOrMutateDurableState(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.mp4", []byte("video"))

	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--caption", "secret caption", "--allow-write", "--dry-run", "--idempotency-key", "album-dry", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(fake.Albums) != 0 {
		t.Fatalf("telegram called in dry-run: %#v", fake.Albums)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run audit state exists: %v", err)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_idempotency WHERE key = ?", "album-dry").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("dry-run cached idempotency row count=%d", count)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, out)
	}
	data := env["data"].(map[string]any)
	if data["dry_run"] != true || data["item_count"] != float64(2) {
		t.Fatalf("plan=%#v", data)
	}
	if data["caption_position"] != float64(0) {
		t.Fatalf("caption position=%#v", data["caption_position"])
	}
	if strings.Contains(out, "secret caption") || strings.Contains(out, first) || strings.Contains(out, second) {
		t.Fatalf("dry-run leaked caption/path: %s", out)
	}
}

func TestUploadAlbumClientCloseFailureIsCommittedAndRedacted(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	fake.CloseErr = errors.New("client close /private/telegram/session failed")
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--caption", "secret caption", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if !strings.Contains(out, `"message_ids":[7001,7002]`) || strings.Contains(out, "client close") || strings.Contains(out, "secret caption") || strings.Contains(out, first) {
		t.Fatalf("unsafe committed output=%s", out)
	}
}

func TestUploadAlbumMalformedResponseIsCommittedWithRecoveryIDs(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	fake.AlbumResp.MessageIDs = []int64{7001, 7001}
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"message_ids":[7001,7001]`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Contains(out, first) || strings.Contains(out, second) {
		t.Fatalf("malformed response leaked path: %s", out)
	}
}

func TestUploadAlbumIdempotencyFinalizationFailureIsCommitted(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	db, err := store.Connect(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER block_album_idempotency BEFORE UPDATE ON tg_idempotency BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--idempotency-key", "blocked", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"message_ids":[7001,7002]`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if strings.Contains(out, first) || strings.Contains(out, "blocked") {
		t.Fatalf("unsafe idempotency failure output=%s", out)
	}
	if len(fake.Albums) != 1 {
		t.Fatalf("album call count=%d", len(fake.Albums))
	}
}

func TestUploadAlbumUploadErrorRedactsSourcePath(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	fake.AlbumErr = errors.New("open " + first + ": secret caption transport failed")
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--caption", "secret caption", "--allow-write", "--json")
	if code == 0 || strings.Contains(out, first) || strings.Contains(out, "secret caption") {
		t.Fatalf("code=%d leaked source path: %s", code, out)
	}
}

func TestUploadAlbumDurableAuditPreflightBlocksTelegram(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	auditPath := filepath.Join(dir, "audit.log")
	if err := os.Mkdir(auditPath, 0o700); err != nil {
		t.Fatal(err)
	}
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--allow-write", "--json")
	if code == 0 || len(fake.Albums) != 0 || strings.Contains(out, auditPath) || strings.Contains(out, first) {
		t.Fatalf("code=%d calls=%d unsafe output=%s", code, len(fake.Albums), out)
	}
}

func TestUploadAlbumConcurrentSameKeySendsOnce(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.AlbumHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	args := []string{"upload-album", "1", first, second, "--idempotency-key", "same-key", "--allow-write", "--json"}
	type result struct {
		out  string
		code int
	}
	firstDone := make(chan result, 1)
	go func() {
		out, code := runRoot(t, cfg, args...)
		firstDone <- result{out: out, code: code}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first album did not reach Telegram fake")
	}
	secondDone := make(chan result, 1)
	go func() {
		out, code := runRoot(t, cfg, args...)
		secondDone <- result{out: out, code: code}
	}()
	select {
	case second := <-secondDone:
		if second.code != 2 || !strings.Contains(second.out, "already in progress") {
			t.Fatalf("second result=%#v", second)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second album did not return in-progress")
	}
	close(release)
	firstResult := <-firstDone
	if firstResult.code != 0 {
		t.Fatalf("first result=%#v", firstResult)
	}
	if len(fake.Albums) != 1 {
		t.Fatalf("Telegram album calls=%d", len(fake.Albums))
	}
}

func TestUploadAlbumFinalSendFailureRetainsReservation(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	fake.AlbumErr = &client.AlbumUploadError{Stage: "final-send-cancel", Err: errors.New("RPC outcome unknown"), OutcomeUnknown: true}
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--idempotency-key", "unknown", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || strings.Contains(out, first) || strings.Contains(out, "RPC outcome") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var resultJSON string
	if err := db.QueryRow("SELECT result_json FROM tg_idempotency WHERE key='unknown'").Scan(&resultJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultJSON, `"pending":true`) {
		t.Fatalf("reservation not retained: %s", resultJSON)
	}
}

func TestUploadAlbumRequiresWriteGate(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto"))
	_, code := runRoot(t, cfg, "upload-album", "1", first, second, "--json")
	if code != 6 {
		t.Fatalf("code=%d, want write disallowed", code)
	}
	if len(fake.Albums) != 0 {
		t.Fatalf("telegram called despite gate: %#v", fake.Albums)
	}
}

func TestUploadAlbumBoundsAndUnsupportedAreBadArgs(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	photo := writeAlbumFixture(t, "photo.jpg", []byte("\xff\xd8\xffphoto"))
	for _, args := range [][]string{
		{"upload-album", "1", photo, "--allow-write", "--json"},
		{"upload-album", "1", photo, photo, photo, photo, photo, photo, photo, photo, photo, photo, photo, "--allow-write", "--json"},
	} {
		_, code := runRoot(t, cfg, args...)
		if code != 2 {
			t.Fatalf("args=%v code=%d, want bad args", args, code)
		}
	}
	doc := writeAlbumFixture(t, "file.txt", []byte("text"))
	_, code := runRoot(t, cfg, "upload-album", "1", photo, doc, "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("unsupported code=%d, want bad args", code)
	}
	if len(fake.Albums) != 0 {
		t.Fatalf("telegram called for invalid album: %#v", fake.Albums)
	}
}

func TestUploadAlbumPreservesOrderAndOptionsAndPersistsAllRows(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.mp4", []byte("video"))
	fake.AlbumResp = client.UploadAlbumResp{ChatID: 1, MessageIDs: []int64{711, 712}, Items: []client.UploadAlbumItemResp{{Position: 0, MessageID: 711, MediaType: "photo"}, {Position: 1, MessageID: 712, MediaType: "video"}}}

	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--caption", "album caption", "--reply-to", "19", "--silent", "--supports-streaming", "--idempotency-key", "album-key", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(fake.Albums) != 1 {
		t.Fatalf("albums=%#v", fake.Albums)
	}
	req := fake.Albums[0]
	if req.ChatID != 1 || req.Caption != "album caption" || req.ReplyTo != 19 || !req.Silent || !req.SupportsStreaming || len(req.Items) != 2 {
		t.Fatalf("request=%#v", req)
	}
	if req.Items[0].Path != first || req.Items[1].Path != second {
		t.Fatalf("order=%#v", req.Items)
	}
	if req.Items[0].Caption != "album caption" {
		t.Fatalf("caption placement=%#v", req.Items)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=1 AND message_id IN (711,712)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("cached message rows=%d", count)
	}
	if !strings.Contains(out, `"message_ids":[711,712]`) {
		t.Fatalf("output=%s", out)
	}
	if strings.Contains(out, "album caption") || strings.Contains(out, first) || strings.Contains(out, second) {
		t.Fatalf("output leaked caption/path: %s", out)
	}
}

func TestUploadAlbumIdenticalReplaySkipsTelegramAndConflictRejects(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	args := []string{"upload-album", "1", first, second, "--caption", "caption", "--idempotency-key", "album-key", "--allow-write", "--json"}
	if _, code := runRoot(t, cfg, args...); code != 0 {
		t.Fatalf("first code=%d", code)
	}
	if _, code := runRoot(t, cfg, args...); code != 0 {
		t.Fatalf("replay code=%d", code)
	}
	if len(fake.Albums) != 1 {
		t.Fatalf("replay made Telegram calls=%d", len(fake.Albums))
	}
	changed := writeAlbumFixture(t, "changed.jpg", []byte("\xff\xd8\xffdifferent"))
	out, code := runRoot(t, cfg, "upload-album", "1", first, changed, "--caption", "caption", "--idempotency-key", "album-key", "--allow-write", "--json")
	if code != 2 || !strings.Contains(out, "already used") {
		t.Fatalf("conflict code=%d out=%s", code, out)
	}
	if len(fake.Albums) != 1 {
		t.Fatalf("conflict made Telegram calls=%d", len(fake.Albums))
	}
}

func TestUploadAlbumFailureIsNotCached(t *testing.T) {
	cfg, fake, dir := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	fake.AlbumErr = errors.New("transport failed")
	out, code := runRoot(t, cfg, "upload-album", "1", first, second, "--idempotency-key", "failed-album", "--allow-write", "--json")
	if code == 0 || !strings.Contains(out, "transport failed") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	db, err := store.ConnectReadonly(filepath.Join(dir, "telegram.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_idempotency WHERE key='failed-album'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed album cached=%d", count)
	}
}

func TestUploadAlbumContextFactoryIsUsed(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	called := false
	cfg.ClientFactory = func(ctx context.Context, _, _ string) (client.Client, error) {
		called = ctx != nil
		return fake, nil
	}
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	if _, code := runRoot(t, cfg, "upload-album", "1", first, second, "--allow-write", "--json"); code != 0 || !called {
		t.Fatalf("code=%d factory_called=%v", code, called)
	}
}

func TestUploadAlbumRequiresExplicitOrFuzzySelector(t *testing.T) {
	cfg, fake, _ := albumFakeConfig(t)
	first := writeAlbumFixture(t, "first.jpg", []byte("\xff\xd8\xffphoto"))
	second := writeAlbumFixture(t, "second.jpg", []byte("\xff\xd8\xffphoto2"))
	out, code := runRoot(t, cfg, "upload-album", "Bjørn", first, second, "--allow-write", "--json")
	if code != 2 || len(fake.Albums) != 0 || !strings.Contains(out, "fuzzy") {
		t.Fatalf("code=%d albums=%d out=%s", code, len(fake.Albums), out)
	}
}

func TestUploadAlbumDoesNotAdvertiseIgnoredConfirmFlag(t *testing.T) {
	cfg, _, _ := albumFakeConfig(t)
	if flag := uploadAlbumCommand(cfg).Flags().Lookup("confirm"); flag != nil {
		t.Fatalf("upload-album advertises an ignored confirmation flag")
	}
}
