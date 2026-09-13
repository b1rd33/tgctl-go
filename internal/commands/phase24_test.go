package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
)

func TestRepliesRunnerBindsCursorToRootAndChat(t *testing.T) {
	cfg, fc, dir := setupWriteEnv(t)
	fc.Resolved = map[string]client.ResolvedPeer{"1": {ChatID: 1, Kind: "supergroup", Title: "Forum"}}
	fc.RepliesPage = client.RemotePage{Messages: []client.BackfillMessage{{ChatID: 1, MessageID: 30, Date: "2026-05-01T00:00:00Z", Text: "reply"}}, NextOffsetID: 30}
	p := readPaths{account: "default", db: dir + "/telegram.sqlite", session: dir + "/tg.session"}
	cfg.ReadOnlyClientFactory = func(context.Context, string) (client.Client, error) { return fc, nil }
	root := NewRootCommand()
	registerThreadReadCommands(root, cfg)
	root.SetArgs([]string{"replies", "1", "7", "--json"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&strings.Builder{})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	if data["root_message_id"].(float64) != 7 || data["source"] != "telegram" {
		t.Fatalf("data = %#v", data)
	}
	_ = p
}

func TestChatPermissionsReadUsesFakeAndMarksAdvisory(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Resolved = map[string]client.ResolvedPeer{"1": {ChatID: 1, Kind: "supergroup", Title: "Forum"}}
	fc.Permissions = client.PermissionInfo{Chat: client.ChatInfo{ID: 1, Title: "Forum"}, Role: "member", Effective: map[string]bool{"send_messages": true}, Advisory: true}
	root := NewRootCommand()
	registerAdminCommands(root, cfg)
	root.SetArgs([]string{"chat-permissions", "1", "--json"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&strings.Builder{})
	if code := ExecuteRoot(root); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"advisory":true`) || !strings.Contains(out.String(), `"send_messages":true`) {
		t.Fatalf("output=%s", out.String())
	}
}

func TestResolveAndAccountLimitsExposeExplicitSources(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Resolved = map[string]client.ResolvedPeer{"@saved": {ChatID: 1, Kind: "user", Username: "saved", Title: "Saved"}}
	fc.Limits = client.AccountLimits{Source: "telegram", Premium: func() *bool { v := false; return &v }(), PremiumKnown: true, CaptionLength: 1024, UploadMaxFileParts: 4000, UploadMaxBytes: 4000 * 524288}
	root := NewRootCommand()
	registerResolveCommand(root, cfg)
	registerAccountLimits(root, cfg)
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&strings.Builder{})
	root.SetArgs([]string{"resolve", "@saved", "--source", "telegram", "--json"})
	if code := ExecuteRoot(root); code != 0 || !strings.Contains(out.String(), `"source":"telegram"`) || !strings.Contains(out.String(), `"chat_id":1`) {
		t.Fatalf("resolve code=%d output=%s", code, out.String())
	}
	out.Reset()
	root.SetArgs([]string{"account-limits", "--json"})
	if code := ExecuteRoot(root); code != 0 || !strings.Contains(out.String(), `"premium":false`) || !strings.Contains(out.String(), `"upload_max_bytes":2097152000`) {
		t.Fatalf("limits code=%d output=%s", code, out.String())
	}
}
