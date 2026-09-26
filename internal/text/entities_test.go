package text

import (
	"encoding/json"
	"github.com/gotd/td/tg"
	"reflect"
	"strings"
	"testing"
)

func TestEntitiesValidateUTF16AndNesting(t *testing.T) {
	for _, tc := range []struct {
		name, body, raw string
		valid           bool
	}{
		{"emoji boundary", "😀 hi", `[{"type":"bold","offset":3,"length":2}]`, true},
		{"emoji whole", "😀 hi", `[{"type":"italic","offset":0,"length":2}]`, true},
		{"emoji split", "😀 hi", `[{"type":"bold","offset":1,"length":1}]`, false},
		{"past end", "abc", `[{"type":"bold","offset":2,"length":2}]`, false},
		{"nested", "abc", `[{"type":"bold","offset":0,"length":3},{"type":"italic","offset":1,"length":1}]`, true},
		{"partial overlap", "abcd", `[{"type":"bold","offset":0,"length":3},{"type":"italic","offset":2,"length":2}]`, false},
		{"code overlap", "abc", `[{"type":"code","offset":0,"length":3},{"type":"bold","offset":0,"length":3}]`, false},
		{"link", "abc", `[{"type":"text_url","offset":0,"length":3,"url":"https://example.com"}]`, true},
		{"credential link", "abc", `[{"type":"text_url","offset":0,"length":3,"url":"https://user:secret@example.com"}]`, false},
		{"unknown field", "abc", `[{"type":"bold","offset":0,"length":1,"invalid":true}]`, false},
		{"unknown kind", "abc", `[{"type":"anything","offset":0,"length":1}]`, false},
		{"null", "abc", `null`, false},
		{"trailing", "abc", `[] []`, false},
		{"too long", strings.Repeat("a", 4097), `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseEntities(tc.raw, tc.body, 4096)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	if err := ValidateEntities("\xff", nil, 4096); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}

func TestEntityTypesSurviveCacheJSON(t *testing.T) {
	want := []Entity{{Type: "bold", Length: 2}, {Type: "italic", Offset: 3, Length: 2}, {Type: "text_url", Offset: 6, Length: 2, URL: "https://example.com"}, {Type: "pre", Offset: 9, Length: 2, Language: "go"}}
	raw, err := MessageJSON(&tg.Message{ID: 1, Message: "ab cd ef gh", Entities: TelegramEntities(want)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ReadEntities(string(raw)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%#v", got)
	}
	raw, _ = MessageJSON(&tg.Message{ID: 1})
	if got := ReadEntities(string(raw)); got == nil || len(got) != 0 {
		t.Fatal("fresh empty entities must be []")
	}
	if ReadEntities(`{"Entities":[{"Offset":0,"Length":1}]}`) != nil {
		t.Fatal("must not guess old untyped entities")
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["ID"] != float64(1) {
		t.Fatal("original fields lost")
	}
}
