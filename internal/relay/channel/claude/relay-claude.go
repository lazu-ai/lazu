package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

const (
	WebSearchMaxUsesLow    = 1
	WebSearchMaxUsesMedium = 5
	WebSearchMaxUsesHigh   = 10
)

type requestReasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

func stopReasonClaude2OpenAI(reason string) string {
	switch reason {
	case "stop_sequence":
		return "stop"
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func RequestOpenAI2ClaudeComplete(textRequest dto.GeneralOpenAIRequest) *dto.ClaudeRequest {
	topP := 0.0
	if textRequest.TopP != nil {
		topP = *textRequest.TopP
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		Prompt:        "",
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		TopP:          topP,
		TopK:          textRequest.TopK,
		Stream:        textRequest.Stream,
	}
	if claudeRequest.MaxTokensToSample == 0 {
		claudeRequest.MaxTokensToSample = 4096
	}
	prompt := ""
	for _, message := range textRequest.Messages {
		if message.Role == "user" {
			prompt += fmt.Sprintf("\n\nHuman: %s", message.StringContent())
		} else if message.Role == "assistant" {
			prompt += fmt.Sprintf("\n\nAssistant: %s", message.StringContent())
		} else if message.Role == "system" {
			if prompt == "" {
				prompt = message.StringContent()
			}
		}
	}
	prompt += "\n\nAssistant:"
	claudeRequest.Prompt = prompt
	return &claudeRequest
}

func RequestOpenAI2ClaudeMessage(c *gin.Context, textRequest dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	topP := 0.0
	if textRequest.TopP != nil {
		topP = *textRequest.TopP
	}
	claudeTools := make([]any, 0, len(textRequest.Tools))

	for _, tool := range textRequest.Tools {
		if params, ok := tool.Function.Parameters.(map[string]any); ok {
			claudeTool := dto.Tool{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
			}
			claudeTool.InputSchema = make(map[string]interface{})
			if params["type"] != nil {
				claudeTool.InputSchema["type"] = params["type"].(string)
			}
			claudeTool.InputSchema["properties"] = params["properties"]
			claudeTool.InputSchema["required"] = params["required"]
			for s, a := range params {
				if s == "type" || s == "properties" || s == "required" {
					continue
				}
				claudeTool.InputSchema[s] = a
			}
			claudeTools = append(claudeTools, &claudeTool)
		}
	}

	// Web search tool
	// https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/web-search-tool
	if textRequest.WebSearchOptions != nil {
		webSearchTool := dto.ClaudeWebSearchTool{
			Type: "web_search_20250305",
			Name: "web_search",
		}

		// 处理 user_location
		if textRequest.WebSearchOptions.UserLocation != nil {
			anthropicUserLocation := &dto.ClaudeWebSearchUserLocation{
				Type: "approximate", // 固定为 "approximate"
			}

			// 解析 UserLocation JSON
			var userLocationMap map[string]interface{}
			if err := json.Unmarshal(textRequest.WebSearchOptions.UserLocation, &userLocationMap); err == nil {
				// 检查是否有 approximate 字段
				if approximateData, ok := userLocationMap["approximate"].(map[string]interface{}); ok {
					if timezone, ok := approximateData["timezone"].(string); ok && timezone != "" {
						anthropicUserLocation.Timezone = timezone
					}
					if country, ok := approximateData["country"].(string); ok && country != "" {
						anthropicUserLocation.Country = country
					}
					if region, ok := approximateData["region"].(string); ok && region != "" {
						anthropicUserLocation.Region = region
					}
					if city, ok := approximateData["city"].(string); ok && city != "" {
						anthropicUserLocation.City = city
					}
				}
			}

			webSearchTool.UserLocation = anthropicUserLocation
		}

		// 处理 search_context_size 转换为 max_uses
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			switch textRequest.WebSearchOptions.SearchContextSize {
			case "low":
				webSearchTool.MaxUses = WebSearchMaxUsesLow
			case "medium":
				webSearchTool.MaxUses = WebSearchMaxUsesMedium
			case "high":
				webSearchTool.MaxUses = WebSearchMaxUsesHigh
			}
		}

		claudeTools = append(claudeTools, &webSearchTool)
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		MaxTokens:     textRequest.GetMaxTokens(),
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		TopP:          topP,
		TopK:          textRequest.TopK,
		Stream:        textRequest.Stream,
		Tools:         claudeTools,
		CacheControl:  textRequest.CacheControl,
		Speed:         textRequest.Speed,
		ServiceTier:   textRequest.ServiceTier,
		Metadata:      textRequest.Metadata,
	}

	// 处理 tool_choice 和 parallel_tool_calls
	if textRequest.ToolChoice != nil || textRequest.ParallelToolCalls != nil {
		claudeToolChoice := mapToolChoice(textRequest.ToolChoice, textRequest.ParallelToolCalls)
		if claudeToolChoice != nil {
			claudeRequest.ToolChoice = claudeToolChoice
		}
	}

	adaptiveEffort := ""
	if baseModel, effort, ok := TrimEffortSuffix(textRequest.Model); ok && SupportsAdaptiveThinkingModel(baseModel) {
		claudeRequest.Model = baseModel
		adaptiveEffort = effort
	}

	// 统一的 thinking 激活逻辑，优先级链：
	// 模型后缀(-thinking/#thinking) < reasoning_effort < reasoning.effort < reasoning.max_tokens
	var budgetPercentage float64

	// 1. 检测 -thinking 或 #thinking 模型后缀
	if model_setting.GetClaudeSettings().ThinkingAdapterEnabled {
		if model, suffix := TrimThinkingSuffix(textRequest.Model); suffix != "" {
			budgetPercentage = model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage
			claudeRequest.Model = model
		}
	}

	if claudeRequest.MaxTokens == 0 {
		claudeRequest.MaxTokens = uint(GetDefaultMaxTokensForThinkingModel(textRequest.Model, claudeRequest.Model))
	}

	// 2. reasoning_effort 参数 → 百分比映射覆盖
	if textRequest.ReasoningEffort != "" {
		if pct := EffortToPercentage(textRequest.ReasoningEffort); pct > 0 {
			budgetPercentage = pct
		}
		if SupportsAdaptiveThinkingModel(claudeRequest.Model) {
			adaptiveEffort = textRequest.ReasoningEffort
		}
	}

	// 3. reasoning 对象解析
	var reasoningMaxTokens int
	if textRequest.Reasoning != nil {
		var reasoning requestReasoning
		if err := common.Unmarshal(textRequest.Reasoning, &reasoning); err != nil {
			return nil, err
		}
		// reasoning.effort → 百分比映射覆盖
		if reasoning.Effort != "" {
			if pct := EffortToPercentage(reasoning.Effort); pct > 0 {
				budgetPercentage = pct
			}
			if SupportsAdaptiveThinkingModel(claudeRequest.Model) {
				adaptiveEffort = reasoning.Effort
			}
		}
		// reasoning.max_tokens → 直接指定 budget_tokens，最高优先级
		if reasoning.MaxTokens > 0 {
			reasoningMaxTokens = reasoning.MaxTokens
		}
	}

	// 4. 应用 thinking 配置
	if budgetPercentage > 0 || reasoningMaxTokens > 0 {
		if strings.HasPrefix(claudeRequest.Model, "claude-opus-4-7") {
			if adaptiveEffort == "" {
				adaptiveEffort = "high"
			}
			ApplyAdaptiveThinking(&claudeRequest, adaptiveEffort)
		} else {
			var budgetTokens int
			if reasoningMaxTokens > 0 {
				budgetTokens = reasoningMaxTokens
			} else {
				budgetTokens = int(float64(claudeRequest.MaxTokens) * budgetPercentage)
			}
			claudeRequest.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: &budgetTokens,
			}
		}
	}
	if claudeRequest.Thinking == nil && adaptiveEffort != "" {
		ApplyAdaptiveThinking(&claudeRequest, adaptiveEffort)
	}
	EnsureThinkingConstraints(&claudeRequest)

	if textRequest.Stop != nil {
		// stop maybe string/array string, convert to array string
		switch textRequest.Stop.(type) {
		case string:
			claudeRequest.StopSequences = []string{textRequest.Stop.(string)}
		case []interface{}:
			stopSequences := make([]string, 0)
			for _, stop := range textRequest.Stop.([]interface{}) {
				stopSequences = append(stopSequences, stop.(string))
			}
			claudeRequest.StopSequences = stopSequences
		}
	}
	formatMessages := make([]dto.Message, 0)
	lastMessage := dto.Message{
		Role: "tool",
	}
	for i, message := range textRequest.Messages {
		if message.Role == "" {
			textRequest.Messages[i].Role = "user"
		}
		fmtMessage := dto.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if message.Role == "tool" {
			fmtMessage.ToolCallId = message.ToolCallId
		}
		if message.Role == "assistant" && message.ToolCalls != nil {
			fmtMessage.ToolCalls = message.ToolCalls
		}
		if lastMessage.Role == message.Role && lastMessage.Role != "tool" {
			if lastMessage.IsStringContent() && message.IsStringContent() {
				fmtMessage.SetStringContent(strings.Trim(fmt.Sprintf("%s %s", lastMessage.StringContent(), message.StringContent()), "\""))
				// delete last message
				formatMessages = formatMessages[:len(formatMessages)-1]
			}
		}
		if fmtMessage.Content == nil {
			fmtMessage.SetStringContent("...")
		} else if fmtMessage.IsStringContent() && fmtMessage.StringContent() == "" {
			fmtMessage.SetStringContent("...")
		}
		formatMessages = append(formatMessages, fmtMessage)
		lastMessage = fmtMessage
	}

	claudeMessages := make([]dto.ClaudeMessage, 0)
	isFirstMessage := true
	// 初始化system消息数组，用于累积多个system消息
	var systemMessages []dto.ClaudeMediaMessage

	for _, message := range formatMessages {
		if message.Role == "system" {
			// 根据Claude API规范，system字段使用数组格式更有通用性
			if message.IsStringContent() {
				systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
					Type: "text",
					Text: common.GetPointer[string](normalizeClaudeTextContent(message.StringContent())),
				})
			} else {
				// 支持复合内容的system消息（虽然不常见，但需要考虑完整性）
				for _, ctx := range message.ParseContent() {
					if ctx.Type == "text" {
						systemMessages = append(systemMessages, dto.ClaudeMediaMessage{
							Type:         "text",
							Text:         common.GetPointer[string](normalizeClaudeTextContent(ctx.Text)),
							CacheControl: ctx.CacheControl,
						})
					}
					// 未来可以在这里扩展对图片等其他类型的支持
				}
			}
		} else {
			if isFirstMessage {
				isFirstMessage = false
				if message.Role != "user" {
					// fix: first message is assistant, add user message
					claudeMessage := dto.ClaudeMessage{
						Role: "user",
						Content: []dto.ClaudeMediaMessage{
							{
								Type: "text",
								Text: common.GetPointer[string]("..."),
							},
						},
					}
					claudeMessages = append(claudeMessages, claudeMessage)
				}
			}
			claudeMessage := dto.ClaudeMessage{
				Role: message.Role,
			}
			if message.Role == "tool" {
				if len(claudeMessages) > 0 && claudeMessages[len(claudeMessages)-1].Role == "user" {
					lastMessage := claudeMessages[len(claudeMessages)-1]
					if content, ok := lastMessage.Content.(string); ok {
						lastMessage.Content = []dto.ClaudeMediaMessage{
							{
								Type: "text",
								Text: common.GetPointer[string](content),
							},
						}
					}
					lastMessage.Content = append(lastMessage.Content.([]dto.ClaudeMediaMessage), dto.ClaudeMediaMessage{
						Type:      "tool_result",
						ToolUseId: message.ToolCallId,
						Content:   message.Content,
					})
					claudeMessages[len(claudeMessages)-1] = lastMessage
					continue
				} else {
					claudeMessage.Role = "user"
					claudeMessage.Content = []dto.ClaudeMediaMessage{
						{
							Type:      "tool_result",
							ToolUseId: message.ToolCallId,
							Content:   message.Content,
						},
					}
				}
			} else if message.IsStringContent() && message.ToolCalls == nil {
				claudeMessage.Content = normalizeClaudeTextContent(message.StringContent())
			} else {
				claudeMediaMessages := make([]dto.ClaudeMediaMessage, 0)
				for _, mediaMessage := range message.ParseContent() {
					claudeMediaMessage := dto.ClaudeMediaMessage{
						Type: mediaMessage.Type,
					}
					if mediaMessage.Type == "text" {
						claudeMediaMessage.Text = common.GetPointer[string](normalizeClaudeTextContent(mediaMessage.Text))
						claudeMediaMessage.CacheControl = mediaMessage.CacheControl
					} else {
						imageUrl := mediaMessage.GetImageMedia()
						claudeMediaMessage.Type = "image"
						claudeMediaMessage.CacheControl = mediaMessage.CacheControl
						claudeMediaMessage.Source = &dto.ClaudeMessageSource{
							Type: "base64",
						}
						// 判断是否是url
						if strings.HasPrefix(imageUrl.Url, "http") {
							// 是url，获取图片的类型和base64编码的数据
							fileData, err := service.GetFileBase64FromUrl(c, imageUrl.Url, "formatting image for Claude")
							if err != nil {
								return nil, fmt.Errorf("get file base64 from url failed: %s", err.Error())
							}
							claudeMediaMessage.Source.MediaType = fileData.MimeType
							claudeMediaMessage.Source.Data = fileData.Base64Data
						} else {
							_, format, base64String, err := service.DecodeBase64ImageData(imageUrl.Url)
							if err != nil {
								return nil, err
							}
							claudeMediaMessage.Source.MediaType = "image/" + format
							claudeMediaMessage.Source.Data = base64String
						}
					}
					claudeMediaMessages = append(claudeMediaMessages, claudeMediaMessage)
				}
				if message.ToolCalls != nil {
					for _, toolCall := range message.ParseToolCalls() {
						inputObj := make(map[string]any)
						if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &inputObj); err != nil {
							common.SysLog("tool call function arguments is not a map[string]any: " + fmt.Sprintf("%v", toolCall.Function.Arguments))
							continue
						}
						claudeMediaMessages = append(claudeMediaMessages, dto.ClaudeMediaMessage{
							Type:  "tool_use",
							Id:    toolCall.ID,
							Name:  toolCall.Function.Name,
							Input: inputObj,
						})
					}
				}
				claudeMessage.Content = claudeMediaMessages
			}
			claudeMessages = append(claudeMessages, claudeMessage)
		}
	}

	// 设置累积的system消息
	if len(systemMessages) > 0 {
		claudeRequest.System = systemMessages
	}

	claudeRequest.Prompt = ""
	claudeRequest.Messages = claudeMessages
	return &claudeRequest, nil
}

