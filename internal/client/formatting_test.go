package client

import (
	"context"
	textutil "github.com/b1rd33/tgctl-go/internal/text"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"reflect"
	"testing"
)

func TestEditClearsFormattingAndSendPreservesUTF16(t *testing.T) {
	calls := 0
	g := &GotdClient{selfID: 7, api: tg.NewClient(invokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		switch req := in.(type) {
		case *tg.MessagesSendMessageRequest:
			if len(req.Entities) != 1 || req.Entities[0].GetOffset() != 3 {
				t.Fatal("entities lost")
			}
			out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 12}
		case *tg.MessagesEditMessageRequest:
			if _, present := req.GetEntities(); !present || len(req.Entities) != 0 {
				t.Fatal("plain edit must explicitly clear formatting")
			}
			if msg, present := req.GetMessage(); !present || msg != "plain" {
				t.Fatal("edit text missing")
			}
			out.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			t.Fatalf("request=%T", in)
		}
		return nil
	}))}
	entities := []textutil.Entity{{Type: "bold", Offset: 3, Length: 2}}
	if _, err := g.SendMessage(context.Background(), SendMessageReq{ChatID: 7, Text: "😀 hi", Entities: entities}); err != nil {
		t.Fatal(err)
	}
	if err := g.EditMessage(context.Background(), EditMessageReq{ChatID: 7, MessageID: 12, NewText: "plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SendMessage(context.Background(), SendMessageReq{ChatID: 7, Text: "😀", Entities: []textutil.Entity{{Type: "bold", Offset: 1, Length: 1}}}); err == nil {
		t.Fatal("split emoji accepted")
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestLiveEditsPreserveAndClearEntityMetadata(t *testing.T) {
	db := updateTestDB(t)
	s := newUpdateStorage(db)
	want := []textutil.Entity{{Type: "italic", Offset: 0, Length: 2}}
	for _, entities := range [][]textutil.Entity{want, {}} {
		msg := &tg.Message{ID: 10, PeerID: &tg.PeerUser{UserID: 7}, Date: 100, Message: "hi", Entities: textutil.TelegramEntities(entities)}
		if err := s.Handle(context.Background(), &tg.UpdateShort{Update: &tg.UpdateEditMessage{Message: msg}}); err != nil {
			t.Fatal(err)
		}
		var raw string
		if err := db.QueryRow("SELECT raw_json FROM tg_messages WHERE chat_id=7 AND message_id=10").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if got := textutil.ReadEntities(raw); !reflect.DeepEqual(got, entities) {
			t.Fatalf("got=%#v want=%#v", got, entities)
		}
	}
}
