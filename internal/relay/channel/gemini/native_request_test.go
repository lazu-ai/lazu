package gemini

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

func TestConvertNativeGeminiRequestPreservesPartControlsAndExplicitZeros(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeGemini,
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: "gemini-4-carbon",
		},
	}

	raw := []byte(`{
		"contents": [{
			"role": "user",
			"parts": [
				{
					"inlineData": {"mimeType": "video/mp4", "data": "AAAA"},
					"videoMetadata": {"fps": 10, "startOffset": "2s", "endOffset": "4s", "contentLayers": ["FRAMES"]},
					"mediaResolution": {"level": "media_resolution_high"},
					"mediaProcessing": "MEDIA_PROCESSING_DEFAULT",
					"partMetadata": {"source": "clip-1", "frameId": 9007199254740993}
				},
				{"text": "Describe what happens between 2s and 4s."}
			]
		}],
		"generationConfig": {
			"topP": 0,
			"topK": 0,
			"seed": 0,
			"responseLogprobs": false,
			"mediaResolution": "MEDIA_RESOLUTION_HIGH",
			"thinkingConfig": {
				"thinkingLevel": "low",
				"thinkingBudget": 0,
				"includeThoughts": false
			}
		}
	}`)

	out, err := adaptor.ConvertNativeGeminiRequest(raw, info)
	if err != nil {
		t.Fatalf("ConvertNativeGeminiRequest returned error: %v", err)
	}

	// Untouched requests should pass through byte-for-byte.
	if string(out) != string(raw) {
		t.Fatalf("expected untouched body to pass through verbatim:\ngot:  %s\nwant: %s", out, raw)
	}

	if got := gjson.GetBytes(out, "contents.0.parts.0.videoMetadata.fps").Int(); got != 10 {
		t.Fatalf("videoMetadata.fps = %d, want 10", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.videoMetadata.startOffset").Str; got != "2s" {
		t.Fatalf("videoMetadata.startOffset = %q, want 2s", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.videoMetadata.endOffset").Str; got != "4s" {
		t.Fatalf("videoMetadata.endOffset = %q, want 4s", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.mediaResolution.level").Str; got != "media_resolution_high" {
		t.Fatalf("part mediaResolution.level = %q, want media_resolution_high", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.partMetadata.frameId").Raw; got != "9007199254740993" {
		t.Fatalf("large int precision lost: %s", got)
	}
	for _, path := range []string{
		"generationConfig.topP",
		"generationConfig.topK",
		"generationConfig.seed",
		"generationConfig.responseLogprobs",
		"generationConfig.thinkingConfig.thinkingBudget",
		"generationConfig.thinkingConfig.includeThoughts",
	} {
		if !gjson.GetBytes(out, path).Exists() {
			t.Fatalf("explicit zero/false field %s was dropped: %s", path, out)
		}
	}
}

func TestConvertNativeGeminiRequestSurgicalEdits(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeGemini,
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: "gemini-4-carbon",
			ParamOverride: map[string]interface{}{
				"generationConfig": map[string]interface{}{
					"temperature": 0.25,
				},
			},
		},
	}

	raw := []byte(`{
		"systemInstruction": {"parts": [{"text": ""}]},
		"metadata": {"lazu": {"trace": "drop"}, "customer": "keep"},
		"service_tier": "priority",
		"contents": [
			{
				"parts": [
					{
						"fileData": {"fileUri": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
						"videoMetadata": {"fps": 2.5}
					}
				]
			},
			{
				"role": "user",
				"parts": [
					{
						"file_data": {"file_uri": "https://www.youtube.com/watch?v=abc"},
						"video_metadata": {"fps": 5}
					},
					{
						"fileData": {"fileUri": "https://www.youtube.com/watch?v=xyz", "mimeType": "video/mp4"}
					}
				]
			}
		]
	}`)

	out, err := adaptor.ConvertNativeGeminiRequest(raw, info)
	if err != nil {
		t.Fatalf("ConvertNativeGeminiRequest returned error: %v", err)
	}

	if gjson.GetBytes(out, "systemInstruction").Exists() {
		t.Fatalf("empty systemInstruction should be deleted: %s", out)
	}
	if gjson.GetBytes(out, "metadata.lazu").Exists() {
		t.Fatalf("metadata.lazu should be deleted: %s", out)
	}
	if got := gjson.GetBytes(out, "metadata.customer").Str; got != "keep" {
		t.Fatalf("metadata.customer = %q, want keep", got)
	}
	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("disabled field service_tier should be removed: %s", out)
	}
	if got := gjson.GetBytes(out, "contents.0.role").Str; got != "user" {
		t.Fatalf("contents.0.role = %q, want user", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.fileData.mimeType").Str; got != "video/webm" {
		t.Fatalf("camelCase YouTube mimeType = %q, want video/webm", got)
	}
	if got := gjson.GetBytes(out, "contents.0.parts.0.videoMetadata.fps").Float(); got != 2.5 {
		t.Fatalf("videoMetadata.fps = %v, want 2.5", got)
	}
	if got := gjson.GetBytes(out, "contents.1.parts.0.file_data.mime_type").Str; got != "video/webm" {
		t.Fatalf("snake_case YouTube mime_type = %q, want video/webm", got)
	}
	if got := gjson.GetBytes(out, "contents.1.parts.0.video_metadata.fps").Int(); got != 5 {
		t.Fatalf("snake_case video_metadata.fps = %d, want 5", got)
	}
	if got := gjson.GetBytes(out, "contents.1.parts.1.fileData.mimeType").Str; got != "video/mp4" {
		t.Fatalf("existing YouTube mimeType overwritten: %q", got)
	}
	if got := gjson.GetBytes(out, "generationConfig.temperature").Float(); got != 0.25 {
		t.Fatalf("ParamOverride temperature = %v, want 0.25", got)
	}
}

func TestConvertNativeGeminiRequestSystemInstructionAndMetadataCleanup(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:              constant.APITypeGemini,
			ChannelType:          constant.ChannelTypeGemini,
			ChannelOtherSettings: dto.ChannelOtherSettings{},
		},
	}

	t.Run("removes empty snake_case system_instruction and empty metadata", func(t *testing.T) {
		raw := []byte(`{"system_instruction":{"parts":[]},"metadata":{"lazu":{"k":"v"}},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
		out, err := adaptor.ConvertNativeGeminiRequest(raw, info)
		if err != nil {
			t.Fatal(err)
		}
		if gjson.GetBytes(out, "system_instruction").Exists() {
			t.Fatalf("empty system_instruction should be deleted: %s", out)
		}
		if gjson.GetBytes(out, "metadata").Exists() {
			t.Fatalf("empty metadata should be deleted: %s", out)
		}
	})

	t.Run("preserves non-empty systemInstruction", func(t *testing.T) {
		raw := []byte(`{"systemInstruction":{"parts":[{"text":"Be concise."}]},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
		out, err := adaptor.ConvertNativeGeminiRequest(raw, info)
		if err != nil {
			t.Fatal(err)
		}
		if got := gjson.GetBytes(out, "systemInstruction.parts.0.text").Str; got != "Be concise." {
			t.Fatalf("systemInstruction altered: %s", out)
		}
	})
}