func StreamResponseClaude2OpenAI(reqMode int, claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Model = claudeResponse.Model
	response.Choices = make([]dto.ChatCompletionsStreamResponseChoice, 0)
	tools := make([]dto.ToolCallResponse, 0)
	fcIdx := 0
	if claudeResponse.Index != nil {
		fcIdx = *claudeResponse.Index - 1
		if fcIdx < 0 {
			fcIdx = 0
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	if reqMode == RequestModeCompletion {
		choice.Delta.SetContentString(claudeResponse.Completion)
		finishReason := stopReasonClaude2OpenAI(claudeResponse.StopReason)
		if finishReason != "null" {
			choice.FinishReason = &finishReason
		}
	} else {
		if claudeResponse.Type == "message_start" {
			response.Id = claudeResponse.Message.Id
			response.Model = claudeResponse.Message.Model
			//claudeUsage = &claudeResponse.Message.Usage
			choice.Delta.SetContentString("")
			choice.Delta.Role = "assistant"
		} else if claudeResponse.Type == "content_block_start" {
			if claudeResponse.ContentBlock != nil {
				// 如果是文本块，尽可能发送首段文本（若存在）
				if claudeResponse.ContentBlock.Type == "text" && claudeResponse.ContentBlock.Text != nil {
					choice.Delta.SetContentString(*claudeResponse.ContentBlock.Text)
				}
				if claudeResponse.ContentBlock.Type == "tool_use" {
					tools = append(tools, dto.ToolCallResponse{
						Index: common.GetPointer(fcIdx),
						ID:    claudeResponse.ContentBlock.Id,
						Type:  "function",
						Function: dto.FunctionResponse{
							Name:      claudeResponse.ContentBlock.Name,
							Arguments: "",
						},
					})
				}
			} else {
				return nil
			}
		} else if claudeResponse.Type == "content_block_delta" {
			if claudeResponse.Delta != nil {
				choice.Delta.Content = claudeResponse.Delta.Text
				switch claudeResponse.Delta.Type {
				case "input_json_delta":
					tools = append(tools, dto.ToolCallResponse{
						Type:  "function",
						Index: common.GetPointer(fcIdx),
						Function: dto.FunctionResponse{
							Arguments: *claudeResponse.Delta.PartialJson,
						},
					})
				case "signature_delta":
					// 加密的不处理
					signatureContent := "\n"
					choice.Delta.ReasoningContent = &signatureContent
				case "thinking_delta":
					choice.Delta.ReasoningContent = claudeResponse.Delta.Thinking
				}
			}
		} else if claudeResponse.Type == "message_delta" {
			finishReason := stopReasonClaude2OpenAI(*claudeResponse.Delta.StopReason)
			if finishReason != "null" {
				choice.FinishReason = &finishReason
			}
			//claudeUsage = &claudeResponse.Usage
		} else if claudeResponse.Type == "message_stop" {
			return nil
		} else {
			return nil
		}
	}
	if len(tools) > 0 {
		choice.Delta.Content = nil // compatible with other OpenAI derivative applications, like LobeOpenAICompatibleFactory ...
		choice.Delta.ToolCalls = tools
	}
	response.Choices = append(response.Choices, choice)

	return &response
}

func ResponseClaude2OpenAI(reqMode int, claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	choices := make([]dto.OpenAITextResponseChoice, 0)
	fullTextResponse := dto.OpenAITextResponse{
		Id:      common.GenerateChatCompletionID(),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
	}
	var responseText string
	var responseThinking string
	if len(claudeResponse.Content) > 0 {
		responseText = claudeResponse.Content[0].GetText()
		if claudeResponse.Content[0].Thinking != nil {
			responseThinking = *claudeResponse.Content[0].Thinking
		}
	}
	tools := make([]dto.ToolCallResponse, 0)
	thinkingContent := ""

	if reqMode == RequestModeCompletion {
		choice := dto.OpenAITextResponseChoice{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: strings.TrimPrefix(claudeResponse.Completion, " "),
				Name:    nil,
			},
			FinishReason: stopReasonClaude2OpenAI(claudeResponse.StopReason),
		}
		choices = append(choices, choice)
	} else {
		fullTextResponse.Id = claudeResponse.Id
		for _, message := range claudeResponse.Content {
			switch message.Type {
			case "tool_use":
				args, _ := json.Marshal(message.Input)
				tools = append(tools, dto.ToolCallResponse{
					ID:   message.Id,
					Type: "function", // compatible with other OpenAI derivative applications
					Function: dto.FunctionResponse{
						Name:      message.Name,
						Arguments: string(args),
					},
				})
			case "thinking":
				// 加密的不管， 只输出明文的推理过程
				if message.Thinking != nil {
					thinkingContent = *message.Thinking
				}
			case "text":
				responseText = message.GetText()
			}
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role: "assistant",
		},
		FinishReason: stopReasonClaude2OpenAI(claudeResponse.StopReason),
	}
	choice.SetStringContent(responseText)
	if len(responseThinking) > 0 {
		choice.ReasoningContent = responseThinking
	}
	if len(tools) > 0 {
		choice.Message.SetToolCalls(tools)
	}
	choice.Message.ReasoningContent = thinkingContent
	fullTextResponse.Model = claudeResponse.Model
	choices = append(choices, choice)
	fullTextResponse.Choices = choices
	return &fullTextResponse
}

type ClaudeResponseInfo struct {
	ResponseId   string
	Created      int64
	Model        string
	ResponseText strings.Builder
	Usage        *dto.Usage
	Done         bool
}

func normalizeClaudeTextContent(text string) string {
	if text == "" {
		return "..."
	}
	return text
}

func applyClaudeUsageDetails(target *dto.Usage, usage *dto.ClaudeUsage) {
	if target == nil || usage == nil {
		return
	}
	target.PromptTokens = usage.InputTokens
	target.PromptExcludesCache = true
	target.InputTokenSource = dto.UsageSourceProvider
	target.PromptTokensDetails.CachedTokens = usage.CacheReadInputTokens
	target.PromptTokensDetails.CachedCreationTokens = usage.CacheCreationInputTokens
	target.CompletionTokens = usage.OutputTokens
	target.OutputTokenSource = dto.UsageSourceProvider
	applyClaudeThinkingTokens(target, usage)
	if usage.CacheCreation != nil {
		target.PromptTokensDetails.CachedCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
		target.PromptTokensDetails.CachedCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
	}
}

// applyClaudeThinkingTokens carries Claude's thinking count into the shared
// reasoning field, the one every other protocol fills. It is informational:
// thinking is already inside CompletionTokens and is billed there.
func applyClaudeThinkingTokens(target *dto.Usage, usage *dto.ClaudeUsage) {
	if target == nil {
		return
	}
	if thinking := usage.ThinkingTokens(); thinking > 0 {
		target.CompletionTokenDetails.ReasoningTokens = thinking
	}
}

func applyClaudeServerToolUsage(c *gin.Context, usage *dto.ClaudeUsage) {
	if c == nil || usage == nil || usage.ServerToolUse == nil || usage.ServerToolUse.WebSearchRequests <= 0 {
		return
	}
	c.Set("claude_web_search_requests", usage.ServerToolUse.WebSearchRequests)
}

func fillClaudeUsageFromAccumulated(current *dto.ClaudeUsage, accumulated *dto.Usage) *dto.ClaudeUsage {
	if accumulated == nil {
		return current
	}
	if current == nil {
		current = &dto.ClaudeUsage{}
	}
	if current.InputTokens == 0 {
		current.InputTokens = accumulated.PromptTokens
	}
	if current.OutputTokens == 0 {
		current.OutputTokens = accumulated.CompletionTokens
	}
	if current.CacheReadInputTokens == 0 {
		current.CacheReadInputTokens = accumulated.PromptTokensDetails.CachedTokens
	}
	if current.CacheCreationInputTokens == 0 {
		current.CacheCreationInputTokens = accumulated.PromptTokensDetails.CachedCreationTokens
	}
	if accumulated.PromptTokensDetails.CachedCreation5mTokens > 0 || accumulated.PromptTokensDetails.CachedCreation1hTokens > 0 {
		if current.CacheCreation == nil {
			current.CacheCreation = &dto.ClaudeCacheCreation{}
		}
		if current.CacheCreation.Ephemeral5mInputTokens == 0 {
			current.CacheCreation.Ephemeral5mInputTokens = accumulated.PromptTokensDetails.CachedCreation5mTokens
		}
		if current.CacheCreation.Ephemeral1hInputTokens == 0 {
			current.CacheCreation.Ephemeral1hInputTokens = accumulated.PromptTokensDetails.CachedCreation1hTokens
		}
	}
	return current
}

func FormatClaudeResponseInfo(requestMode int, claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	if requestMode == RequestModeCompletion {
		claudeInfo.ResponseText.WriteString(claudeResponse.Completion)
	} else {
		if claudeResponse.Type == "message_start" {
			claudeInfo.ResponseId = claudeResponse.Message.Id
			claudeInfo.Model = claudeResponse.Message.Model

			// message_start, 获取usage
			applyClaudeUsageDetails(claudeInfo.Usage, claudeResponse.Message.Usage)
		} else if claudeResponse.Type == "content_block_delta" {
			if claudeResponse.Delta.Text != nil {
				claudeInfo.ResponseText.WriteString(*claudeResponse.Delta.Text)
			}
			if claudeResponse.Delta.Thinking != nil {
				claudeInfo.ResponseText.WriteString(*claudeResponse.Delta.Thinking)
			}
		} else if claudeResponse.Type == "message_delta" {
			// 最终的usage获取
			if claudeResponse.Usage.InputTokens > 0 {
				// 不叠加，只取最新的
				claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
				claudeInfo.Usage.PromptExcludesCache = true
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
			}
			if claudeResponse.Usage.CacheReadInputTokens > 0 {
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
				claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
			}
			if claudeResponse.Usage.CacheCreationInputTokens > 0 {
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
				claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
			}
			if claudeResponse.Usage.CacheCreation != nil {
				claudeInfo.Usage.PromptTokensDetails.CachedCreation5mTokens = claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens
				claudeInfo.Usage.PromptTokensDetails.CachedCreation1hTokens = claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens
			}
			claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
			claudeInfo.Usage.OutputTokenSource = dto.UsageSourceProvider
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
			applyClaudeThinkingTokens(claudeInfo.Usage, claudeResponse.Usage)

			// 判断是否完整
			claudeInfo.Done = true
		} else if claudeResponse.Type == "content_block_start" {
		} else {
			return false
		}
	}
	if oaiResponse != nil {
		oaiResponse.Id = claudeInfo.ResponseId
		oaiResponse.Created = claudeInfo.Created
		oaiResponse.Model = claudeInfo.Model
	}
	return true
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string, requestMode int) *types.LazuError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	applyClaudeServerToolUsage(c, claudeResponse.Usage)
	// How the reply ended, kept for the outcome row. Claude reports it once, on
	// the message_delta that closes the message, and in its own vocabulary.
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		if reason := stopReasonClaude2OpenAI(*claudeResponse.Delta.StopReason); reason != "null" {
			info.FinishReason = reason
		}
	}
	if info.RelayFormat == types.RelayFormatClaude {
		FormatClaudeResponseInfo(requestMode, &claudeResponse, nil, claudeInfo)
		if requestMode != RequestModeCompletion {
			if claudeResponse.Type == "message_start" {
				info.UpstreamModelName = claudeResponse.Message.Model
				if claudeResponse.Message != nil {
					claudeResponse.Message.Usage = fillClaudeUsageFromAccumulated(claudeResponse.Message.Usage, claudeInfo.Usage)
				}
			}
			if claudeResponse.Type == "message_delta" {
				claudeResponse.Usage = fillClaudeUsageFromAccumulated(claudeResponse.Usage, claudeInfo.Usage)
			}
		}
		helper.ClaudeData(c, claudeResponse)
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		if requestMode != RequestModeCompletion && claudeResponse.Type == "message_delta" {
			claudeResponse.Usage = fillClaudeUsageFromAccumulated(claudeResponse.Usage, claudeInfo.Usage)
		}
		response := StreamResponseClaude2OpenAI(requestMode, &claudeResponse)

		if !FormatClaudeResponseInfo(requestMode, &claudeResponse, response, claudeInfo) {
			return nil
		}

		err = helper.ObjectData(c, response)
		if err != nil {
			logger.LogError(c, "send_stream_response_failed: "+err.Error())
		}
	}
	return nil
}

func finalizeClaudeStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) {

	if requestMode == RequestModeCompletion {
		if claudeInfo.ResponseText.Len() == 0 {
			return
		}
		claudeInfo.Usage = service.ResponseText2Usage(claudeInfo.ResponseText.String(), info.UpstreamModelName, service.PromptTokensForUsage(c, info))
	} else {

		if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
			if common.DebugEnabled {
				common.SysLog("claude response usage is not complete, maybe upstream error")
			}
			fallbackUsage := service.ResponseText2Usage(claudeInfo.ResponseText.String(), info.UpstreamModelName, claudeInfo.Usage.PromptTokens)
			// Claude input excludes cache buckets; a provider-reported zero is valid.
			// A whole-prompt estimate here would count the cached input twice.
			if claudeInfo.Usage.PromptTokens == 0 && claudeInfo.Usage.InputTokenSource != dto.UsageSourceProvider {
				if claudeInfo.ResponseText.Len() > 0 || service.ValidUsage(claudeInfo.Usage) {
					service.ApplyLocalPromptUsage(c, info, claudeInfo.Usage)
				}
			}
			if claudeInfo.Usage.CompletionTokens == 0 {
				claudeInfo.Usage.CompletionTokens = fallbackUsage.CompletionTokens
				claudeInfo.Usage.OutputTokenSource = dto.UsageSourceLocalEstimate
			}
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		} else if claudeInfo.Usage.TotalTokens == 0 {
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		}
	}

}

