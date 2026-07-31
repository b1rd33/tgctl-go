package commands

import (
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
)

func TestTopicCreateCallsClientAndReplaysIdempotency(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.NextTopicID = 77
	first, code := runRoot(t, cfg, "topic-create", "1", "Launch", "--icon-emoji-id", "123", "--allow-write", "--idempotency-key", "topic-k", "--json")
	if code != 0 {
		t.Fatalf("first code=%d\nout:%s", code, first)
	}
	second, code := runRoot(t, cfg, "topic-create", "1", "Launch", "--icon-emoji-id", "123", "--allow-write", "--idempotency-key", "topic-k", "--json")
	if code != 0 {
		t.Fatalf("second code=%d\nout:%s", code, second)
	}
	if len(fc.TopicCreates) != 1 || fc.TopicCreates[0].Title != "Launch" {
		t.Fatalf("TopicCreates=%#v", fc.TopicCreates)
	}
	if !strings.Contains(second, `"idempotent_replay":true`) {
		t.Fatalf("missing replay flag: %s", second)
	}
}

func TestTopicEditRequiresMutation(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "topic-edit", "1", "55", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
}

func TestFoldersListAndShowUseClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Folders = []client.FolderInfo{{ID: 2, Title: "Ops", IncludeChatIDs: []int64{1}}}
	out, code := runRoot(t, cfg, "folders-list", "--json")
	if code != 0 {
		t.Fatalf("list code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"title":"Ops"`) {
		t.Fatalf("unexpected list: %s", out)
	}
	out, code = runRoot(t, cfg, "folder-show", "2", "--json")
	if code != 0 {
		t.Fatalf("show code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"folder_id":2`) {
		t.Fatalf("unexpected show: %s", out)
	}
}

func TestFolderDeleteRejectsDefaultFolder(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "folder-delete", "0", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.FolderDeletes) != 0 {
		t.Fatalf("client called: %#v", fc.FolderDeletes)
	}
}

func TestFolderDeleteRequiresTypedFolderID(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "folder-delete", "2", "--allow-write", "--json")
	if code != 7 {
		t.Fatalf("missing confirm code=%d want NEEDS_CONFIRM=7\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "folder-delete", "2", "--allow-write", "--confirm", "3", "--json")
	if code != 2 {
		t.Fatalf("mismatch code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
	out, code = runRoot(t, cfg, "folder-delete", "2", "--allow-write", "--confirm", " 2 ", "--json")
	if code != 0 {
		t.Fatalf("confirmed code=%d\nout:%s", code, out)
	}
	if len(fc.FolderDeletes) != 1 || fc.FolderDeletes[0] != 2 {
		t.Fatalf("FolderDeletes=%#v", fc.FolderDeletes)
	}
}

func TestFolderCreateReplaysIdempotency(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Folders = []client.FolderInfo{{ID: 2, Title: "Existing"}}
	first, code := runRoot(t, cfg, "folder-create", "Ops", "--include-chats", "1", "--allow-write", "--idempotency-key", "folder-k", "--json")
	if code != 0 {
		t.Fatalf("first code=%d\nout:%s", code, first)
	}
	second, code := runRoot(t, cfg, "folder-create", "Ops", "--include-chats", "1", "--allow-write", "--idempotency-key", "folder-k", "--json")
	if code != 0 {
		t.Fatalf("second code=%d\nout:%s", code, second)
	}
	if len(fc.FolderUpdates) != 1 {
		t.Fatalf("FolderUpdates=%#v", fc.FolderUpdates)
	}
	if !strings.Contains(second, `"idempotent_replay":true`) {
		t.Fatalf("missing replay flag: %s", second)
	}
}

func TestFolderCreateRejectsTitleOverTelegramLimit(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "folder-create", "admin-verify-20260509170450", "--include-chats", "1", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.FolderUpdates) != 0 {
		t.Fatalf("client called: %#v", fc.FolderUpdates)
	}
}
