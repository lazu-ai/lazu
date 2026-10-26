package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/tiktoken-go/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

// tokenEncoderMap won't grow after initialization
var defaultTokenEncoder tokenizer.Codec

// tokenEncoderMap is used to store token encoders for different models
var tokenEncoderMap = make(map[string]tokenizer.Codec)

// tokenEncoderMutex protects tokenEncoderMap for concurrent access
var tokenEncoderMutex sync.RWMutex

func InitTokenEncoders() {
	common.SysLog("initializing token encoders")
	tokenEncoderMutex.Lock()
	if defaultTokenEncoder == nil {
		defaultTokenEncoder = codec.NewCl100kBase()
	}
	tokenEncoderMutex.Unlock()
	common.SysLog("token encoders initialized")
}

func getTokenEncoder(model string) tokenizer.Codec {
	// First, try to get the encoder from cache with read lock
	tokenEncoderMutex.RLock()
	if encoder, exists := tokenEncoderMap[model]; exists {
		tokenEncoderMutex.RUnlock()
		return encoder
	}
	tokenEncoderMutex.RUnlock()

	// If not in cache, create new encoder with write lock
	tokenEncoderMutex.Lock()
	defer tokenEncoderMutex.Unlock()

	// Double-check if another goroutine already created the encoder
	if encoder, exists := tokenEncoderMap[model]; exists {
		return encoder
	}

	// The default is also initialized lazily for detached background workers
	// and focused tests that do not run the application startup sequence.
	if defaultTokenEncoder == nil {
		defaultTokenEncoder = codec.NewCl100kBase()
	}
	// Create new encoder
	modelCodec, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		// Cache the default encoder for this model to avoid repeated failures
		tokenEncoderMap[model] = defaultTokenEncoder
		return defaultTokenEncoder
	}

	// Cache the new encoder
	tokenEncoderMap[model] = modelCodec
	return modelCodec
}

func getTokenNum(tokenEncoder tokenizer.Codec, text string) int {
	if text == "" {
		return 0
	}
	tkm, _ := tokenEncoder.Count(text)
	return tkm
}

func CountRequestToken(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	if !constant.GetMediaToken {
		return 0, nil
	}
	if !constant.GetMediaTokenNotStream && !info.IsStream {
		return 0, nil
	}
	return countRequestTokens(c, meta, info)
}

// The shadow worker calls this complete estimator independently of the legacy
// billing fallback switches, using only already captured text metadata.
func countRequestTokens(c *gin.Context, meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	if info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return 0, nil
	}
	if meta == nil {
		return 0, errors.New("token count meta is nil")
	}

	model := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	if model == "" {
		model = info.OriginModelName
	}
	tkm := 0

	if meta.TokenType == types.TokenTypeTextNumber {
		tkm += utf8.RuneCountInString(meta.CombineText)
	} else {
		tkm += CountTextToken(meta.CombineText, model)
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		tkm += meta.ToolsCount * 8
		tkm += meta.MessagesCount * 3 // 每条消息的格式化token数量
		tkm += meta.NameCount * 3
		tkm += 3
	}

	for i, file := range meta.Files {
		if file == nil {
			return 0, fmt.Errorf("missing media at index %d", i)
		}
		switch file.FileType {
		case types.FileTypeImage:
			token, err := getImageToken(file, model, info.IsStream)
			if err != nil {
				return 0, fmt.Errorf("image token count unavailable at media index %d: %w", i, err)
			}
			tkm += token
		default:
			return 0, fmt.Errorf("local token rule unavailable at media index %d", i)
		}
	}

	common.SetContextKey(c, constant.ContextKeyPromptTokens, tkm)
	return tkm, nil
}

const (
	defaultImageTPMReserve = 2048
	k26ImageTPMReserve     = 4096
)

