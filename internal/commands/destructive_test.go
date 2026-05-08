package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDeleteMsgRequiresTypedConfirm(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "delete-msg", "1", "10", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code = %d, want BAD_ARGS=2\nout: %s", code, out)
	}
	if len(fc.Deletes) != 0 {
		t.Fatalf("client called without confirm: %v", fc.Deletes)
	}
}

func TestDeleteMsgWrongConfirmRejected(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "delete-msg", "1", "10", "--allow-write", "--confirm", "Bjørn", "--json")
	if code != 2 {
		t.Fatalf("code = %d, want 2\nout: %s", code, out)
	}
}

func TestDeleteMsgCorrectConfirmExecutes(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "delete-msg", "1", "10,11", "--allow-write", "--confirm", "1", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Deletes) != 1 || len(fc.Deletes[0].MessageIDs) != 2 {
		t.Fatalf("fc.Deletes = %#v", fc.Deletes)
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(out), &env)
	summary := env["data"].(map[string]any)["summary"].(map[string]any)
	if summary["requested"].(float64) != 2 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestLeaveChatRefuses1on1User(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	if _, err := openSeed(cfg.Paths.(stubPaths).db,
		"INSERT INTO tg_chats(chat_id, type, title) VALUES (5, 'user', 'Alice')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, code := runRoot(t, cfg, "leave-chat", "5", "--allow-write", "--confirm", "5", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want 2\nout: %s", code, out)
	}
	if len(fc.Leaves) != 0 {
		t.Fatalf("Leave called for 1-on-1: %v", fc.Leaves)
	}
}

func TestLeaveChatGroupExecutes(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	if _, err := openSeed(cfg.Paths.(stubPaths).db,
		"INSERT INTO tg_chats(chat_id, type, title) VALUES (10, 'group', 'Team')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, code := runRoot(t, cfg, "leave-chat", "10", "--allow-write", "--confirm", "10", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Leaves) != 1 || fc.Leaves[0].ChatID != 10 {
		t.Fatalf("fc.Leaves = %#v", fc.Leaves)
	}
}

func TestBlockUserRequiresConfirm(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	if _, err := openSeed(cfg.Paths.(stubPaths).db,
		"INSERT INTO tg_chats(chat_id, type, title) VALUES (77, 'user', 'Spam')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, code := runRoot(t, cfg, "block-user", "77", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Blocks) != 0 {
		t.Fatalf("client called without confirm")
	}

	// With correct confirm, the call should land.
	out, code = runRoot(t, cfg, "block-user", "77", "--allow-write", "--confirm", "77", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Blocks) != 1 || fc.Blocks[0].UserID != 77 {
		t.Fatalf("fc.Blocks = %#v", fc.Blocks)
	}
}

func TestUnblockUserExecutes(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	if _, err := openSeed(cfg.Paths.(stubPaths).db,
		"INSERT INTO tg_chats(chat_id, type, title) VALUES (77, 'user', 'Spam')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, code := runRoot(t, cfg, "unblock-user", "77", "--allow-write", "--confirm", "77", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Unblocks) != 1 {
		t.Fatalf("fc.Unblocks = %#v", fc.Unblocks)
	}
}

func TestTerminateSessionTypedConfirm(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "terminate-session", "12345", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want 2\nout: %s", code, out)
	}
	if len(fc.Terms) != 0 {
		t.Fatalf("client called without confirm")
	}
	out, code = runRoot(t, cfg, "terminate-session", "12345", "--allow-write", "--confirm", "12345", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout: %s", code, out)
	}
	if len(fc.Terms) != 1 || fc.Terms[0].Hash != 12345 {
		t.Fatalf("fc.Terms = %#v", fc.Terms)
	}
}

// runRoot wires both write and destructive commands. Reuse from messages_write_test.go.
// Hint to readers: Phase 13 commands are registered by registerDestructiveCommands.
func init() {
	// keep linker happy for the bytes/strings imports in test helpers below.
	_ = bytes.MinRead
	_ = strings.Builder{}
}
