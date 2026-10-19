package common

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestSetExplicitStream(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  string
		stream bool
	}{
		{name: "adds false", input: `{"model":"kimi-k2.6"}`, stream: false},
		{name: "keeps true", input: `{"model":"kimi-k2.6","stream":true}`, stream: true},
		{name: "overrides stale value", input: `{"model":"kimi-k2.6","stream":true}`, stream: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := SetExplicitStream([]byte(test.input), test.stream)
			if err != nil {
				t.Fatal(err)
			}
			value := gjson.GetBytes(got, "stream")
			if !value.Exists() || value.Bool() != test.stream {
				t.Fatalf("stream=%v exists=%v, want stream=%v", value.Bool(), value.Exists(), test.stream)
			}
		})
	}
}
