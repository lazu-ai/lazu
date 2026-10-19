package contracttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

type fixtureCase struct {
	name       string
	file       string
	unknownKey string
	targetType string
}

var contractFixtures = []fixtureCase{
	{
		name:       "openai chat",
		file:       "openai_chat_request.json",
		unknownKey: "x_future_flag",
		targetType: "openai_chat",
	},
	{
		name:       "openai responses",
		file:       "openai_responses_request.json",
		unknownKey: "x_future_responses",
		targetType: "openai_responses",
	},
	{
		name:       "claude messages",
		file:       "claude_messages_request.json",
		unknownKey: "x_anthropic_future",
		targetType: "claude_messages",
	},
	{
		name:       "gemini generate",
		file:       "gemini_generate_content_request.json",
		unknownKey: "x_gemini_future",
		targetType: "gemini_generate",
	},
}

func TestContractFixtures_ValidJSONAndUnknownFieldPresence(t *testing.T) {
	t.Parallel()

	for _, tc := range contractFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := mustReadFixture(t, tc.file)
			obj := mustJSONMap(t, raw)

			if _, ok := obj[tc.unknownKey]; !ok {
				t.Fatalf("fixture %s must keep unknown field %q", tc.file, tc.unknownKey)
			}
		})
	}
}

func TestContractFixtures_UnknownFieldCanBeRecoveredByMergeStep(t *testing.T) {
	t.Parallel()

	for _, tc := range contractFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := mustReadFixture(t, tc.file)

			var converted []byte
			switch tc.targetType {
			case "openai_chat":
				typedMap := mustTypedRoundTripMap[dto.GeneralOpenAIRequest](t, raw)
				converted = mustMarshalMap(t, typedMap)
			case "openai_responses":
				typedMap := mustTypedRoundTripMap[dto.OpenAIResponsesRequest](t, raw)
				converted = mustMarshalMap(t, typedMap)
			case "claude_messages":
				typedMap := mustTypedRoundTripMap[dto.ClaudeRequest](t, raw)
				converted = mustMarshalMap(t, typedMap)
			case "gemini_generate":
				typedMap := mustTypedRoundTripMap[dto.GeminiChatRequest](t, raw)
				converted = mustMarshalMap(t, typedMap)
			default:
				t.Fatalf("unknown target type: %s", tc.targetType)
			}

			merged, err := relaycommon.MergeUnknownTopLevelFields(raw, converted)
			if err != nil {
				t.Fatalf("merge unknown fields failed: %v", err)
			}

			mergedMap := mustJSONMap(t, merged)
			if _, ok := mergedMap[tc.unknownKey]; !ok {
				t.Fatalf("expected merge step to recover unknown field %q", tc.unknownKey)
			}
		})
	}
}

func mustReadFixture(t *testing.T, file string) []byte {
	t.Helper()

	path := filepath.Join("testdata", file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s failed: %v", file, err)
	}
	return data
}

func mustJSONMap(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("fixture must be valid JSON object: %v", err)
	}
	return obj
}

func mustTypedRoundTripMap[T any](t *testing.T, data []byte) map[string]any {
	t.Helper()

	var req T
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("typed decode failed: %v", err)
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("typed re-marshal failed: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(encoded, &obj); err != nil {
		t.Fatalf("typed output map decode failed: %v", err)
	}
	return obj
}

func mustMarshalMap(t *testing.T, obj map[string]any) []byte {
	t.Helper()

	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal map failed: %v", err)
	}
	return data
}
