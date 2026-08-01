package client

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestBackfillPreservesGroupedIDFromMessagesAndChannelPages(t *testing.T) {
	for _, tc := range []struct {
		name string
		page historyPage
	}{
		{name: "messages", page: historyPage{Messages: []tg.MessageClass{&tg.Message{ID: 101, GroupedID: 7001, Message: "photo"}, &tg.Message{ID: 102, GroupedID: 7001, Message: "video"}}, Total: 2, TotalKnown: true}},
		{name: "channel", page: historyPage{Messages: []tg.MessageClass{&tg.Message{ID: 201, GroupedID: 7002, Message: "photo"}, &tg.Message{ID: 202, GroupedID: 7002, Message: "video"}}, Total: 2, TotalKnown: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := (&GotdClient{}).paginateBackfillHistory(context.Background(), BackfillReq{ChatID: 42, Limit: 2}, func(context.Context, int, int) (historyPage, error) {
				return tc.page, nil
			}, func(context.Context, time.Duration) error { return nil })
			if err != nil {
				t.Fatalf("paginateBackfillHistory: %v", err)
			}
			if len(result.Messages) != 2 {
				t.Fatalf("messages len=%d", len(result.Messages))
			}
			if result.Messages[0].GroupedID == 0 || result.Messages[0].GroupedID != result.Messages[1].GroupedID {
				t.Fatalf("grouped ids = %d,%d", result.Messages[0].GroupedID, result.Messages[1].GroupedID)
			}
		})
	}
}

func TestHistoryResponseShapesRetainGroupedID(t *testing.T) {
	for _, resp := range []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: []tg.MessageClass{&tg.Message{ID: 1, GroupedID: 901}}},
		&tg.MessagesMessagesSlice{Messages: []tg.MessageClass{&tg.Message{ID: 2, GroupedID: 902}}},
		&tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.Message{ID: 3, GroupedID: 903}}},
	} {
		page := historyPageFromResp(resp)
		message, ok := page.Messages[0].(*tg.Message)
		if !ok || message.GroupedID == 0 {
			t.Fatalf("history page = %#v", page)
		}
	}
}
