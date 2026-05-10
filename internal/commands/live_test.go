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

func TestShouldEmitListenEventFilters(t *testing.T) {
	dm := client.ListenEvent{UpdateKind: "new_message", ChatID: 1240314255, MessageID: 1, Text: "hi"}
	basicGroup := client.ListenEvent{UpdateKind: "new_message", ChatID: -200500300, MessageID: 1}
	channel := client.ListenEvent{UpdateKind: "channel_message", ChatID: 1421106796, MessageID: 1}
	statusUpdate := client.ListenEvent{UpdateKind: "user_status", ChatID: 0}

	cases := []struct {
		name       string
		event      client.ListenEvent
		onlyDMs    bool
		onlyGroups bool
		want       bool
	}{
		{"no filter, DM passes", dm, false, false, true},
		{"no filter, group passes", channel, false, false, true},
		{"no filter, status passes", statusUpdate, false, false, true},

		{"only-dms passes DMs", dm, true, false, true},
		{"only-dms blocks channel", channel, true, false, false},
		{"only-dms blocks basic group (negative chat_id new_message)", basicGroup, true, false, false},
		{"only-dms passes status (no chat target)", statusUpdate, true, false, true},

		{"only-groups blocks DMs", dm, false, true, false},
		{"only-groups passes channel", channel, false, true, true},
		{"only-groups passes basic group", basicGroup, false, true, true},
		{"only-groups passes status (no chat target)", statusUpdate, false, true, true},
	}
	for _, tc := range cases {
		got := shouldEmitListenEvent(tc.event, tc.onlyDMs, tc.onlyGroups)
		if got != tc.want {
			t.Errorf("%s: shouldEmitListenEvent(%+v, onlyDMs=%v, onlyGroups=%v) = %v, want %v",
				tc.name, tc.event, tc.onlyDMs, tc.onlyGroups, got, tc.want)
		}
	}
}

func TestListenOnlyDMsFlagSkipsChannelEventsAndStops(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.ListenEvents = []client.ListenEvent{
		{UpdateKind: "channel_message", ChatID: 1421106796, MessageID: 100, Text: "group noise 1"},
		{UpdateKind: "channel_message", ChatID: 1421106796, MessageID: 101, Text: "group noise 2"},
		{UpdateKind: "new_message", ChatID: 544822655, MessageID: 200, Text: "real DM"},
	}
	out, code := runRoot(t, cfg, "listen", "--once", "--only-dms", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if !strings.Contains(out, `"message_id":200`) {
		t.Fatalf("expected the DM event (message_id=200), got: %s", out)
	}
	if strings.Contains(out, `"message_id":100`) || strings.Contains(out, `"message_id":101`) {
		t.Fatalf("filter leaked group events into output: %s", out)
	}
	// FakeClient's ListenOnce was called 3 times to skip past the 2 group events.
	if len(fc.ListenCalls) != 3 {
		t.Fatalf("ListenCalls=%d want 3", len(fc.ListenCalls))
	}
}
