package wirepatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/packages/domain/providercap"
	"github.com/tidwall/gjson"
)

// deepseekToolPatterns rewrites the ECMAScript null escape \0 to \u0000 inside
// every "pattern" string of the tools' JSON schemas. DeepSeek's schema
// validator rejects \0 with "Invalid schema for function ... is not a
// 'regex'"; OpenAI accepts it, so clients (Claude Code's tool definitions among
// them) send it.
//
// Only the offending string literals are replaced, spliced in place by gjson
// offset, so the body outside them is byte for byte what the codec compiled
// and the prompt cache prefix survives. A body without the three bytes `\\0`
// is returned as the same slice.
var deepseekToolPatterns = Patch{
	ID:       "deepseek_tool_patterns",
	Dialects: []string{providercap.DialectDeepSeek},
	Reason:   `DeepSeek rejects the ECMAScript escape \0 in tool JSON-schema "pattern" fields with "Invalid schema for function ... is not a 'regex'"; \u0000 is accepted.`,
	Source:   "https://github.com/yetone/magpie/blob/main/internal/gateway/deepseek_schema.go (MIT; deepseekToolPatterns)",
	Apply: func(body []byte, _ Context) []byte {
		if !bytes.Contains(body, []byte(`\\0`)) {
			return body
		}
		return toolPatterns(body, unicodeNullEscape)
	},
}

// toolPatterns is body with fix applied to every "pattern" of the tools'
// schemas: tools[].function.parameters (Chat) and tools[].parameters
// (Responses). Each replacement is spliced in place; when no pattern changes
// the caller's slice is returned.
func toolPatterns(body []byte, fix func(string) string) []byte {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || tools.Index <= 0 || !tools.IsArray() {
		return body
	}
	var targets []patternTarget
	collectPatterns(tools, false, fix, &targets)
	if len(targets) == 0 {
		return body
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].index < targets[j].index })

	out := make([]byte, 0, len(body)+6*len(targets))
	last := 0
	for _, t := range targets {
		if t.index < last || t.index+t.rawLen > len(body) {
			continue
		}
		out = append(out, body[last:t.index]...)
		out = append(out, t.replacement...)
		last = t.index + t.rawLen
	}
	return append(out, body[last:]...)
}

type patternTarget struct {
	index       int // offset of the string literal in the body
	rawLen      int // length of the literal including quotes
	replacement string
}

// collectPatterns walks the tools array. Outside a schema it only descends
// through "function" and into "parameters"; inside a schema it follows the
// JSON Schema keywords that hold sub-schemas, so nested properties, $defs,
// items and the combinators are covered while unrelated strings (descriptions,
// enum values, a property that happens to be named "pattern" under
// "properties") are left alone. gjson reports child offsets relative to the
// original body, which is what the splice needs.
func collectPatterns(node gjson.Result, inSchema bool, fix func(string) string, targets *[]patternTarget) {
	if !node.Exists() {
		return
	}
	if node.IsArray() {
		node.ForEach(func(_, item gjson.Result) bool {
			collectPatterns(item, inSchema, fix, targets)
			return true
		})
		return
	}
	if !node.IsObject() {
		return
	}
	node.ForEach(func(key, value gjson.Result) bool {
		k := key.String()
		if !inSchema {
			switch k {
			case "parameters":
				collectPatterns(value, true, fix, targets)
			case "function":
				collectPatterns(value, false, fix, targets)
			}
			return true
		}
		switch k {
		case "pattern":
			if value.Type == gjson.String && value.Index > 0 {
				var pattern string
				if err := json.Unmarshal([]byte(value.Raw), &pattern); err == nil {
					if fixed := fix(pattern); fixed != pattern {
						// Decoding and re-encoding the one literal may also
						// normalise its other escapes (\/ or <); that is
						// the same string to the vendor, and it stays inside
						// the literal being replaced anyway.
						*targets = append(*targets, patternTarget{index: value.Index, rawLen: len(value.Raw), replacement: encodeJSONString(fixed)})
					}
				}
			}
		case "properties", "$defs", "definitions", "dependentSchemas", "patternProperties":
			if value.IsObject() {
				value.ForEach(func(_, schema gjson.Result) bool {
					collectPatterns(schema, true, fix, targets)
					return true
				})
			}
		case "items", "additionalProperties", "contains", "propertyNames", "not", "if", "then", "else",
			"allOf", "anyOf", "oneOf", "prefixItems", "unevaluatedProperties", "unevaluatedItems", "contentSchema":
			collectPatterns(value, true, fix, targets)
		}
		return true
	})
}

// encodeJSONString is a JSON string literal for s without HTML escaping, so
// <, > and & come out as the codec wrote them.
func encodeJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// unicodeNullEscape rewrites the regex escape \0 to \u0000. A doubled
// backslash is literal and an octal escape (\01) keeps its own meaning, so
// neither is touched.
func unicodeNullEscape(pattern string) string {
	if !strings.Contains(pattern, `\0`) {
		return pattern
	}
	var out strings.Builder
	out.Grow(len(pattern) + 8)
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' || i+1 == len(pattern) {
			out.WriteByte(pattern[i])
			continue
		}
		next := pattern[i+1]
		if next == '0' && (i+2 == len(pattern) || pattern[i+2] < '0' || pattern[i+2] > '9') {
			out.WriteString(`\u0000`)
		} else {
			out.WriteByte('\\')
			out.WriteByte(next)
		}
		i++
	}
	return out.String()
}
