package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

// BridgesToGemini says whether the attempt serves another protocol's client
// from this Gemini channel through the codec.
func BridgesToGemini(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ProtocolPlan != nil && !info.ProtocolPlan.Native && info.ProtocolRequest != nil &&
		info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolGemini
}

// EncodeBridgedRequest is the generateContent request for a bridged attempt:
// the IR encoded by the Gemini codec, then what is the channel's to decide
// (AGENTS.md, Protocol Layer): media fetched inline, the image count limit,
// response modalities, thinking for the model, and safety settings.
func EncodeBridgedRequest(c *gin.Context, info *relaycommon.RelayInfo, request *textgen.Request) (*dto.GeminiChatRequest, error) {
	out, err := geminicodec.Encode(request, geminicodec.EncodeOptions{
		InlineImage: func(url string) (string, string, error) { return inlineMediaFromURL(c, url) },
	})
	if err != nil {
		return nil, err
	}
	if err := checkImageCount(out); err != nil {
		return nil, err
	}
	if model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
		out.GenerationConfig.ResponseModalities = []string{"TEXT", "IMAGE"}
	}
	thinking, err := bridgedThinking(request, info)
	if err != nil {
		return nil, err
	}
	out.GenerationConfig.ThinkingConfig = thinking
	out.SafetySettings = safetySettings()
	return out, nil
}

// inlineMediaFromURL fetches media named by an http(s) URL for Gemini,
// which takes media inline, and refuses a type Gemini does not read.
func inlineMediaFromURL(c *gin.Context, url string) (string, string, error) {
	file, err := service.GetFileBase64FromUrl(c, url, "formatting image for Gemini")
	if err != nil {
		return "", "", fmt.Errorf("get file base64 from url '%s' failed: %w", url, err)
	}
	if _, ok := geminiSupportedMimeTypes[strings.ToLower(file.MimeType)]; !ok {
		return "", "", fmt.Errorf("mime type is not supported by Gemini: '%s', url: '%s', supported types are: %v", file.MimeType, url, getSupportedMimeTypesList())
	}
	return file.MimeType, file.Base64Data, nil
}

// checkImageCount applies GEMINI_VISION_MAX_IMAGE_NUM per message (-1: no
// limit).
func checkImageCount(request *dto.GeminiChatRequest) error {
	if constant.GeminiVisionMaxImageNum < 0 {
		return nil
	}
	for _, content := range request.Contents {
		images := 0
		for _, part := range content.Parts {
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MimeType, "image/") {
				images++
			}
		}
		if images > constant.GeminiVisionMaxImageNum {
			return fmt.Errorf("too many images in the message, max allowed is %d", constant.GeminiVisionMaxImageNum)
		}
	}
	return nil
}

func safetySettings() []dto.GeminiChatSafetySettings {
	settings := make([]dto.GeminiChatSafetySettings, 0, len(SafetySettingList))
	for _, category := range SafetySettingList {
		settings = append(settings, dto.GeminiChatSafetySettings{Category: category, Threshold: model_setting.GetGeminiSafetySetting(category)})
	}
	return settings
}

// bridgedThinking is the thinking config for a bridged request. A client's
// own Gemini config in extra_body.google.thinking_config is taken as before.
// Otherwise the request's reasoning is mapped: an effort the catalog lists
// for the model becomes Gemini's thinking level, a budget its thinking
// budget, and the thoughts are returned so the client sees the reasoning.
// With neither, the model's default stands.
func bridgedThinking(request *textgen.Request, info *relaycommon.RelayInfo) (*dto.GeminiThinkingConfig, error) {
	if config, ok, err := extraBodyThinking(request); err != nil || ok {
		return config, err
	}
	effort := strings.ToLower(strings.TrimSpace(request.Reasoning.Effort))
	efforts := model.ReasoningPolicy(info.UpstreamModelName, info.OriginModelName).Efforts
	switch {
	case effort != "" && slices.Contains(efforts, effort):
		return &dto.GeminiThinkingConfig{ThinkingLevel: effort, IncludeThoughts: true}, nil
	case request.Reasoning.Budget.Set:
		budget := request.Reasoning.Budget.Value
		return &dto.GeminiThinkingConfig{ThinkingBudget: &budget, IncludeThoughts: budget > 0}, nil
	}
	return nil, nil
}

