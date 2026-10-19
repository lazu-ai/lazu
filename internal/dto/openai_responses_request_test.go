package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIResponsesRequestParseInputReadsFileData(t *testing.T) {
	rawInput, err := json.Marshal([]map[string]any{
		{
			"type":      "input_file",
			"filename":  "paper.pdf",
			"file_data": "data:application/pdf;base64,JVBERg==",
		},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	req := OpenAIResponsesRequest{Input: rawInput}
	inputs := req.ParseInput()
	if len(inputs) != 1 {
		t.Fatalf("expected one input, got %d", len(inputs))
	}
	if inputs[0].FileUrl != "data:application/pdf;base64,JVBERg==" {
		t.Fatalf("unexpected file data input: %#v", inputs[0])
	}
}

func TestResponsesNestedMessageAdmissionIncludesTextAndMedia(t *testing.T) {
	req := &OpenAIResponsesRequest{Input: []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"the agent conversation"},{"type":"input_image","image_url":"https://example.invalid/image.png"}]}]`)}
	meta := req.GetTokenCountMeta()
	if !strings.Contains(meta.CombineText, "the agent conversation") || len(meta.Files) != 1 {
		t.Fatalf("nested message omitted: %+v", meta)
	}
}
