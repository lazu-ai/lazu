package helper

import "testing"

func TestStreamEventCarriesOutputIgnoresMetadataFrames(t *testing.T) {
	for payload, want := range map[string]bool{
		`{"choices":[{"delta":{"role":"assistant","content":""}}]}`:                        false,
		`{"choices":[{"delta":{"content":"1 2"}}]}`:                                        true,
		`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`:                         true,
		`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"a\""}}]}}]}`:     true,
		`{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":64}}`:               false,
		`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`:         false,
		`{"type":"response.output_text.delta","delta":"Hello"}`:                            true,
		`{"type":"message_start","message":{"content":[],"role":"assistant"}}`:             false,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`:         true,
		`{"type":"content_block_start","content_block":{"type":"text","text":""}}`:         false,
		`{"candidates":[{"content":{"parts":[{"text":"Hi"}],"role":"model"}}]}`:            true,
		`{"candidates":[{"content":{"parts":[{"text":""}]}}],"usageMetadata":{"total":1}}`: false,
		`{"type": "response.output_text.delta", "delta": "hello"}`:                         true,
		`{"choices": [{"delta": {"role": "assistant", "content": ""}}]}`:                   false,
		`{"choices":[{"delta":{"content" :	"x"}}]}`:                                        true,
	} {
		if got := StreamEventCarriesOutput(payload); got != want {
			t.Errorf("%s = %t, want %t", payload, got, want)
		}
	}
}

func TestStreamEventCarriesAnswerSkipsReasoning(t *testing.T) {
	for payload, want := range map[string]bool{
		`{"choices":[{"delta":{"content":"1 2"}}]}`:                                                 true,
		`{"choices":[{"delta":{"reasoning_content":"thinking","content":""}}]}`:                     false,
		`{"choices":[{"delta":{"reasoning":"thinking"}}]}`:                                          false,
		`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"a\""}}]}}]}`:              true,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}`:         false,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":""}}`:            false,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`:                  true,
		`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"q"}}`:  true,
		`{"type":"response.reasoning_summary_text.delta","delta":"Considering"}`:                    false,
		`{"type": "response.reasoning_summary_text.done", "text": "Considering"}`:                   false,
		`{"type":"response.output_item.done","item":{"type":"reasoning","summary":[{"text":"x"}]}}`: false,
		`{"type":"response.output_text.delta","delta":"Hello"}`:                                     true,
		`{"type":"response.function_call_arguments.delta","delta":"{\"a"}`:                          true,
		`{"candidates":[{"content":{"parts":[{"text":"plan","thought":true}],"role":"model"}}]}`:    false,
		`{"candidates":[{"content":{"parts":[{"text":"Hi"}],"role":"model"}}]}`:                     true,
		`{"type":"message_start","message":{"content":[],"role":"assistant"}}`:                      false,
	} {
		if got := StreamEventCarriesAnswer(payload); got != want {
			t.Errorf("%s = %t, want %t", payload, got, want)
		}
	}
}