// extraBodyThinking reads extra_body.google.thinking_config, with the
// checks the Chat conversion made of it.
func extraBodyThinking(request *textgen.Request) (*dto.GeminiThinkingConfig, bool, error) {
	raw, ok := request.Raw.UnknownFields["extra_body"]
	if !ok || len(raw) == 0 {
		return nil, false, nil
	}
	var extraBody map[string]any
	if err := json.Unmarshal(raw, &extraBody); err != nil {
		return nil, false, fmt.Errorf("invalid extra body: %w", err)
	}
	google, ok := extraBody["google"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	if _, wrong := google["thinkingConfig"]; wrong {
		return nil, false, errors.New("extra_body.google.thinkingConfig is not supported, use extra_body.google.thinking_config instead")
	}
	thinking, ok := google["thinking_config"].(map[string]any)
	if !ok {
		return nil, false, nil
	}
	if _, wrong := thinking["thinkingBudget"]; wrong {
		return nil, false, errors.New("extra_body.google.thinking_config.thinkingBudget is not supported, use extra_body.google.thinking_config.thinking_budget instead")
	}
	if _, wrong := thinking["thinkingLevel"]; wrong {
		return nil, false, errors.New("extra_body.google.thinking_config.thinkingLevel is not supported, use extra_body.google.thinking_config.thinking_level instead")
	}
	config := &dto.GeminiThinkingConfig{IncludeThoughts: true}
	if budget, ok := thinking["thinking_budget"].(float64); ok {
		config.ThinkingBudget = common.GetPointer(int(budget))
	}
	if value, exists := thinking["thinking_level"]; exists {
		level, ok := value.(string)
		if !ok {
			return nil, false, errors.New("extra_body.google.thinking_config.thinking_level must be a string")
		}
		level = strings.ToLower(strings.TrimSpace(level))
		switch level {
		case "minimal", "low", "medium", "high":
			config.ThinkingLevel = level
		default:
			return nil, false, errors.New("extra_body.google.thinking_config.thinking_level must be one of minimal, low, medium, or high")
		}
	}
	return config, true, nil
}

// bridgedStreamHandler serves a streamed Gemini reply to a bridged client
// through the hub's runner and the writer for the client's protocol.
func bridgedStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	decoder := &geminicodec.StreamDecoder{}
	media := &mediaOutput{}
	return channel.RunHubStream(c, resp, info, channel.HubStream{
		Decode: decoder.Decode,
		Observe: func(c *gin.Context, _ *relaycommon.RelayInfo, data string, events []textgen.Event) {
			media.observe(c, data, events)
		},
		Settle: media.settle,
	}, channel.HubClientFor(c, info))
}

// mediaOutput keeps what a Gemini reply's usage is estimated from when the
// upstream reports no output tokens and the reply carries inline media: the
// codec writes media to the client as data URLs, which are not text tokens.
// The rules are the native handler's (applyGeminiStreamFallbackUsage).
type mediaOutput struct {
	text        strings.Builder
	images      int
	audioTokens int
	seen        bool
}

// observe keeps a stream chunk: the text from its decoded events, and a
// chunk with media is parsed again for it (the events hold media as a data
// URL).
func (m *mediaOutput) observe(c *gin.Context, data string, events []textgen.Event) {
	if !strings.Contains(data, "inlineData") {
		for _, event := range events {
			if event.Kind == textgen.EventText || event.Kind == textgen.EventReasoning {
				m.text.WriteString(event.Text)
			}
		}
		return
	}
	var response dto.GeminiChatResponse
	if common.UnmarshalJsonStr(data, &response) != nil {
		return
	}
	m.add(c, &response)
}

func (m *mediaOutput) add(c *gin.Context, response *dto.GeminiChatResponse) {
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				mimeType := strings.ToLower(strings.TrimSpace(part.InlineData.MimeType))
				switch {
				case strings.HasPrefix(mimeType, "image/"):
					m.images++
					m.seen = true
				case strings.HasPrefix(mimeType, "audio/"):
					m.seen = true
					tokens, err := estimateGeminiInlineAudioTokens(part.InlineData)
					if err != nil {
						logger.LogError(c, "failed to estimate Gemini inline audio tokens: "+err.Error())
						continue
					}
					m.audioTokens += tokens
				}
			}
			m.text.WriteString(part.Text)
		}
	}
}

func (m *mediaOutput) settle(c *gin.Context, info *relaycommon.RelayInfo, result textgen.Result) *dto.Usage {
	if !m.seen || (result.Usage != nil && result.Usage.CompletionTokens > 0) {
		return channel.SettleHubUsage(c, info, result)
	}
	usage := result.Usage
	if usage == nil {
		usage = &dto.Usage{}
	}
	fallbackPromptTokens := 0
	if usage.PromptTokens == 0 {
		fallbackPromptTokens = service.PromptTokensForUsage(c, info)
	}
	applyGeminiStreamFallbackUsage(usage, m.text.String(), info.UpstreamModelName, fallbackPromptTokens, m.images, m.audioTokens, result.Usage != nil)
	return usage
}

// bridgedHandler serves a whole Gemini reply to a bridged client in the
// client's protocol.
func bridgedHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	service.CloseResponseBodyGracefully(resp)
	events, err := geminicodec.DecodeResponse(body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Model != "" {
		info.UpstreamModelName = result.Model
	}
	if result.StopSeen {
		info.FinishReason = openai_chat.FinishReasonOf(result.Stop)
	}
	if result.Error != nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", result.Error.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	media := &mediaOutput{}
	if strings.Contains(string(body), "inlineData") {
		var response dto.GeminiChatResponse
		if common.Unmarshal(body, &response) == nil {
			media.add(c, &response)
		}
	}
	usage := media.settle(c, info, result)
	result.Usage = usage
	var rendered any
	switch info.ProtocolPlan.ClientProtocol.Family {
	case textgen.ProtocolAnthropic:
		rendered, err = anthropic_messages.Render(result, info.UpstreamModelName)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case textgen.ProtocolOpenAIResponses:
		rendered = openai_responses.ChatResponseToResponses(openai_chat.Render(result, info.UpstreamModelName), info.UpstreamModelName, info.ProtocolRequest)
	default:
		rendered = openai_chat.Render(result, info.UpstreamModelName)
	}
	data, err := common.Marshal(rendered)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.IOCopyBytesGracefully(c, resp, data)
	return usage, nil
}
