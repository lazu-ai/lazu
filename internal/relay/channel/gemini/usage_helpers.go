package gemini

import (
	"mime"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
)

func estimateGeminiInlineAudioTokens(inlineData *dto.GeminiInlineData) (int, error) {
	if inlineData == nil || inlineData.Data == "" {
		return 0, nil
	}
	mimeType := strings.ToLower(strings.TrimSpace(inlineData.MimeType))
	sampleRate := 0
	if mediaType, parameters, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = strings.ToLower(mediaType)
		if rawRate := strings.TrimSpace(parameters["rate"]); rawRate != "" {
			if parsedRate, parseErr := strconv.Atoi(rawRate); parseErr == nil && parsedRate > 0 {
				sampleRate = parsedRate
			}
		}
	} else if parameterIndex := strings.IndexByte(mimeType, ';'); parameterIndex >= 0 {
		mimeType = strings.TrimSpace(mimeType[:parameterIndex])
	}
	if !strings.HasPrefix(mimeType, "audio/") {
		return 0, nil
	}

	format := strings.TrimPrefix(mimeType, "audio/")
	switch format {
	case "l16", "pcm", "pcm16", "raw":
		format = "pcm16"
	case "pcmu", "mulaw", "g711_ulaw":
		format = "g711_ulaw"
	case "pcma", "alaw", "g711_alaw":
		format = "g711_alaw"
	}
	if format == "pcm16" && sampleRate > 0 {
		return service.CountPCM16AudioTokenOutput(inlineData.Data, sampleRate)
	}
	return service.CountAudioTokenOutput(inlineData.Data, format)
}

func applyGeminiStreamFallbackUsage(
	usage *dto.Usage,
	responseText string,
	modelName string,
	fallbackPromptTokens int,
	imageCount int,
	audioTokens int,
	hasProviderUsage bool,
) {
	if usage == nil {
		return
	}
	if usage.CompletionTokens == 0 {
		textTokens := 0
		if responseText != "" {
			textTokens = service.CountTextToken(responseText, modelName)
		}
		imageTokens := imageCount * 258
		usage.CompletionTokens = textTokens + imageTokens + audioTokens
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		usage.CompletionTokenDetails.ImageTokens = imageTokens
		usage.CompletionTokenDetails.AudioTokens = audioTokens
		if usage.CompletionTokens > 0 && usage.PromptTokens == 0 {
			usage.PromptTokens = fallbackPromptTokens
			usage.InputTokenSource = dto.UsageSourceUnavailable
			if fallbackPromptTokens > 0 {
				usage.InputTokenSource = dto.UsageSourceLocalEstimate
			}
		}
	}
	if usage.CompletionTokens == 0 && !hasProviderUsage && usage.PromptTokens == 0 {
		*usage = dto.Usage{}
	}
	geminicodec.ReconcileTotal(usage)
}

func buildGeminiUsageWithLocalFallback(c *gin.Context, info *relaycommon.RelayInfo, metadata dto.GeminiUsageMetadata) dto.Usage {
	fallback := 0
	if metadata.PromptTokenCount+metadata.ToolUsePromptTokenCount <= 0 {
		fallback = service.PromptTokensForUsage(c, info)
	}
	return geminicodec.UsageOf(metadata, fallback)
}
