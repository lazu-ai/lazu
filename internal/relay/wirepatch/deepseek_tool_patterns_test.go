package wirepatch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unsafe"

	"github.com/lazu-ai/lazu/packages/domain/providercap"
	"github.com/tidwall/gjson"
)

func applyDeepSeek(body []byte) []byte {
	return deepseekToolPatterns.Apply(body, Context{Dialect: providercap.DialectDeepSeek})
}

func sameSlice(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || unsafe.SliceData(a) == unsafe.SliceData(b))
}

func TestDeepSeekToolPatterns(t *testing.T) {
	const nul = `\\0` // the three bytes a JSON encoder writes for the regex escape \0

	tests := []struct {
		name string
		body string
		// wantPatterns maps a gjson path of a "pattern" string to the decoded
		// string value expected after the patch.
		wantPatterns map[string]string
		// wantUntouched lists gjson paths whose raw bytes must be identical.
		wantUntouched []string
		changed       bool
	}{
		{
			name:         "chat pattern fixed",
			body:         `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"id":{"type":"string","pattern":"^[^` + nul + `]+$"}}}}}]}`,
			wantPatterns: map[string]string{"tools.0.function.parameters.properties.id.pattern": `^[^\u0000]+$`},
			changed:      true,
		},
		{
			name:         "responses tool shape fixed",
			body:         `{"model":"deepseek-chat","input":"hi","tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"id":{"type":"string","pattern":"a` + nul + `b"}}}}]}`,
			wantPatterns: map[string]string{"tools.0.parameters.properties.id.pattern": `a\u0000b`},
			changed:      true,
		},
		{
			name: "nested properties, $defs, items, combinators",
			body: `{"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object",` +
				`"properties":{"outer":{"type":"object","properties":{"inner":{"pattern":"` + nul + `a"}}},` +
				`"list":{"type":"array","items":{"pattern":"x` + nul + `"}},` +
				`"either":{"anyOf":[{"pattern":"` + nul + `"},{"type":"integer"}]}},` +
				`"$defs":{"Ref":{"pattern":"d` + nul + `"}},"additionalProperties":{"pattern":"` + nul + `z"}}}}]}`,
			wantPatterns: map[string]string{
				"tools.0.function.parameters.properties.outer.properties.inner.pattern": `\u0000a`,
				"tools.0.function.parameters.properties.list.items.pattern":             `x\u0000`,
				"tools.0.function.parameters.properties.either.anyOf.0.pattern":         `\u0000`,
				"tools.0.function.parameters.$defs.Ref.pattern":                         `d\u0000`,
				"tools.0.function.parameters.additionalProperties.pattern":              `\u0000z`,
			},
			changed: true,
		},
		{
			name: "non-pattern \\0 untouched",
			body: `{"messages":[{"role":"user","content":"literal ` + nul + ` in text"}],` +
				`"tools":[{"type":"function","function":{"name":"f","description":"desc ` + nul + `","parameters":{"type":"object",` +
				`"properties":{"pattern":{"type":"string","description":"a property named pattern ` + nul + `","enum":["` + nul + `"]},` +
				`"id":{"pattern":"` + nul + `"}},"required":["pattern"]}}}]}`,
			wantPatterns: map[string]string{"tools.0.function.parameters.properties.id.pattern": `\u0000`},
			wantUntouched: []string{
				"messages.0.content",
				"tools.0.function.description",
				"tools.0.function.parameters.properties.pattern.description",
				"tools.0.function.parameters.properties.pattern.enum.0",
				"tools.0.function.parameters.required",
			},
			changed: true,
		},
		{
			name:    "escaped backslash and octal escape are not null escapes",
			body:    `{"tools":[{"type":"function","function":{"name":"f","parameters":{"properties":{"a":{"pattern":"\\\\0 and ` + nul + `7 and ` + nul + `"}}}}}]}`,
			changed: true,
			wantPatterns: map[string]string{
				"tools.0.function.parameters.properties.a.pattern": `\\0 and \07 and \u0000`,
			},
		},
		{
			name:    "pattern outside tools is not a tool schema",
			body:    `{"response_format":{"json_schema":{"schema":{"properties":{"a":{"pattern":"` + nul + `"}}}}},"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]}`,
			changed: false,
		},
		{
			name:    "no tools",
			body:    `{"messages":[{"role":"user","content":"` + nul + `"}]}`,
			changed: false,
		},
		{
			name:    "body without \\0 is the same slice",
			body:    `{"tools":[{"type":"function","function":{"name":"f","parameters":{"properties":{"a":{"pattern":"^[a-z]+$"}}}}}]}`,
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := []byte(tt.body)
			if !gjson.ValidBytes(in) {
				t.Fatalf("test body is not valid JSON: %s", in)
			}
			before := append([]byte(nil), in...)
			out := applyDeepSeek(in)
			if !bytes.Equal(in, before) {
				t.Fatal("caller's slice was mutated")
			}
			if !tt.changed {
				if !sameSlice(out, in) {
					t.Fatalf("unchanged body must be returned as the same slice; got %s", out)
				}
				return
			}
			if sameSlice(out, in) || bytes.Equal(out, in) {
				t.Fatalf("body should have changed: %s", out)
			}
			if !gjson.ValidBytes(out) {
				t.Fatalf("patched body is not valid JSON: %s", out)
			}
			if bytes.Contains(out, []byte(`\\u0000`)) == false {
				t.Fatalf("no \\u0000 written: %s", out)
			}
			for path, want := range tt.wantPatterns {
				if got := gjson.GetBytes(out, path).String(); got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			for _, path := range tt.wantUntouched {
				if got, want := gjson.GetBytes(out, path).Raw, gjson.GetBytes(in, path).Raw; got != want {
					t.Errorf("%s changed: %s -> %s", path, want, got)
				}
			}
			// Everything before the first patched literal is byte-identical,
			// which is what keeps the prompt cache prefix stable.
			firstPatched := bytes.Index(out, []byte(`\\u0000`))
			if firstPatched < 0 {
				t.Fatal("no patched literal found")
			}
			prefixEnd := bytes.LastIndexByte(out[:firstPatched], '"')
			if !bytes.HasPrefix(in, out[:prefixEnd]) {
				t.Fatalf("prefix before the first patched field changed:\n in=%s\nout=%s", in, out)
			}
			// Beyond the pattern strings the document is semantically equal.
			var a, b any
			if err := json.Unmarshal(in, &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out, &b); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(fmtJSON(b), `\u0000`) {
				t.Fatalf("patched document lost the null escape: %s", fmtJSON(b))
			}
		})
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDeepSeekToolPatternsPreservesSurroundingBytes(t *testing.T) {
	// Unknown fields, HTML characters, numeric precision and whitespace all
	// survive, since only the literal is spliced.
	in := []byte(`{"model":"deepseek-chat", "vendor_number":9007199254740993,"html":"<b>&</b>","tools":[ {"type":"function","function":{"name":"f","parameters":{"properties":{"a":{"pattern":"\\0<>&"},"b":{"pattern":"ok"}}}}} ],"trailing":"<"}`)
	out := applyDeepSeek(in)
	want := []byte(`{"model":"deepseek-chat", "vendor_number":9007199254740993,"html":"<b>&</b>","tools":[ {"type":"function","function":{"name":"f","parameters":{"properties":{"a":{"pattern":"\\u0000<>&"},"b":{"pattern":"ok"}}}}} ],"trailing":"<"}`)
	if !bytes.Equal(out, want) {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
}

func TestUnicodeNullEscape(t *testing.T) {
	cases := map[string]string{
		``:             ``,
		`\0`:           `\u0000`,
		`a\0b`:         `a\u0000b`,
		`\\0`:          `\\0`,
		`\01`:          `\01`,
		`\0\0`:         `\u0000\u0000`,
		`\\\0`:         `\\\u0000`,
		`trailing\`:    `trailing\`,
		`[^\0-\x1f]`:   `[^\u0000-\x1f]`,
		`\u0000 stays`: `\u0000 stays`,
		`\n\0\t`:       `\n\u0000\t`,
		`\08`:          `\08`, // a digit after \0 is an octal escape, left to the vendor
	}
	for in, want := range cases {
		if got := unicodeNullEscape(in); got != want {
			t.Errorf("unicodeNullEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
