package commands

import (
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/client"
)

func TestListenRejectsReadOnly(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "--read-only", "listen", "--once", "--json")
	if code != 6 {
		t.Fatalf("code=%d want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.ListenCalls) != 0 {
		t.Fatalf("client called: %#v", fc.ListenCalls)
	}
}

func TestListenOnceEmitsEventEnvelope(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.ListenEvents = []client.ListenEvent{{UpdateKind: "message", ChatID: 1, MessageID: 10, Text: "hi"}}
	out, code := runRoot(t, cfg, "listen", "--once", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"command":"listen.event"`) || !strings.Contains(out, `"message_id":10`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
