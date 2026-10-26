package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference?hl=zh-cn#blob
var geminiSupportedMimeTypes = map[string]bool{
	"application/pdf": true,
	"audio/mpeg":      true,
	"audio/mp3":       true,
	"audio/wav":       true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"text/plain":      true,
	"video/mov":       true,
	"video/mpeg":      true,
	"video/mp4":       true,
	"video/mpg":       true,
	"video/avi":       true,
	"video/wmv":       true,
	"video/mpegps":    true,
	"video/flv":       true,
}

// Helper function to get a list of supported MIME types for error messages
func getSupportedMimeTypesList() []string {
	keys := make([]string, 0, len(geminiSupportedMimeTypes))
	for k := range geminiSupportedMimeTypes {
		keys = append(keys, k)
	}
	return keys
}

// recordGeminiFinishReason keeps how a Gemini reply ended for the outcome row,
// in the chat vocabulary every other channel reports.
func recordGeminiFinishReason(info *relaycommon.RelayInfo, response *dto.GeminiChatResponse) {
	if info == nil || response == nil {
		return
	}
	for _, candidate := range response.Candidates {
		if candidate.FinishReason == nil || *candidate.FinishReason == "" {
			continue
		}
		switch *candidate.FinishReason {
		case "STOP":
			info.FinishReason = constant.FinishReasonStop
		case "MAX_TOKENS":
			info.FinishReason = constant.FinishReasonLength
		case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
			info.FinishReason = constant.FinishReasonContentFilter
		default:
			info.FinishReason = strings.ToLower(*candidate.FinishReason)
		}
		return
	}
}

func GeminiEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	var geminiResponse dto.GeminiBatchEmbeddingResponse
	if jsonErr := common.Unmarshal(responseBody, &geminiResponse); jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// convert to openai format response
	openAIResponse := dto.OpenAIEmbeddingResponse{
		Object: "list",
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(geminiResponse.Embeddings)),
		Model:  info.UpstreamModelName,
	}

	for i, embedding := range geminiResponse.Embeddings {
		openAIResponse.Data = append(openAIResponse.Data, dto.OpenAIEmbeddingResponseItem{
			Object:    "embedding",
			Embedding: embedding.Values,
			Index:     i,
		})
	}

	// calculate usage
	// https://ai.google.dev/gemini-api/docs/pricing?hl=zh-cn#text-embedding-004
	// Google has not yet clarified how embedding models will be billed
	// refer to openai billing method to use input tokens billing
	// https://platform.openai.com/docs/guides/embeddings#what-are-embeddings
	promptTokens := service.PromptTokensForUsage(c, info)
	usage := &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: 0,
		TotalTokens:      promptTokens,
	}
	service.ApplyLocalPromptUsage(c, info, usage)
	openAIResponse.Usage = *usage

	jsonResponse, jsonErr := common.Marshal(openAIResponse)
	if jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return usage, nil
}

func GeminiImageContentHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var geminiResponse dto.GeminiChatResponse
	if jsonErr := common.Unmarshal(responseBody, &geminiResponse); jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	openAIResponse := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0),
	}

	for _, candidate := range geminiResponse.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			if strings.HasPrefix(part.InlineData.MimeType, "image/") {
				openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{
					B64Json: part.InlineData.Data,
				})
			}
		}
	}

	if len(openAIResponse.Data) == 0 {
		return nil, types.NewOpenAIError(errors.New("no images generated"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	jsonResponse, jsonErr := json.Marshal(openAIResponse)
	if jsonErr != nil {
		return nil, types.NewError(jsonErr, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	usage := &dto.Usage{}
	hasProviderUsage := geminicodec.HasUsage(geminiResponse.UsageMetadata)
	if hasProviderUsage {
		mappedUsage := buildGeminiUsageWithLocalFallback(c, info, geminiResponse.UsageMetadata)
		*usage = mappedUsage
	}

	if usage.PromptTokens == 0 {
		service.ApplyLocalPromptUsage(c, info, usage)
	}
	if usage.CompletionTokens == 0 {
		const imageTokens = 258
		usage.CompletionTokens = imageTokens * len(openAIResponse.Data)
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		usage.CompletionTokenDetails.ImageTokens = usage.CompletionTokens
	}
	geminicodec.ReconcileTotal(usage)

	return usage, nil
}

func GeminiImageHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	var geminiResponse dto.GeminiImageResponse
	if jsonErr := common.Unmarshal(responseBody, &geminiResponse); jsonErr != nil {
		return nil, types.NewOpenAIError(jsonErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if len(geminiResponse.Predictions) == 0 {
		return nil, types.NewOpenAIError(errors.New("no images generated"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// convert to openai format response
	openAIResponse := dto.ImageResponse{
		Created: common.GetTimestamp(),
		Data:    make([]dto.ImageData, 0, len(geminiResponse.Predictions)),
	}

	for _, prediction := range geminiResponse.Predictions {
		if prediction.RaiFilteredReason != "" {
			continue // skip filtered image
		}
		openAIResponse.Data = append(openAIResponse.Data, dto.ImageData{
			B64Json: prediction.BytesBase64Encoded,
		})
	}

	jsonResponse, jsonErr := json.Marshal(openAIResponse)
	if jsonErr != nil {
		return nil, types.NewError(jsonErr, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)

	// https://github.com/google-gemini/cookbook/blob/719a27d752aac33f39de18a8d3cb42a70874917e/quickstarts/Counting_Tokens.ipynb
	// each image has fixed 258 tokens
	const imageTokens = 258
	generatedImages := len(openAIResponse.Data)

	usage := &dto.Usage{
		PromptTokens:     imageTokens * generatedImages, // each generated image has fixed 258 tokens
		InputTokenSource: dto.UsageSourceLocalEstimate,
		CompletionTokens: 0, // image generation does not calculate completion tokens
		TotalTokens:      imageTokens * generatedImages,
	}
	usage.PromptTokensDetails.ImageTokens = usage.PromptTokens

	return usage, nil
}
