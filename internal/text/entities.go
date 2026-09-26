package text

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gotd/td/tg"
)

// Entity uses Telegram's UTF-16 offsets, independently of UTF-8 input bytes.
type Entity struct {
	Type     string `json:"type"`
	Offset   int    `json:"offset"`
	Length   int    `json:"length"`
	URL      string `json:"url,omitempty"`
	Language string `json:"language,omitempty"`
}

const MaxInputBytes = 64 * 1024

func ParseEntities(raw, body string, maxUnits int) ([]Entity, error) {
	var entities []Entity
	if raw != "" {
		if !strings.HasPrefix(strings.TrimSpace(raw), "[") {
			return nil, fmt.Errorf("entities must be a JSON array")
		}
		if len(raw) > MaxInputBytes {
			return nil, fmt.Errorf("entities exceed 64 KiB")
		}
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&entities); err != nil {
			return nil, fmt.Errorf("entities must be a JSON array of type/offset/length objects")
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return nil, fmt.Errorf("entities contain trailing JSON")
		}
	}
	return entities, ValidateEntities(body, entities, maxUnits)
}

func ValidateEntities(body string, entities []Entity, maxUnits int) error {
	if len(body) > MaxInputBytes || !utf8.ValidString(body) {
		return fmt.Errorf("text must be valid UTF-8 and at most 64 KiB")
	}
	boundaries := map[int]bool{0: true}
	units := 0
	for _, r := range body {
		units++
		if r > 0xffff {
			units++
		}
		boundaries[units] = true
	}
	if units > maxUnits {
		return fmt.Errorf("text exceeds the local limit of %d UTF-16 units", maxUnits)
	}
	if len(entities) > 100 {
		return fmt.Errorf("at most 100 entities are supported")
	}
	for i, e := range entities {
		if e.Offset < 0 || e.Length <= 0 || e.Offset > units || e.Length > units-e.Offset || !boundaries[e.Offset] || !boundaries[e.Offset+e.Length] {
			return fmt.Errorf("entity %d has an invalid UTF-16 range", i)
		}
		switch e.Type {
		case "bold", "italic", "underline", "strike", "spoiler", "code", "pre", "blockquote", "text_url":
		default:
			return fmt.Errorf("unsupported entity type at index %d", i)
		}
		if e.Type == "text_url" {
			u, err := url.Parse(e.URL)
			if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
				return fmt.Errorf("entity %d requires an http(s) URL without credentials", i)
			}
		} else if e.URL != "" {
			return fmt.Errorf("url is only valid on text_url entities")
		}
		if e.Type != "pre" && e.Language != "" {
			return fmt.Errorf("language is only valid on pre entities")
		}
		for j := 0; j < i; j++ {
			p := entities[j]
			if e.Offset >= p.Offset+p.Length || p.Offset >= e.Offset+e.Length {
				continue
			}
			if e.Type == "code" || e.Type == "pre" || p.Type == "code" || p.Type == "pre" {
				return fmt.Errorf("code/pre entities cannot overlap other entities")
			}
			if !((e.Offset >= p.Offset && e.Offset+e.Length <= p.Offset+p.Length) || (p.Offset >= e.Offset && p.Offset+p.Length <= e.Offset+e.Length)) {
				return fmt.Errorf("entity ranges must be disjoint or fully nested")
			}
			if !styleEntity(e.Type) && !styleEntity(p.Type) {
				return fmt.Errorf("only style entities may nest inside other entities")
			}
		}
	}
	return nil
}

func styleEntity(kind string) bool {
	return kind == "bold" || kind == "italic" || kind == "underline" || kind == "strike" || kind == "spoiler"
}

// TelegramEntities must be called only after ValidateEntities.
func TelegramEntities(entities []Entity) []tg.MessageEntityClass {
	var out []tg.MessageEntityClass
	for _, e := range entities {
		var v tg.MessageEntityClass
		switch e.Type {
		case "bold":
			v = &tg.MessageEntityBold{Offset: e.Offset, Length: e.Length}
		case "italic":
			v = &tg.MessageEntityItalic{Offset: e.Offset, Length: e.Length}
		case "underline":
			v = &tg.MessageEntityUnderline{Offset: e.Offset, Length: e.Length}
		case "strike":
			v = &tg.MessageEntityStrike{Offset: e.Offset, Length: e.Length}
		case "spoiler":
			v = &tg.MessageEntitySpoiler{Offset: e.Offset, Length: e.Length}
		case "code":
			v = &tg.MessageEntityCode{Offset: e.Offset, Length: e.Length}
		case "pre":
			v = &tg.MessageEntityPre{Offset: e.Offset, Length: e.Length, Language: e.Language}
		case "blockquote":
			v = &tg.MessageEntityBlockquote{Offset: e.Offset, Length: e.Length}
		case "text_url":
			v = &tg.MessageEntityTextURL{Offset: e.Offset, Length: e.Length, URL: e.URL}
		}
		if v != nil {
			out = append(out, v)
		}
	}
	return out
}

// MessageJSON preserves the TL entity type lost by encoding/json on interface
// values. Other original message fields remain available in raw_json.
func MessageJSON(m *tg.Message) ([]byte, error) {
	type message tg.Message
	return json.Marshal(struct {
		*message
		Entities []Entity `json:"tgctl_entities"`
	}{(*message)(m), FromTelegram(m.Entities)})
}

func FromTelegram(entities []tg.MessageEntityClass) []Entity {
	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		if e == nil {
			continue
		}
		kind := strings.TrimPrefix(e.TypeName(), "messageEntity")
		if kind == "TextUrl" {
			kind = "text_url"
		}
		v := Entity{Type: strings.ToLower(kind), Offset: e.GetOffset(), Length: e.GetLength()}
		switch t := e.(type) {
		case *tg.MessageEntityTextURL:
			v.Type = "text_url"
			v.URL = t.URL
		case *tg.MessageEntityPre:
			v.Language = t.Language
		}
		out = append(out, v)
	}
	return out
}

// ReadEntities returns nil for old cache rows without typed entity metadata;
// an empty slice means a fresh read confirmed there are no entities.
func ReadEntities(raw string) []Entity {
	var v map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	b, ok := v["tgctl_entities"]
	if !ok {
		b, ok = v["entities"]
	}
	if !ok {
		return nil
	}
	var entities []Entity
	if json.Unmarshal(b, &entities) != nil {
		return nil
	}
	return entities
}
