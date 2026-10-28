package common_handler

import (
	"io"
	"net/http"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
)

// EmbeddingHandler serves an OpenAI-shaped /v1/embeddings reply from an
// OpenAI-compatible upstream. The vectors pass through untouched; only
// `usage` is rewritten, to the embedding shape (prompt and total tokens, plus
// the gateway's cost), with the upstream's count when it reports one and the
// local estimate when it does not.
//
// Until this handler existed the chat handler took these replies. It
// re-marshalled the body as a chat completion when the channel's force_format
// was on, a shape with no `data`, so the vectors were dropped; and it always
// rewrote `usage` with the chat counters (completion_tokens and both
// *_details objects).
func EmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		service.CaptureIncompleteModelResponse(c, responseBody, resp.StatusCode)
		return nil, service.UpstreamReadError(c, err)
	}
	var reply dto.SimpleResponse
	if err := common.Unmarshal(responseBody, &reply); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := reply.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	usage := reply.Usage
	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.PromptTokens == 0 && usage.TotalTokens > 0 {
		usage.PromptTokens = usage.TotalTokens
	}
	usage.MarkProviderUsage()
	if usage.PromptTokens == 0 {
		service.ApplyLocalPromptUsage(c, info, &usage)
	}
	// An embedding has no completion; an upstream that reports one anyway
	// (a few count the vectors) must not be billed for output.
	usage.CompletionTokens = 0
	usage.OutputTokens = 0
	usage.TotalTokens = usage.PromptTokens
	if usage.OutputTokenSource == "" {
		usage.OutputTokenSource = usage.InputTokenSource
	}

	preparedQuote := service.BuildTextBillingQuote(c, info, &usage, "")
	service.AnnotateUsageCost(c, &usage, preparedQuote.Quote)

	responseBody, err = sjson.SetBytes(responseBody, "usage", dto.EmbeddingUsage{
		PromptTokens: usage.PromptTokens,
		TotalTokens:  usage.TotalTokens,
		Cost:         usage.Cost,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	service.AttachModelLogPayload(c, &service.ModelLogPayload{
		ResponseBody: append([]byte(nil), responseBody...),
		Usage:        usage,
		StatusCode:   resp.StatusCode,
	})
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return &usage, nil
}
