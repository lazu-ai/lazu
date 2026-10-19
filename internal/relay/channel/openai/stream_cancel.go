package openai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

const maxBridgedStreamLogBytes = 256 << 10

type chatStreamBillableOutput interface {
	VisitBillableOutput(func(string)) int
}

type chatStreamUsageAccumulator struct {
	model         string
	promptTokens  int
	terminal      bool
	providerUsage *dto.Usage
}

func newChatStreamUsageAccumulator(info *relaycommon.RelayInfo) *chatStreamUsageAccumulator {
	accumulator := &chatStreamUsageAccumulator{}
	if info != nil {
		accumulator.model = info.UpstreamModelName
		accumulator.promptTokens = info.UsagePromptTokens
	}
	return accumulator
}

func (a *chatStreamUsageAccumulator) Observe(chunk *dto.ChatCompletionsStreamResponse) {
	if a == nil || chunk == nil {
		return
	}
	if service.ValidUsage(chunk.Usage) {
		usage := *chunk.Usage
		usage.MarkProviderUsage()
		a.providerUsage = &usage
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			a.terminal = true
		}
	}
}

func (a *chatStreamUsageAccumulator) Usage(info *relaycommon.RelayInfo, output chatStreamBillableOutput) (*dto.Usage, bool) {
	if a == nil {
		return &dto.Usage{}, false
	}
	if service.ValidUsage(a.providerUsage) {
		usage := *a.providerUsage
		applyUsagePostProcessing(info, &usage, nil)
		return &usage, true
	}
	completionTokens := 0
	toolCount := 0
	if output != nil {
		toolCount = output.VisitBillableOutput(func(fragment string) {
			completionTokens += service.CountTextToken(fragment, a.model)
		})
	}
	if completionTokens == 0 && toolCount == 0 && !a.terminal {
		return &dto.Usage{}, false
	}
	completionTokens += toolCount * 7
	promptTokens := a.promptTokens
	if info != nil {
		promptTokens = info.UsagePromptTokens
	}
	usage := &dto.Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, InputTokenSource: dto.UsageSourceLocalEstimate, OutputTokenSource: dto.UsageSourceLocalEstimate}
	if info == nil || !info.UsagePromptTokensAvailable {
		usage.InputTokenSource = dto.UsageSourceUnavailable
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	applyUsagePostProcessing(info, usage, nil)
	return usage, true
}

type boundedStreamChunks struct {
	limit     int
	size      int
	received  int
	chunks    []string
	truncated bool
}

func newBoundedStreamChunks(limit int) *boundedStreamChunks {
	if limit <= 0 {
		limit = maxBridgedStreamLogBytes
	}
	return &boundedStreamChunks{limit: limit}
}

func (b *boundedStreamChunks) Add(chunk string) {
	if b == nil || chunk == "" {
		return
	}
	b.received += len(chunk)
	if b.size+len(chunk) > b.limit {
		b.truncated = true
		return
	}
	b.chunks = append(b.chunks, chunk)
	b.size += len(chunk)
}

func (b *boundedStreamChunks) Values() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.chunks...)
}

func (b *boundedStreamChunks) Metadata() map[string]any {
	if b == nil || !b.truncated {
		return nil
	}
	return map[string]any{"response_chunks_truncated": true, "response_chunks_retained_bytes": b.size, "response_bytes_received": b.received}
}

// streamClientCanceledError turns a downstream disconnect into an explicit
// client-fault outcome. Stream scanners stop when the inbound request context
// is canceled; without this boundary the protocol emitters can misclassify the
// resulting truncated stream as a provider or conversion failure.
func streamClientCanceledError(c *gin.Context) *types.LazuError {
	if c == nil || c.Request == nil || c.Request.Context().Err() != context.Canceled {
		return nil
	}
	return types.NewError(
		context.Canceled,
		types.ErrorCodeClientCanceled,
		types.ErrOptionWithSkipRetry(),
		types.ErrOptionWithNoRecordErrorLog(),
		types.ErrOptionWithHideErrMsg("request canceled by client"),
	)
}

// observedChatStreamUsage returns only usage supported by provider counters or
// by content/tool deltas already observed by the gateway. A metadata-only
// stream is not enough evidence to charge a canceled client.
func observedChatStreamUsage(info *relaycommon.RelayInfo, streamItems []string, current *dto.Usage) (*dto.Usage, bool) {
	usage := &dto.Usage{}
	if current != nil {
		*usage = *current
	}
	for _, item := range streamItems {
		var chunk dto.ChatCompletionsStreamResponse
		if json.Unmarshal(common.StringToByteSlice(item), &chunk) == nil && service.ValidUsage(chunk.Usage) {
			*usage = *chunk.Usage
			usage.MarkProviderUsage()
		}
	}
	if service.ValidUsage(usage) {
		applyUsagePostProcessing(info, usage, nil)
		return usage, true
	}

	var output strings.Builder
	var toolCount int
	relayMode := 0
	if info != nil {
		relayMode = info.RelayMode
		if relayMode == relayconstant.RelayModeResponses {
			relayMode = relayconstant.RelayModeChatCompletions
		}
	}
	if err := processTokens(relayMode, streamItems, &output, &toolCount); err != nil || (output.Len() == 0 && toolCount == 0) {
		return usage, false
	}
	promptTokens := 0
	modelName := ""
	if info != nil {
		promptTokens = info.UsagePromptTokens
		modelName = info.UpstreamModelName
	}
	usage = service.ResponseText2Usage(output.String(), modelName, promptTokens)
	usage.CompletionTokens += toolCount * 7
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	applyUsagePostProcessing(info, usage, nil)
	return usage, true
}

func observedResponsesStreamUsage(info *relaycommon.RelayInfo, usage *dto.Usage, output string, functionCallCount int) bool {
	if usage == nil {
		return false
	}
	if service.ValidUsage(usage) {
		applyUsagePostProcessing(info, usage, nil)
		return true
	}
	if output == "" && functionCallCount == 0 {
		return false
	}
	modelName := ""
	promptTokens := 0
	if info != nil {
		modelName = info.UpstreamModelName
		promptTokens = info.UsagePromptTokens
	}
	usage.PromptTokens = promptTokens
	usage.InputTokenSource = dto.UsageSourceUnavailable
	if info != nil && info.UsagePromptTokensAvailable {
		usage.InputTokenSource = dto.UsageSourceLocalEstimate
	}
	usage.OutputTokenSource = dto.UsageSourceLocalEstimate
	usage.CompletionTokens = service.CountTextToken(output, modelName) + functionCallCount*7
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	applyUsagePostProcessing(info, usage, nil)
	return true
}

func markCanceledStreamForSettlement(c *gin.Context, observed bool) {
	if c != nil && observed {
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
	}
}

// Unlike observedChatStreamUsage (legacy utilities), the native handler keeps
// only billable text and incremental usage, never all raw SSE events.
func observedAccumulatedChatStreamUsage(info *relaycommon.RelayInfo, output string, toolCount int, current *dto.Usage) (*dto.Usage, bool) {
	if service.ValidUsage(current) {
		applyUsagePostProcessing(info, current, nil)
		return current, true
	}
	if output == "" && toolCount == 0 {
		return &dto.Usage{}, false
	}
	usage := service.ResponseText2Usage(output, info.UpstreamModelName, info.UsagePromptTokens)
	usage.CompletionTokens += toolCount * 7
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	applyUsagePostProcessing(info, usage, nil)
	return usage, true
}
