package client

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestMessageMediaTypeUsesTelegramAttributes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		media tg.MessageMediaClass
		want  string
	}{
		{"absent", nil, ""},
		{"photo", &tg.MessageMediaPhoto{}, "photo"},
		{"video", &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{}}}}, "video"},
		{"voice", &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true}}}}, "voice"},
		{"audio", &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{}}}}, "audio"},
		{"animation", &tg.MessageMediaDocument{Video: true, Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAnimated{}}}}, "animation"},
		{"round video", &tg.MessageMediaDocument{Round: true}, "video_note"},
		{"sticker", &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{}}}}, "sticker"},
		{"file", &tg.MessageMediaDocument{Document: &tg.Document{}}, "document"},
		{"typed nil", (*tg.MessageMediaDocument)(nil), ""},
		{"malformed attribute", &tg.MessageMediaDocument{Document: &tg.Document{Attributes: []tg.DocumentAttributeClass{(*tg.DocumentAttributeVideo)(nil)}}}, "document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageMediaType(tc.media); got != tc.want {
				t.Fatalf("media type %q, want %q", got, tc.want)
			}
		})
	}
}