func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) {
	finalizeClaudeStreamUsage(c, info, claudeInfo, requestMode)

	if info.RelayFormat == types.RelayFormatClaude {
		//
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		if info.ShouldIncludeUsage {
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, *claudeInfo.Usage)
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog("send final response failed: " + err.Error())
			}
		}
		helper.Done(c)
	}
}

func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*dto.Usage, *types.LazuError) {
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var err *types.LazuError
	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		err = HandleStreamResponseData(c, info, claudeInfo, data, requestMode)
		if err != nil {
			return false
		}
		return true
	})
	if err == nil {
		err = scan.RelayError(c.Writer.Written() || info.HasSendResponse())
	}
	if err != nil {
		finalizeClaudeStreamUsage(c, info, claudeInfo, requestMode)
		if service.ValidUsage(claudeInfo.Usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return claudeInfo.Usage, err
	}

	HandleStreamFinalResponse(c, info, claudeInfo, requestMode)
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte, requestMode int) *types.LazuError {
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	// How the reply ended, kept for the outcome row exactly as the stream
	// handler does from its closing message_delta.
	if claudeResponse.StopReason != "" {
		info.FinishReason = stopReasonClaude2OpenAI(claudeResponse.StopReason)
	}
	if requestMode == RequestModeCompletion {
		claudeInfo.Usage = service.ResponseText2Usage(claudeResponse.Completion, info.OriginModelName, service.PromptTokensForUsage(c, info))
	} else {
		claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
		claudeInfo.Usage.PromptExcludesCache = true
		claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
		claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.OutputTokenSource = dto.UsageSourceProvider
		claudeInfo.Usage.TotalTokens = claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens
		applyClaudeThinkingTokens(claudeInfo.Usage, claudeResponse.Usage)
		claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
		claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
		if claudeResponse.Usage.CacheCreation != nil {
			claudeInfo.Usage.PromptTokensDetails.CachedCreation5mTokens = claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens
			claudeInfo.Usage.PromptTokensDetails.CachedCreation1hTokens = claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens
		}
	}
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		openaiResponse := ResponseClaude2OpenAI(requestMode, &claudeResponse)
		openaiResponse.Usage = *claudeInfo.Usage
		responseData, err = json.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		responseData = data
	}

	applyClaudeServerToolUsage(c, claudeResponse.Usage)

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if common.DebugEnabled {
		println("responseBody: ", string(responseBody))
	}
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody, requestMode)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}

