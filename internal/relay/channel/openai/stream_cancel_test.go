package openai

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestStreamClientCanceledError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest("POST", "/v1/responses", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	err := streamClientCanceledError(c)
	if err == nil || err.GetErrorCode() != types.ErrorCodeClientCanceled {
		t.Fatalf("error = %v, want client_canceled", err)
	}
	if !types.IsSkipRetryError(err) {
		t.Fatal("client cancellation must not retry another provider")
	}
}

func TestObservedChatStreamUsageEstimatesDeliveredOutput(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:    relayconstant.RelayModeChatCompletions,
		PromptTokens: 9000, UsagePromptTokens: 12, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	usage, observed := observedChatStreamUsage(info, []string{
		`{"choices":[{"delta":{"content":"billable output"}}]}`,
	}, nil)
	if !observed || usage.PromptTokens != 12 || usage.CompletionTokens <= 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("observed output was not estimated: observed=%v usage=%+v", observed, usage)
	}
}

func TestObservedChatStreamUsagePrefersProviderCounters(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	usage, observed := observedChatStreamUsage(info, []string{
		`{"choices":[],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}`,
	}, &dto.Usage{})
	if !observed || usage.PromptTokens != 8 || usage.CompletionTokens != 3 || usage.TotalTokens != 11 {
		t.Fatalf("provider usage was not retained: observed=%v usage=%+v", observed, usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceProvider {
		t.Fatalf("partial stream provider provenance lost: %+v", got)
	}
}

func TestCanceledStreamSettlementRequiresBillableEvidence(t *testing.T) {
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}
	if usage, observed := observedChatStreamUsage(info, []string{`{"choices":[{"delta":{}}]}`}, nil); observed || usage.TotalTokens != 0 {
		t.Fatalf("metadata-only chunk must not be billed: observed=%v usage=%+v", observed, usage)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	markCanceledStreamForSettlement(c, false)
	if common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("metadata-only cancellation unexpectedly requested settlement")
	}
	markCanceledStreamForSettlement(c, true)
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("observed billable output must request settlement")
	}
}

func TestObservedResponsesStreamUsageEstimatesTextAndTools(t *testing.T) {
	info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 7, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
	usage := &dto.Usage{}
	if !observedResponsesStreamUsage(info, usage, "partial", 1) {
		t.Fatal("Responses deltas should be billable evidence")
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens < 8 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("Responses cancellation usage was not estimated: %+v", usage)
	}
}

func TestChatStreamUsageAccumulatorCountsIncrementally(t *testing.T) {
	info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 5, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
	accumulator := newChatStreamUsageAccumulator(info)
	bridge := openai_responses.NewStreamEmitter("resp_usage", info.UpstreamModelName, nil)
	toolIndex := 0
	first := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Index: 0,
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
			Content: common.GetPointer("partial text"),
			ToolCalls: []dto.ToolCallResponse{{
				Index:    &toolIndex,
				Function: dto.FunctionResponse{Name: "lookup", Arguments: `{"x":`},
			}},
		},
	}}}
	second := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Index: 0,
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{
			Index:    &toolIndex,
			Function: dto.FunctionResponse{Arguments: `1}`},
		}}},
	}}}
	for _, chunk := range []*dto.ChatCompletionsStreamResponse{first, second} {
		accumulator.Observe(chunk)
		if _, err := bridge.Emit((&openai_chat.StreamDecoder{}).Decode(chunk)...); err != nil {
			t.Fatal(err)
		}
	}
	usage, observed := accumulator.Usage(info, bridge)
	if !observed || usage.PromptTokens != 5 || usage.CompletionTokens <= 7 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("incremental Chat usage was not retained: observed=%v usage=%+v", observed, usage)
	}
}

func TestChatStreamUsageAccumulatorIsIndependentOfDeltaBoundaries(t *testing.T) {
	info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 5, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
	measure := func(fragments []string) *dto.Usage {
		accumulator := newChatStreamUsageAccumulator(info)
		bridge := openai_responses.NewStreamEmitter("resp_usage", info.UpstreamModelName, nil)
		for _, fragment := range fragments {
			chunk := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: common.GetPointer(fragment)},
			}}}
			accumulator.Observe(chunk)
			if _, err := bridge.Emit((&openai_chat.StreamDecoder{}).Decode(chunk)...); err != nil {
				t.Fatal(err)
			}
		}
		usage, observed := accumulator.Usage(info, bridge)
		if !observed {
			t.Fatal("logical output must be observed")
		}
		return usage
	}
	logical := "indivisible output"
	single := measure([]string{logical})
	fragmented := make([]string, 0, len(logical))
	for _, char := range logical {
		fragmented = append(fragmented, string(char))
	}
	perCharacter := measure(fragmented)
	want := service.CountTextToken(logical, info.UpstreamModelName)
	if single.CompletionTokens != want || perCharacter.CompletionTokens != want {
		t.Fatalf("usage changed with SSE boundaries: single=%+v fragmented=%+v want=%d", single, perCharacter, want)
	}
}

func TestBoundedStreamChunksNeverExceedsLimit(t *testing.T) {
	chunks := newBoundedStreamChunks(8)
	chunks.Add("1234")
	chunks.Add("5678")
	chunks.Add("overflow")
	if got := chunks.Values(); len(got) != 2 || got[0] != "1234" || got[1] != "5678" {
		t.Fatalf("unexpected bounded chunks: %#v", got)
	}
	metadata := chunks.Metadata()
	if metadata["response_chunks_truncated"] != true || metadata["response_chunks_retained_bytes"] != 8 {
		t.Fatalf("missing truncation metadata: %#v", metadata)
	}
}

func TestChatStreamUsageAccumulatorRetainsPromptForCompletedEmptyOutput(t *testing.T) {
	info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 6, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
	accumulator := newChatStreamUsageAccumulator(info)
	bridge := openai_responses.NewStreamEmitter("resp_empty", info.UpstreamModelName, nil)
	finish := "stop"
	chunk := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Index:        0,
		FinishReason: &finish,
	}}}
	accumulator.Observe(chunk)
	if _, err := bridge.Emit((&openai_chat.StreamDecoder{}).Decode(chunk)...); err != nil {
		t.Fatal(err)
	}
	usage, observed := accumulator.Usage(info, bridge)
	if !observed || usage.PromptTokens != 6 || usage.CompletionTokens != 0 || usage.TotalTokens != 6 {
		t.Fatalf("completed empty output must retain prompt usage: observed=%v usage=%+v", observed, usage)
	}
}
