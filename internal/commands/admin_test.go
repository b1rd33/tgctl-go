package commands

import (
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
)

func TestChatTitleInvokesClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "chat-title", "1", "Renamed", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].Action != "chat-title" || fc.AdminActions[0].Value != "Renamed" {
		t.Fatalf("AdminActions=%#v", fc.AdminActions)
	}
}

func TestSetPermissionsAcceptsSendMessagesFlag(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "set-permissions", "1", "--send-messages", "--allow-write", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"send_messages":true`) {
		t.Fatalf("out=%s", out)
	}
	if len(fc.AdminActions) != 0 {
		t.Fatalf("dry-run called client: %#v", fc.AdminActions)
	}

	out, code = runRoot(t, cfg, "set-permissions", "1", "--send-messages", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].Action != "set-permissions" || fc.AdminActions[0].Value != "send-messages" {
		t.Fatalf("AdminActions=%#v", fc.AdminActions)
	}
}

func TestBanFromChatRequiresTypedUserConfirm(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "ban-from-chat", "1", "42", "--allow-write", "--json")
	if code != 7 {
		t.Fatalf("code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 0 {
		t.Fatalf("client called: %#v", fc.AdminActions)
	}
	out, code = runRoot(t, cfg, "ban-from-chat", "1", "42", "--confirm", "42", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("confirmed code=%d\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].Action != "ban-from-chat" {
		t.Fatalf("AdminActions=%#v", fc.AdminActions)
	}
}

func TestPromoteRequiresResolvedChatConfirmation(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "promote", "1", "42", "--allow-write", "--json")
	if code != 7 {
		t.Fatalf("missing confirm code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "promote", "1", "42", "--allow-write", "--confirm", "42", "--json")
	if code != 2 {
		t.Fatalf("user-id confirm code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "promote", "1", "42", "--allow-write", "--confirm", " 1 ", "--json")
	if code != 0 {
		t.Fatalf("chat-id confirm code=%d\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].Action != "promote" {
		t.Fatalf("AdminActions=%#v", fc.AdminActions)
	}
}

func TestDemoteRequiresResolvedChatConfirmation(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "demote", "1", "42", "--allow-write", "--json")
	if code != 7 {
		t.Fatalf("missing confirm code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "demote", "1", "42", "--allow-write", "--confirm", "42", "--json")
	if code != 2 {
		t.Fatalf("user-id confirm code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "demote", "1", "42", "--allow-write", "--confirm", " 1 ", "--json")
	if code != 0 {
		t.Fatalf("chat-id confirm code=%d\nout:%s", code, out)
	}
	if len(fc.AdminActions) != 1 || fc.AdminActions[0].Action != "demote" {
		t.Fatalf("AdminActions=%#v", fc.AdminActions)
	}
}

func TestChatsInfoAndMembersReadCommands(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.ChatInfos = []client.ChatInfo{{ID: 1, Type: "user", Title: "Alpha"}}
	fc.Members = []client.MemberInfo{{UserID: 42, Username: "ada", DisplayName: "Ada"}}
	out, code := runRoot(t, cfg, "chats-info", "1", "--json")
	if code != 0 || !strings.Contains(out, `"title":"Alpha"`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	out, code = runRoot(t, cfg, "chat-members", "1", "--json")
	if code != 0 || !strings.Contains(out, `"username":"ada"`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestAccountSessionsUsesListSessions(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Sessions = []client.SessionRef{{Hash: 123, DeviceName: "Mac", Platform: "macOS", IsCurrent: true}}
	out, code := runRoot(t, cfg, "account-sessions", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"device_name":"Mac"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