// EstimateRequestTPMTokens returns a deliberately cheap, structured estimate
// for admission control and pre-consumption. It never fetches media or decodes image pixels:
// Base64/URLs are already separated into meta.Files, so each image contributes
// a model-scoped estimate from bounded inline headers or a reserve for unknown sizes. Upstream
// usage remains authoritative for settlement and reporting.
func EstimateRequestTPMTokens(meta *types.TokenCountMeta, info *relaycommon.RelayInfo) (int, error) {
	if meta == nil {
		return 0, errors.New("token count meta is nil")
	}

	tokens := estimateTextBytes(meta.CombineText)
	if meta.TokenType == types.TokenTypeTextNumber {
		tokens = utf8.RuneCountInString(meta.CombineText)
	}
	if info != nil && info.RelayFormat == types.RelayFormatOpenAI {
		tokens += meta.ToolsCount * 8
		tokens += meta.MessagesCount * 3
		tokens += meta.NameCount * 3
		tokens += 3
	}

	model := ""
	if info != nil {
		model = info.OriginModelName
	}
	for _, file := range meta.Files {
		if file == nil {
			continue
		}
		switch file.FileType {
		case types.FileTypeImage:
			tokens += imageAdmissionTokens(file, model)
		case types.FileTypeAudio:
			tokens += 256
		case types.FileTypeVideo:
			tokens += 4096 * 2
		case types.FileTypeFile:
			tokens += 4096
		default:
			// Unknown media is still charged a bounded reserve so it cannot
			// bypass TPM merely because its type was not recognized locally.
			tokens += 4096
		}
	}
	return tokens, nil
}

// PromptTokensForUsage lazily estimates input usage from the actual request.
// Admission is never a usage fallback. Disabled/unavailable local counting is
// represented by zero and UsagePromptTokensAvailable=false, not a reservation.
func PromptTokensForUsage(c *gin.Context, info *relaycommon.RelayInfo) int {
	if info == nil {
		return 0
	}
	if info.UsagePromptTokensSet {
		return info.UsagePromptTokens
	}
	info.UsagePromptTokensSet = true
	if c == nil || info.Request == nil || !constant.GetMediaToken ||
		(!constant.GetMediaTokenNotStream && !info.IsStream) || info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return 0
	}
	// CountRequestToken also serves diagnostics; preserve the admission context.
	admission, present := c.Get(string(constant.ContextKeyPromptTokens))
	defer func() {
		if present {
			c.Set(string(constant.ContextKeyPromptTokens), admission)
		} else {
			// Gin's context map is shared with stream diagnostics. Use its
			// synchronized setter; a nil value remains unavailable to typed reads.
			c.Set(string(constant.ContextKeyPromptTokens), nil)
		}
	}()
	meta := info.TokenCountMeta
	if meta == nil {
		meta = info.Request.GetTokenCountMeta()
	}
	if counted, err := CountRequestToken(c, meta, info); err == nil {
		info.UsagePromptTokens = counted
		info.UsagePromptTokensAvailable = true
	}
	return info.UsagePromptTokens
}

func ApplyLocalPromptUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) {
	usage.PromptTokens = PromptTokensForUsage(c, info)
	usage.InputTokenSource = dto.UsageSourceUnavailable
	if info != nil && info.UsagePromptTokensAvailable {
		usage.InputTokenSource = dto.UsageSourceLocalEstimate
	}
}

func estimateTextBytes(text string) int {
	if text == "" {
		return 0
	}
	// Three UTF-8 bytes per token is intentionally conservative for Latin
	// text while remaining close to one token per CJK rune. Most importantly,
	// this is O(1) after structured parsing and never sees media payloads.
	return (len(text) + 2) / 3
}

func imageTPMReserve(model string) int {
	if strings.Contains(strings.ToLower(model), "k2.6") {
		return k26ImageTPMReserve
	}
	return defaultImageTPMReserve
}