func mapToolChoice(toolChoice any, parallelToolCalls *bool) *dto.ClaudeToolChoice {
	var claudeToolChoice *dto.ClaudeToolChoice

	// 处理 tool_choice 字符串值
	if toolChoiceStr, ok := toolChoice.(string); ok {
		switch toolChoiceStr {
		case "auto":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "auto",
			}
		case "required":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "any",
			}
		case "none":
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "none",
			}
		}
	} else if toolChoiceMap, ok := toolChoice.(map[string]interface{}); ok {
		// 处理 tool_choice 对象值
		if function, ok := toolChoiceMap["function"].(map[string]interface{}); ok {
			if toolName, ok := function["name"].(string); ok {
				claudeToolChoice = &dto.ClaudeToolChoice{
					Type: "tool",
					Name: toolName,
				}
			}
		}
	}

	// 处理 parallel_tool_calls
	if parallelToolCalls != nil {
		if claudeToolChoice == nil {
			// 如果没有 tool_choice，但有 parallel_tool_calls，创建默认的 auto 类型
			claudeToolChoice = &dto.ClaudeToolChoice{
				Type: "auto",
			}
		}

		// 设置 disable_parallel_tool_use
		// 如果 parallel_tool_calls 为 true，则 disable_parallel_tool_use 为 false
		claudeToolChoice.DisableParallelToolUse = !*parallelToolCalls
	}

	return claudeToolChoice
}
