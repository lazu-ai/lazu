package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

func collectRecording(t *testing.T, name string) textgen.Result {
	t.Helper()
	decoder := &StreamDecoder{}
	var col textgen.Collector
	for _, data := range fixtures.Data(t, name) {
		events, err := decoder.Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		col.Add(events...)
	}
	return col.Result()
}

func TestDecodeRecordedText(t *testing.T) {
	result := collectRecording(t, "gemini/text.sse")
	var text strings.Builder
	signature := ""
	for _, part := range result.Parts {
		switch part.Kind {
		case textgen.PartText:
			text.WriteString(part.Text)
		case textgen.PartReasoning:
			if part.SealedBy == SealedBy {
				signature = part.Signature
			}
		}
	}
	if text.String() != "Hello, nice to meet you." || signature == "" {
		t.Fatalf("text %q signature %q", text.String(), signature)
	}
	if !result.StopSeen || result.Stop != textgen.StopEnd || !strings.HasPrefix(result.Model, "gemini-") || result.ResponseID == "" {
		t.Fatalf("result: %+v", result)
	}
	usage := result.Usage
	if usage == nil || usage.PromptTokens != 6 || usage.CompletionTokens != 7+336 || usage.CompletionTokenDetails.ReasoningTokens != 336 {
		t.Fatalf("usage: %+v", usage)
	}
}

func TestDecodeRecordedToolCall(t *testing.T) {
	result := collectRecording(t, "gemini/tool_call.sse")
	var call *textgen.Part
	for index := range result.Parts {
		if result.Parts[index].Kind == textgen.PartTool {
			call = &result.Parts[index]
		}
	}
	if call == nil || call.Name != "get_weather" || call.CallID == "" || call.SealedBy != SealedBy || call.Signature == "" {
		t.Fatalf("tool call: %+v", call)
	}
	var args map[string]string
	if json.Unmarshal([]byte(call.Text), &args) != nil || args["city"] != "Paris" {
		t.Fatalf("arguments %q", call.Text)
	}
	if result.Stop != textgen.StopTool {
		t.Fatalf("a reply that called a tool stops for the tool: %s", result.Stop)
	}
}

func TestDecodeRecordedJSONSchemaAnswersJSON(t *testing.T) {
	result := collectRecording(t, "gemini/json_schema.sse")
	var text strings.Builder
	for _, part := range result.Parts {
		if part.Kind == textgen.PartText {
			text.WriteString(part.Text)
		}
	}
	if !json.Valid([]byte(text.String())) {
		t.Fatalf("not JSON: %q", text.String())
	}
}

func TestDecodeErrorsAndBlockedPrompts(t *testing.T) {
	decoder := &StreamDecoder{}
	events, err := decoder.Decode(`{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`)
	if err != nil || len(events) != 1 || events[0].Kind != textgen.EventError || events[0].Error.Message != "The model is overloaded." {
		t.Fatalf("error: %+v %v", events, err)
	}
	decoder = &StreamDecoder{}
	events, err = decoder.Decode(`{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":5,"totalTokenCount":5}}`)
	if err != nil {
		t.Fatal(err)
	}
	var col textgen.Collector
	col.Add(events...)
	if result := col.Result(); !result.StopSeen || result.Stop != textgen.StopFilter {
		t.Fatalf("a blocked prompt stops as filtered: %+v", result)
	}
}

// gemini-3.5-flash with includeThoughts: the thought summary arrives as
// reasoning, then the answer as text.
func TestDecodeRecordedThinking(t *testing.T) {
	result := collectRecording(t, "gemini/thinking.sse")
	var reasoning, text strings.Builder
	for _, part := range result.Parts {
		switch part.Kind {
		case textgen.PartReasoning:
			reasoning.WriteString(part.Text)
		case textgen.PartText:
			text.WriteString(part.Text)
		}
	}
	if reasoning.Len() == 0 || !strings.Contains(text.String(), "391") || result.Stop != textgen.StopEnd {
		t.Fatalf("reasoning %q text %q stop %s", reasoning.String(), text.String(), result.Stop)
	}
	if result.Usage == nil || result.Usage.CompletionTokenDetails.ReasoningTokens == 0 {
		t.Fatalf("usage: %+v", result.Usage)
	}
}

// Two calls in one reply, each whole, with distinct ids.
func TestDecodeRecordedParallelToolCalls(t *testing.T) {
	result := collectRecording(t, "gemini/parallel_tool_calls.sse")
	cities := map[string]bool{}
	ids := map[string]bool{}
	for _, part := range result.Parts {
		if part.Kind != textgen.PartTool {
			continue
		}
		var args map[string]string
		if part.Name != "get_weather" || json.Unmarshal([]byte(part.Text), &args) != nil {
			t.Fatalf("tool call: %+v", part)
		}
		cities[args["city"]] = true
		ids[part.CallID] = true
	}
	if !cities["Paris"] || !cities["Tokyo"] || len(ids) != 2 || result.Stop != textgen.StopTool {
		t.Fatalf("cities %v ids %v stop %s", cities, ids, result.Stop)
	}
}

// gemini-3.1-flash-image answers with an inline image; the decoder carries
// it as a data URL in the text (bytes shortened in the recording).
func TestDecodeRecordedImageOutput(t *testing.T) {
	result := collectRecording(t, "gemini/image_output.sse")
	var text strings.Builder
	for _, part := range result.Parts {
		if part.Kind == textgen.PartText {
			text.WriteString(part.Text)
		}
	}
	if !strings.Contains(text.String(), "data:image/jpeg;base64,") || !result.StopSeen {
		t.Fatalf("text %q result %+v", text.String(), result)
	}
}