func RequestTPMTokens(promptTokens int, meta *types.TokenCountMeta) int {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if meta == nil || meta.MaxTokens <= 0 {
		return promptTokens
	}
	completionCount := meta.CompletionCount
	if completionCount <= 0 {
		completionCount = 1
	}
	if meta.MaxTokens > math.MaxInt/completionCount {
		return math.MaxInt
	}
	outputTokens := meta.MaxTokens * completionCount
	if promptTokens > math.MaxInt-outputTokens {
		return math.MaxInt
	}
	return promptTokens + outputTokens
}

func CountTokenRealtime(info *relaycommon.RelayInfo, request dto.RealtimeEvent, model string) (int, int, error) {
	audioToken := 0
	textToken := 0
	switch request.Type {
	case dto.RealtimeEventTypeSessionUpdate:
		if request.Session != nil {
			msgTokens := CountTextToken(request.Session.Instructions, model)
			textToken += msgTokens
		}
	case dto.RealtimeEventResponseAudioDelta:
		// count audio token
		atk, err := CountAudioTokenOutput(request.Delta, info.OutputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventResponseAudioTranscriptionDelta, dto.RealtimeEventResponseFunctionCallArgumentsDelta:
		// count text token
		tkm := CountTextToken(request.Delta, model)
		textToken += tkm
	case dto.RealtimeEventInputAudioBufferAppend:
		// count audio token
		atk, err := CountAudioTokenInput(request.Audio, info.InputAudioFormat)
		if err != nil {
			return 0, 0, fmt.Errorf("error counting audio token: %v", err)
		}
		audioToken += atk
	case dto.RealtimeEventConversationItemCreated:
		if request.Item != nil {
			switch request.Item.Type {
			case "message":
				for _, content := range request.Item.Content {
					if content.Type == "input_text" {
						tokens := CountTextToken(content.Text, model)
						textToken += tokens
					}
				}
			}
		}
	case dto.RealtimeEventTypeResponseDone:
		// count tools token
		if !info.IsFirstRequest {
			if info.RealtimeTools != nil && len(info.RealtimeTools) > 0 {
				for _, tool := range info.RealtimeTools {
					toolTokens := CountTokenInput(tool, model)
					textToken += 8
					textToken += toolTokens
				}
			}
		}
	}
	return textToken, audioToken, nil
}

func CountTokenInput(input any, model string) int {
	switch v := input.(type) {
	case string:
		return CountTextToken(v, model)
	case []string:
		text := ""
		for _, s := range v {
			text += s
		}
		return CountTextToken(text, model)
	case []interface{}:
		text := ""
		for _, item := range v {
			text += fmt.Sprintf("%v", item)
		}
		return CountTextToken(text, model)
	}
	return CountTokenInput(fmt.Sprintf("%v", input), model)
}

func CountAudioTokenInput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	return int(duration / 60 * 100 / 0.06), nil
}

func CountAudioTokenOutput(audioBase64 string, audioFormat string) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	duration, err := parseAudio(audioBase64, audioFormat)
	if err != nil {
		return 0, err
	}
	return int(duration / 60 * 200 / 0.24), nil
}

func CountPCM16AudioTokenOutput(audioBase64 string, sampleRate int) (int, error) {
	if audioBase64 == "" {
		return 0, nil
	}
	if sampleRate <= 0 {
		return 0, fmt.Errorf("invalid PCM16 sample rate: %d", sampleRate)
	}
	duration, err := parseAudioAtSampleRate(audioBase64, "pcm16", sampleRate)
	if err != nil {
		return 0, err
	}
	return int(duration / 60 * 200 / 0.24), nil
}

//func CountAudioToken(sec float64, audioType string) {
//	if audioType == "input" {
//
//	}
//}

// CountTextToken estimates text tokens using the locally available model tokenizer.
func CountTextToken(text string, model string) int {
	if text == "" {
		return 0
	}
	tokenEncoder := getTokenEncoder(model)
	return getTokenNum(tokenEncoder, text)
}
