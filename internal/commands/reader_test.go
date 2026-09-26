package commands

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/reader"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"strings"
	"testing"
)

type readerFake struct {
	*client.FakeClient
	views, clicks, reports, fetches int
	actionErr                       error
}

func (f *readerFake) GetSponsoredMessages(context.Context, int64) (client.SponsoredBatch, error) {
	f.fetches++
	return client.SponsoredBatch{}, nil
}
func (f *readerFake) DownloadReaderFile(context.Context, client.ReaderFile) ([]byte, error) {
	return []byte("data"), nil
}
func (f *readerFake) ViewSponsoredMessage(context.Context, []byte) error {
	f.views++
	return f.actionErr
}
func (f *readerFake) ClickSponsoredMessage(context.Context, []byte, bool, bool) error {
	f.clicks++
	return f.actionErr
}
func (f *readerFake) ReportSponsoredMessage(context.Context, []byte, []byte) (client.SponsoredReport, error) {
	f.reports++
	return client.SponsoredReport{State: "reported"}, f.actionErr
}

func TestReaderPreflightAndDryRunDoNotOpenPathsOrClients(t *testing.T) {
	cfg := CommandsConfig{Paths: failingAccountPaths{}, ClientFactory: func(context.Context, string, string) (client.Client, error) {
		t.Fatal("client opened")
		return nil, nil
	}, ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) {
		t.Fatal("readonly client opened")
		return nil, nil
	}}
	for _, args := range [][]string{{"reader", "self", "--dry-run"}, {"reader", "self", "--allow-write", "--dry-run"}, {"--account", "default", "reader", "self", "--allow-write", "--read-only", "--dry-run"}, {"--account", "default", "reader", "title", "--allow-write", "--dry-run"}, {"--account", "default", "reader", "self", "--allow-write", "--limit", "101", "--dry-run"}, {"--account", "default", "reader", "self", "--allow-write", "--duration", "61m", "--dry-run"}} {
		if out, code := runRoot(t, cfg, append(args, "--json")...); code == 0 {
			t.Fatalf("unsafe startup: %v %s", args, out)
		}
	}
	out, code := runRoot(t, cfg, "--account", "default", "reader", "self", "--allow-write", "--dry-run", "--json")
	if code != 0 || !strings.Contains(out, `"dry_run":true`) {
		t.Fatalf("dry run touched paths: %d %s", code, out)
	}
}
func TestReaderBindsIdentityAndClosesEveryRead(t *testing.T) {
	f := &readerFake{FakeClient: &client.FakeClient{Me: client.User{ID: 10}, Resolved: map[string]client.ResolvedPeer{"self": {ChatID: 10}}, ChatInfos: []client.ChatInfo{{ID: 10, Type: "channel"}}, RemoteHistoryPage: client.RemotePage{Messages: []client.BackfillMessage{{MessageID: 2}, {MessageID: 1}}}}}
	opens := 0
	b := &readerBackend{selector: "self", limit: 2, cfg: CommandsConfig{ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) { opens++; f.Closed = false; return f, nil }}}
	p, err := b.Page(context.Background(), 0, true)
	if err != nil || !f.Closed || p.Messages[0].ID != 1 || f.fetches != 1 || b.userID != 10 {
		t.Fatal("read binding/release", err)
	}
	if _, err = b.File(context.Background(), client.ReaderFile{}); err != nil || !f.Closed || opens != 2 {
		t.Fatal("media read release")
	}
	f.Me.ID = 11
	if _, err = b.Page(context.Background(), 0, true); err == nil || !f.Closed || f.fetches != 1 {
		t.Fatal("switched account admitted")
	}
	f.Me.ID = 10
	f.Resolved["self"] = client.ResolvedPeer{ChatID: 11}
	if _, err = b.Page(context.Background(), 0, true); err == nil {
		t.Fatal("switched peer admitted")
	}
}
func TestReaderCallbacksUseNormalWritePipeline(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	dbPath, sessionPath, auditPath, _ := cfg.Paths.AccountPaths("default")
	f := &readerFake{FakeClient: fc}
	fc.Me.ID = 10
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) { return f, nil }
	b := &readerBackend{cfg: cfg, paths: readPaths{db: dbPath, session: sessionPath, audit: auditPath}, peerID: 1, userID: 10, gate: safety.Args{AllowWrite: true}}
	a := reader.Action{Kind: "view", RandomID: []byte("ad"), Key: "reader-synthetic-view"}
	for i := 0; i < 2; i++ {
		if _, err := b.Action(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if f.views != 1 || idempotencyCount(t, dbPath, a.Key) != 1 {
		t.Fatal("view replay bypassed pipeline")
	}
	a.Kind = "click"
	a.Key = "reader-synthetic-unknown"
	f.actionErr = context.DeadlineExceeded
	if _, err := b.Action(context.Background(), a); err == nil {
		t.Fatal("unknown succeeded")
	}
	f.actionErr = nil
	if _, err := b.Action(context.Background(), a); err == nil || f.clicks != 1 {
		t.Fatal("unknown retried")
	}
	a.Kind = "report"
	a.Key = "reader-synthetic-report"
	fc.Me.ID = 11
	if _, err := b.Action(context.Background(), a); err == nil || f.reports != 0 {
		t.Fatal("wrong account callback")
	}
	b.gate.ReadOnly = true
	if _, err := b.Action(context.Background(), a); err == nil {
		t.Fatal("readonly callback")
	}
	b.gate.ReadOnly = false
	fc.Me.ID = 10
	fc.CloseErr = errors.New("private close error")
	a.Key = "reader-close-failure"
	if _, err := b.Action(context.Background(), a); err == nil || strings.Contains(err.Error(), "private close error") {
		t.Fatal("close outcome")
	}
}
