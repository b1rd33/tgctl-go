package client

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func TestGotdClientRejectsInt32OverflowBeforeDependencies(t *testing.T) {
	ctx := context.Background()
	g := &GotdClient{}
	over := int64(math.MaxInt32) + 1
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "send reply", run: func() error { _, err := g.SendMessage(ctx, SendMessageReq{ReplyTo: over}); return err }},
		{name: "selector reply", run: func() error { _, err := g.SendMessageBySelector(ctx, "@x", "x", over, false, false); return err }},
		{name: "upload reply", run: func() error { _, err := g.UploadFile(ctx, UploadFileReq{ReplyTo: over}); return err }},
		{name: "edit message", run: func() error { return g.EditMessage(ctx, EditMessageReq{MessageID: over}) }},
		{name: "forward message", run: func() error { _, err := g.Forward(ctx, ForwardReq{MessageIDs: []int64{over}}); return err }},
		{name: "forward topic", run: func() error { _, err := g.Forward(ctx, ForwardReq{MessageIDs: []int64{1}, TopicID: over}); return err }},
		{name: "pin message", run: func() error { return g.Pin(ctx, PinReq{MessageID: over}) }},
		{name: "react message", run: func() error { return g.React(ctx, ReactReq{MessageID: over}) }},
		{name: "mark read", run: func() error { return g.MarkRead(ctx, MarkReadReq{UpToID: over}) }},
		{name: "delete message", run: func() error {
			_, err := g.DeleteMessages(ctx, DeleteMessagesReq{MessageIDs: []int64{over}})
			return err
		}},
		{name: "edit topic", run: func() error { return g.EditTopic(ctx, EditTopicReq{TopicID: over}) }},
		{name: "pin topic", run: func() error { return g.PinTopic(ctx, PinTopicReq{TopicID: over}) }},
		{name: "update folder", run: func() error { return g.UpdateFolder(ctx, FolderUpdateReq{ID: over}) }},
		{name: "delete folder", run: func() error { return g.DeleteFolder(ctx, over) }},
		{name: "reorder folder", run: func() error { return g.ReorderFolders(ctx, []int64{over}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			var badArgs *safety.BadArgs
			if !errors.As(err, &badArgs) {
				t.Fatalf("error=%v, want BadArgs before dependency access", err)
			}
			if !strings.Contains(err.Error(), "32-bit") {
				t.Fatalf("error=%q, want int32 boundary diagnostic", err)
			}
		})
	}
}

func TestTelegramInt32ValidatorsAcceptMax(t *testing.T) {
	max := int64(math.MaxInt32)
	if err := validatePositiveTelegramInt32(max, "message_id"); err != nil {
		t.Fatal(err)
	}
	if err := validateOptionalTelegramInt32(max, "reply_to"); err != nil {
		t.Fatal(err)
	}
	if err := validatePositiveTelegramInts32([]int64{1, max}, "message_id"); err != nil {
		t.Fatal(err)
	}
}
