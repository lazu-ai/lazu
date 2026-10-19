package openai_responses

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestResponsesArrayTokenCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, input, text string
		invalid           bool
	}{
		{name: "plain string", input: `"hello"`, text: "hello"},
		{name: "null retains legacy empty text", input: `null`},
		{name: "JSON whitespace array", input: " \t\r\n" + `[{"role":"user","content":[{"type":"input_text","text":"\u96ea"}]}]`, text: "雪"},
		{name: "string content", input: `[{"role":"user","content":"hello"}]`, text: "hello"},
		{name: "null content retains legacy empty text", input: `[{"role":"user","content":null}]`},
		{name: "duplicate content keeps last value", input: `[{"role":"user","content":"first","content":[{"type":"input_text","text":"last"}]}]`, text: "last"},
		{name: "number", input: `42`, invalid: true},
		{name: "object", input: `{}`, invalid: true},
		{name: "boolean", input: `true`, invalid: true},
		{name: "empty array", input: `[]`, invalid: true},
		{name: "nonobject item", input: `["hello"]`, invalid: true},
		{name: "malformed array", input: `[{"role":"user","content":[{"type":"input_text","text":"hello"}]`, invalid: true},
		{name: "Unicode whitespace is invalid", input: "\u00a0" + `[{"role":"user","content":"hello"}]`, invalid: true},
		{name: "formfeed is invalid", input: "\f" + `[{"role":"user","content":"hello"}]`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, _, err := Decode([]byte(`{"model":"m","input":` + tc.input + `}`))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid input became accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(request.Conversation) != 1 || len(request.Conversation[0].Content) != 1 || request.Conversation[0].Content[0].Text != tc.text {
				t.Fatalf("input changed: %+v", request.Conversation)
			}
		})
	}
	for _, lastKey := range []string{"input", "INPUT"} {
		body := `{"model":"m","input":"first","` + lastKey + `":[{"role":"user","content":[{"type":"input_text","text":"last"}]}]}`
		request, _, err := Decode([]byte(body))
		if lastKey == "input" {
			if err == nil || !strings.Contains(err.Error(), `duplicate JSON field "input"`) {
				t.Fatalf("duplicate input must still be rejected: %v", err)
			}
			continue
		}
		if err != nil || len(request.Conversation) != 1 || request.Conversation[0].Content[0].Text != "last" {
			t.Fatalf("duplicate/case-folded input behavior changed: %v %+v", err, request)
		}
	}
}

func BenchmarkResponsesArrayDecode(b *testing.B) {
	for _, kb := range []int{1, 100, 600} {
		b.Run(fmt.Sprintf("%dKB", kb), func(b *testing.B) {
			word := "gateway request 客户 "
			text := strings.Repeat(word, kb*1024/len(word))
			body := []byte(`{"model":"gpt-mock","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":` + strconv.Quote(text) + `}]}]}`)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := Decode(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
