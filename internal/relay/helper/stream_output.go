package helper

import "strings"

// streamOutputFields are the JSON string fields through which the streaming
// protocols we relay deliver generated output: chat deltas (content,
// reasoning_content, reasoning, tool-call arguments), Responses deltas
// (delta), Claude content_block_delta (text, thinking, partial_json) and
// Gemini parts (text).
var streamOutputFields = []string{
	`"content"`,
	`"reasoning_content"`,
	`"reasoning"`,
	`"arguments"`,
	`"delta"`,
	`"text"`,
	`"thinking"`,
	`"partial_json"`,
}

// StreamEventCarriesOutput reports whether one SSE data payload contains
// generated output, as opposed to protocol metadata such as a role chunk,
// response.created, message_start or a usage-only frame.
//
// It looks for a non-empty string value in any of the fields above rather
// than decoding the event, because it runs on every chunk of every stream and
// each protocol names its output differently. Insignificant whitespace around
// the colon is allowed, since upstreams do not all serialize compactly. A
// false positive costs a slightly early first-token time; a false negative
// only drops one sample from the stall measurement.
func StreamEventCarriesOutput(data string) bool {
	return anyNonEmptyStringField(data, streamOutputFields)
}

// streamAnswerFields are the output fields that carry the answer rather than
// reasoning: the list above without reasoning_content, reasoning and thinking.
var streamAnswerFields = []string{
	`"content"`,
	`"arguments"`,
	`"delta"`,
	`"text"`,
	`"partial_json"`,
}

// StreamEventCarriesAnswer reports whether one SSE data payload carries
// answer output — text or tool-call arguments — as opposed to reasoning or
// protocol metadata.
//
// The answer starts where decode speed can be measured honestly. Reasoning
// is billed as output whether or not it is streamed, and when an upstream
// hides it, all of it happens before the first answer token; dividing the
// whole output by the answer's duration would credit the route with
// generating the hidden reasoning in no time at all.
//
// Responses reasoning events reuse the answer's field names ("delta", and
// "text" on their done events), so they are recognised by event or item type
// first; Gemini marks a thought part with "thought": true.
func StreamEventCarriesAnswer(data string) bool {
	if hasStringFieldWithPrefix(data, `"type"`, "response.reasoning") ||
		hasStringFieldWithPrefix(data, `"type"`, "reasoning") ||
		strings.Contains(data, `"thought":true`) || strings.Contains(data, `"thought": true`) {
		return false
	}
	return anyNonEmptyStringField(data, streamAnswerFields)
}

func anyNonEmptyStringField(data string, fields []string) bool {
	for _, field := range fields {
		rest := data
		for {
			index := strings.Index(rest, field)
			if index < 0 {
				break
			}
			rest = rest[index+len(field):]
			if value, ok := jsonStringValueAfterKey(rest); ok && value {
				return true
			}
		}
	}
	return false
}

// hasStringFieldWithPrefix reports whether a quoted key appears with a string
// value that starts with prefix.
func hasStringFieldWithPrefix(data, key, prefix string) bool {
	rest := data
	for {
		index := strings.Index(rest, key)
		if index < 0 {
			return false
		}
		rest = rest[index+len(key):]
		value := strings.TrimLeft(rest, " \t\r\n")
		if !strings.HasPrefix(value, ":") {
			continue
		}
		value = strings.TrimLeft(value[1:], " \t\r\n")
		if strings.HasPrefix(value, `"`+prefix) {
			return true
		}
	}
}

// jsonStringValueAfterKey reads what follows a quoted key: optional
// whitespace, a colon, optional whitespace, then a string. It reports whether
// that was a key at all (ok) and whether its string value is non-empty.
func jsonStringValueAfterKey(rest string) (nonEmpty bool, ok bool) {
	rest = strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(rest, ":") {
		return false, false
	}
	rest = strings.TrimLeft(rest[1:], " \t\r\n")
	if !strings.HasPrefix(rest, `"`) {
		return false, false
	}
	rest = rest[1:]
	return rest != "" && rest[0] != '"', true
}
