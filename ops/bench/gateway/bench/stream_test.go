package main

import (
	"io"
	"strings"
	"testing"
)

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestFixtureStreamRejectsTruncatedSuccess(t *testing.T) {
	content := "data: {\"delta\":\"tok0 \"}\n\n"
	terminal := "data: [DONE]\n\n"
	for _, test := range []struct {
		name string
		body io.Reader
		want bool
	}{
		{"complete", strings.NewReader(content + terminal), true},
		{"empty200", strings.NewReader(""), false},
		{"noContent", strings.NewReader(terminal), false},
		{"noTerminal", strings.NewReader(content), false},
		{"partialTerminalFrame", strings.NewReader(content + "data: [DONE]\n"), false},
		{"transportTruncation", io.MultiReader(strings.NewReader(content+terminal), brokenBody{}), false},
		{"missingContentEvents", strings.NewReader(content + content + terminal), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := readFixtureStream(test.body, func() float64 { return 1 })
			if got := facts.completionError(true, 1) == nil; got != test.want {
				t.Fatalf("valid=%v want=%v facts=%+v", got, test.want, facts)
			}
		})
	}
}
