package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func ClaudeToOpenAIRequest(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openAIRequest := dto.GeneralOpenAIRequest{
		Model:        claudeRequest.Model,
		MaxTokens:    claudeRequest.MaxTokens,
		Temperature:  claudeRequest.Temperature,
		Stream:       claudeRequest.Stream,
		CacheControl: claudeRequest.CacheControl,
		Speed:        claudeRequest.Speed,
		ServiceTier:  claudeRequest.ServiceTier,
		Metadata:     claudeRequest.Metadata,
	}
	if claudeRequest.TopP != 0 {
		openAIRequest.TopP = common.GetPointer(claudeRequest.TopP)
	}

	if claudeRequest.Thinking != nil && claudeRequest.Thinking.Type == "enabled" {
		// 检测原始模型名使用的 thinking 后缀格式（-thinking 或 #thinking），保留一致
		var thinkingSuffix string
		if strings.HasSuffix(info.OriginModelName, "-thinking") {
			thinkingSuffix = "-thinking"
		} else if strings.HasSuffix(info.OriginModelName, "#thinking") {
			thinkingSuffix = "#thinking"
		}
		if thinkingSuffix != "" &&
			!strings.HasSuffix(openAIRequest.Model, "-thinking") &&
			!strings.HasSuffix(openAIRequest.Model, "#thinking") {
			openAIRequest.Model = openAIRequest.Model + thinkingSuffix
		}
	}

	// Convert stop sequences
	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	// Convert tools
	tools, _ := common.Any2Type[[]dto.Tool](claudeRequest.Tools)
	openAITools := make([]dto.ToolCallRequest, 0)
	for _, claudeTool := range tools {
		openAITool := dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        claudeTool.Name,
				Description: claudeTool.Description,
				Parameters:  claudeTool.InputSchema,
			},
		}
		openAITools = append(openAITools, openAITool)
	}
	openAIRequest.Tools = openAITools

	// Convert messages
	openAIMessages := make([]dto.Message, 0)

	// Add system message if present
	if claudeRequest.System != nil {
		if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
			openAIMessage := dto.Message{
				Role: "system",
			}
			openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			systems := claudeRequest.ParseSystem()
			if len(systems) > 0 {
				openAIMessage := dto.Message{
					Role: "system",
				}
				systemStr := ""
				for _, system := range systems {
					if system.Text != nil {
						systemStr += *system.Text
					}
				}
				openAIMessage.SetStringContent(systemStr)
				openAIMessages = append(openAIMessages, openAIMessage)
			}
		}
	}
	for _, claudeMessage := range claudeRequest.Messages {
		openAIMessage := dto.Message{
			Role: claudeMessage.Role,
		}

		//log.Printf("claudeMessage.Content: %v", claudeMessage.Content)
		if claudeMessage.IsStringContent() {
			openAIMessage.SetStringContent(claudeMessage.GetStringContent())
		} else {
			content, err := claudeMessage.ParseContent()
			if err != nil {
				return nil, err
			}
			contents := content
			var toolCalls []dto.ToolCallRequest
			mediaMessages := make([]dto.MediaContent, 0, len(contents))

			for _, mediaMsg := range contents {
				switch mediaMsg.Type {
				case "text":
					message := dto.MediaContent{
						Type:         "text",
						Text:         mediaMsg.GetText(),
						CacheControl: mediaMsg.CacheControl,
					}
					mediaMessages = append(mediaMessages, message)
				case "image":
					// Handle image conversion (base64 to URL or keep as is)
					imageData := fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, mediaMsg.Source.Data)
					//textContent += fmt.Sprintf("[Image: %s]", imageData)
					mediaMessage := dto.MediaContent{
						Type:     "image_url",
						ImageUrl: &dto.MessageImageUrl{Url: imageData},
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "tool_use":
					toolCall := dto.ToolCallRequest{
						ID:   mediaMsg.Id,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      mediaMsg.Name,
							Arguments: toJSONString(mediaMsg.Input),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				case "tool_result":
					// Add tool result as a separate message
					toolName := mediaMsg.Name
					if toolName == "" {
						toolName = claudeRequest.SearchToolNameByToolCallId(mediaMsg.ToolUseId)
					}
					oaiToolMessage := dto.Message{
						Role:       "tool",
						Name:       &toolName,
						ToolCallId: mediaMsg.ToolUseId,
					}
					//oaiToolMessage.SetStringContent(*mediaMsg.GetMediaContent().Text)
					if mediaMsg.IsStringContent() {
						oaiToolMessage.SetStringContent(mediaMsg.GetStringContent())
					} else {
						mediaContents := mediaMsg.ParseMediaContent()
						encodeJson, _ := common.Marshal(mediaContents)
						oaiToolMessage.SetStringContent(string(encodeJson))
					}
					openAIMessages = append(openAIMessages, oaiToolMessage)
				}
			}

			if len(toolCalls) > 0 {
				openAIMessage.SetToolCalls(toolCalls)
			}

			if len(mediaMessages) > 0 && len(toolCalls) == 0 {
				openAIMessage.SetMediaContent(mediaMessages)
			}
		}
		if len(openAIMessage.ParseContent()) > 0 || len(openAIMessage.ToolCalls) > 0 {
			openAIMessages = append(openAIMessages, openAIMessage)
		}
	}

	openAIRequest.Messages = openAIMessages

	return &openAIRequest, nil
}

func generateStopBlock(index int) *dto.ClaudeResponse {
	return &dto.ClaudeResponse{
		Type:  "content_block_stop",
		Index: common.GetPointer[int](index),
	}
}

func StreamResponseOpenAI2Claude(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) []*dto.ClaudeResponse {
	var claudeResponses []*dto.ClaudeResponse
	if info.SendResponseCount == 1 {
		msg := &dto.ClaudeMediaMessage{
			Id:    openAIResponse.Id,
			Model: openAIResponse.Model,
			Type:  "message",
			Role:  "assistant",
			Usage: &dto.ClaudeUsage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		}
		msg.SetContent(make([]any, 0))
		claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
			Type:    "message_start",
			Message: msg,
		})
		//claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
		//	Type: "ping",
		//})
		if openAIResponse.IsToolCall() {
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeTools
			resp := &dto.ClaudeResponse{
				Type: "content_block_start",
				ContentBlock: &dto.ClaudeMediaMessage{
					Id:    openAIResponse.GetFirstToolCall().ID,
					Type:  "tool_use",
					Name:  openAIResponse.GetFirstToolCall().Function.Name,
					Input: map[string]interface{}{},
				},
			}
			resp.SetIndex(0)
			claudeResponses = append(claudeResponses, resp)
		} else {

		}
		// 判断首个响应是否存在内容（非标准的 OpenAI 响应）
		if len(openAIResponse.Choices) > 0 && len(openAIResponse.Choices[0].Delta.GetContentString()) > 0 {
			claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
				Index: &info.ClaudeConvertInfo.Index,
				Type:  "content_block_start",
				ContentBlock: &dto.ClaudeMediaMessage{
					Type: "text",
					Text: common.GetPointer[string](""),
				},
			})
			claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
				Index: &info.ClaudeConvertInfo.Index,
				Type:  "content_block_delta",
				Delta: &dto.ClaudeMediaMessage{
					Type: "text_delta",
					Text: common.GetPointer[string](openAIResponse.Choices[0].Delta.GetContentString()),
				},
			})
			info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeText
		}
		return claudeResponses
	}

	if len(openAIResponse.Choices) == 0 {
		// no choices
		// 可能为非标准的 OpenAI 响应，判断是否已经完成
		if info.Done {
			claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
			oaiUsage := info.ClaudeConvertInfo.Usage
			if oaiUsage != nil {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Type:  "message_delta",
					Usage: buildClaudeUsageFromOpenAIUsage(oaiUsage),
					Delta: &dto.ClaudeMediaMessage{
						StopReason: common.GetPointer[string](stopReasonOpenAI2Claude(info.FinishReason)),
					},
				})
			}
			claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
				Type: "message_stop",
			})
		}
		return claudeResponses
	} else {
		chosenChoice := openAIResponse.Choices[0]
		if chosenChoice.FinishReason != nil && *chosenChoice.FinishReason != "" {
			// should be done
			info.FinishReason = *chosenChoice.FinishReason
			if !info.Done {
				return claudeResponses
			}
		}
		if info.Done {
			claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
			oaiUsage := info.ClaudeConvertInfo.Usage
			if oaiUsage != nil {
				claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
					Type:  "message_delta",
					Usage: buildClaudeUsageFromOpenAIUsage(oaiUsage),
					Delta: &dto.ClaudeMediaMessage{
						StopReason: common.GetPointer[string](stopReasonOpenAI2Claude(info.FinishReason)),
					},
				})
			}
			claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
				Type: "message_stop",
			})
		} else {
			var claudeResponse dto.ClaudeResponse
			var isEmpty bool
			claudeResponse.Type = "content_block_delta"
			if len(chosenChoice.Delta.ToolCalls) > 0 {
				if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeTools {
					claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
					info.ClaudeConvertInfo.Index++
					claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
						Index: &info.ClaudeConvertInfo.Index,
						Type:  "content_block_start",
						ContentBlock: &dto.ClaudeMediaMessage{
							Id:    openAIResponse.GetFirstToolCall().ID,
							Type:  "tool_use",
							Name:  openAIResponse.GetFirstToolCall().Function.Name,
							Input: map[string]interface{}{},
						},
					})
				}
				info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeTools
				// tools delta
				claudeResponse.Delta = &dto.ClaudeMediaMessage{
					Type:        "input_json_delta",
					PartialJson: &chosenChoice.Delta.ToolCalls[0].Function.Arguments,
				}
			} else {
				reasoning := chosenChoice.Delta.GetReasoningContent()
				textContent := chosenChoice.Delta.GetContentString()
				if reasoning != "" || textContent != "" {
					if reasoning != "" {
						if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeThinking {
							//info.ClaudeConvertInfo.Index++
							claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
								Index: &info.ClaudeConvertInfo.Index,
								Type:  "content_block_start",
								ContentBlock: &dto.ClaudeMediaMessage{
									Type:     "thinking",
									Thinking: common.GetPointer[string](""),
								},
							})
						}
						info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeThinking
						// text delta
						claudeResponse.Delta = &dto.ClaudeMediaMessage{
							Type:     "thinking_delta",
							Thinking: &reasoning,
						}
					} else {
						if info.ClaudeConvertInfo.LastMessagesType != relaycommon.LastMessageTypeText {
							if info.LastMessagesType == relaycommon.LastMessageTypeThinking || info.LastMessagesType == relaycommon.LastMessageTypeTools {
								claudeResponses = append(claudeResponses, generateStopBlock(info.ClaudeConvertInfo.Index))
								info.ClaudeConvertInfo.Index++
							}
							claudeResponses = append(claudeResponses, &dto.ClaudeResponse{
								Index: &info.ClaudeConvertInfo.Index,
								Type:  "content_block_start",
								ContentBlock: &dto.ClaudeMediaMessage{
									Type: "text",
									Text: common.GetPointer[string](""),
								},
							})
						}
						info.ClaudeConvertInfo.LastMessagesType = relaycommon.LastMessageTypeText
						// text delta
						claudeResponse.Delta = &dto.ClaudeMediaMessage{
							Type: "text_delta",
							Text: common.GetPointer[string](textContent),
						}
					}
				} else {
					isEmpty = true
				}
			}
			claudeResponse.Index = &info.ClaudeConvertInfo.Index
			if !isEmpty {
				claudeResponses = append(claudeResponses, &claudeResponse)
			}
		}
	}

	return claudeResponses
}

func buildClaudeUsageFromOpenAIUsage(oaiUsage *dto.Usage) *dto.ClaudeUsage {
	if oaiUsage == nil {
		return nil
	}
	usage := &dto.ClaudeUsage{
		InputTokens:              oaiUsage.PromptTokens,
		OutputTokens:             oaiUsage.CompletionTokens,
		CacheCreationInputTokens: oaiUsage.PromptTokensDetails.CachedCreationTokens,
		CacheReadInputTokens:     oaiUsage.PromptTokensDetails.CachedTokens,
	}
	if oaiUsage.PromptTokensDetails.CachedCreation5mTokens > 0 || oaiUsage.PromptTokensDetails.CachedCreation1hTokens > 0 {
		usage.CacheCreation = &dto.ClaudeCacheCreation{
			Ephemeral5mInputTokens: oaiUsage.PromptTokensDetails.CachedCreation5mTokens,
			Ephemeral1hInputTokens: oaiUsage.PromptTokensDetails.CachedCreation1hTokens,
		}
	}
	return usage
}

func ResponseOpenAI2Claude(openAIResponse *dto.OpenAITextResponse, info *relaycommon.RelayInfo) *dto.ClaudeResponse {
	var stopReason string
	contents := make([]dto.ClaudeMediaMessage, 0)
	claudeResponse := &dto.ClaudeResponse{
		Id:    openAIResponse.Id,
		Type:  "message",
		Role:  "assistant",
		Model: openAIResponse.Model,
	}
	for _, choice := range openAIResponse.Choices {
		stopReason = stopReasonOpenAI2Claude(choice.FinishReason)
		if choice.FinishReason == "tool_calls" {
			for _, toolUse := range choice.Message.ParseToolCalls() {
				claudeContent := dto.ClaudeMediaMessage{}
				claudeContent.Type = "tool_use"
				claudeContent.Id = toolUse.ID
				claudeContent.Name = toolUse.Function.Name
				var mapParams map[string]interface{}
				if err := common.Unmarshal([]byte(toolUse.Function.Arguments), &mapParams); err == nil {
					claudeContent.Input = mapParams
				} else {
					claudeContent.Input = toolUse.Function.Arguments
				}
				contents = append(contents, claudeContent)
			}
		} else {
			claudeContent := dto.ClaudeMediaMessage{}
			claudeContent.Type = "text"
			claudeContent.SetText(choice.Message.StringContent())
			contents = append(contents, claudeContent)
		}
	}
	claudeResponse.Content = contents
	claudeResponse.StopReason = stopReason
	claudeResponse.Usage = &dto.ClaudeUsage{
		InputTokens:  openAIResponse.PromptTokens,
		OutputTokens: openAIResponse.CompletionTokens,
	}

	return claudeResponse
}

func stopReasonOpenAI2Claude(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "stop_sequence":
		return "stop_sequence"
	case "length":
		fallthrough
	case "max_tokens":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return reason
	}
}

func toJSONString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func normalizeGeminiFunctionDeclarations(declarations any) []dto.FunctionRequest {
	switch typed := declarations.(type) {
	case []dto.FunctionRequest:
		return typed
	case []any:
		functions := make([]dto.FunctionRequest, 0, len(typed))
		for _, item := range typed {
			var function dto.FunctionRequest
			if item == nil {
				continue
			}
			if data, err := json.Marshal(item); err == nil {
				if err := json.Unmarshal(data, &function); err == nil {
					functions = append(functions, function)
				}
			}
		}
		return functions
	case []map[string]any:
		functions := make([]dto.FunctionRequest, 0, len(typed))
		for _, item := range typed {
			var function dto.FunctionRequest
			if data, err := json.Marshal(item); err == nil {
				if err := json.Unmarshal(data, &function); err == nil {
					functions = append(functions, function)
				}
			}
		}
		return functions
	case map[string]any:
		var function dto.FunctionRequest
		if data, err := json.Marshal(typed); err == nil {
			if err := json.Unmarshal(data, &function); err == nil {
				return []dto.FunctionRequest{function}
			}
		}
	case json.RawMessage:
		var functions []dto.FunctionRequest
		if err := json.Unmarshal(typed, &functions); err == nil {
			return functions
		}
	}
	return nil
}

type pendingGeminiToolCall struct {
	ID   string
	Name string
}

func GeminiToOpenAIRequest(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openaiRequest := &dto.GeneralOpenAIRequest{
		Model:  info.UpstreamModelName,
		Stream: info.IsStream,
	}

	usedToolCallIDs, err := collectGeminiFunctionCallIDs(geminiRequest)
	if err != nil {
		return nil, err
	}

	// 转换 messages
	var messages []dto.Message
	var pendingToolCalls []pendingGeminiToolCall
	nextGeneratedToolCallID := 1
	for _, content := range geminiRequest.Contents {
		message := dto.Message{
			Role: convertGeminiRoleToOpenAI(content.Role),
		}

		if content.Role == "user" {
			hasPromptPart := false
			for _, part := range content.Parts {
				if part.FunctionResponse == nil {
					hasPromptPart = true
					break
				}
			}
			if hasPromptPart {
				pendingToolCalls = nil
			}
		}

		// 处理 parts
		var mediaContents []dto.MediaContent
		var toolCalls []dto.ToolCallRequest
		for _, part := range content.Parts {
			if part.Text != "" {
				mediaContent := dto.MediaContent{
					Type: "text",
					Text: part.Text,
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.InlineData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      fmt.Sprintf("data:%s;base64,%s", part.InlineData.MimeType, part.InlineData.Data),
						Detail:   "auto",
						MimeType: part.InlineData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FileData != nil {
				mediaContent := dto.MediaContent{
					Type: "image_url",
					ImageUrl: &dto.MessageImageUrl{
						Url:      part.FileData.FileUri,
						Detail:   "auto",
						MimeType: part.FileData.MimeType,
					},
				}
				mediaContents = append(mediaContents, mediaContent)
			} else if part.FunctionCall != nil {
				// 处理 Gemini 的工具调用
				toolCallID := part.FunctionCall.ID
				if toolCallID == "" {
					toolCallID = nextUniqueGeminiToolCallID(usedToolCallIDs, &nextGeneratedToolCallID)
					usedToolCallIDs[toolCallID] = struct{}{}
				}
				toolCall := dto.ToolCallRequest{
					ID:   toolCallID,
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      part.FunctionCall.FunctionName,
						Arguments: toJSONString(part.FunctionCall.Arguments),
					},
				}
				toolCalls = append(toolCalls, toolCall)
				pendingToolCalls = append(pendingToolCalls, pendingGeminiToolCall{
					ID:   toolCallID,
					Name: part.FunctionCall.FunctionName,
				})
			} else if part.FunctionResponse != nil {
				toolCallID, ok := consumePendingGeminiToolCallID(&pendingToolCalls, part.FunctionResponse.Name)
				if !ok {
					return nil, fmt.Errorf("function response %q has no preceding function call to map to an OpenAI tool_call_id", part.FunctionResponse.Name)
				}
				// 处理 Gemini 的工具响应，创建单独的 tool 消息
				toolMessage := dto.Message{
					Role:       "tool",
					ToolCallId: toolCallID,
				}
				toolMessage.SetStringContent(toJSONString(part.FunctionResponse.Response))
				messages = append(messages, toolMessage)
			}
		}

		// 设置消息内容
		if len(toolCalls) > 0 {
			// 如果有工具调用，设置工具调用
			message.SetToolCalls(toolCalls)
		} else if len(mediaContents) == 1 && mediaContents[0].Type == "text" {
			// 如果只有一个文本内容，直接设置字符串
			message.Content = mediaContents[0].Text
		} else if len(mediaContents) > 0 {
			// 如果有多个内容或包含媒体，设置为数组
			message.SetMediaContent(mediaContents)
		}

		// 只有当消息有内容或工具调用时才添加
		if len(message.ParseContent()) > 0 || len(message.ToolCalls) > 0 {
			messages = append(messages, message)
		}
	}

	openaiRequest.Messages = messages

	if geminiRequest.GenerationConfig.Temperature != nil {
		openaiRequest.Temperature = geminiRequest.GenerationConfig.Temperature
	}
	if geminiRequest.GenerationConfig.TopP > 0 {
		openaiRequest.TopP = common.GetPointer(geminiRequest.GenerationConfig.TopP)
	}
	if geminiRequest.GenerationConfig.TopK > 0 {
		openaiRequest.TopK = int(geminiRequest.GenerationConfig.TopK)
	}
	if geminiRequest.GenerationConfig.MaxOutputTokens > 0 {
		openaiRequest.MaxTokens = geminiRequest.GenerationConfig.MaxOutputTokens
	}
	// gemini stop sequences 最多 5 个，openai stop 最多 4 个
	if stops := geminiRequest.GenerationConfig.StopSequences; len(stops) > 0 {
		openaiRequest.Stop = stops[:min(len(stops), 4)]
	}
	if geminiRequest.GenerationConfig.CandidateCount > 0 {
		openaiRequest.N = geminiRequest.GenerationConfig.CandidateCount
	}

	// 转换工具调用
	if len(geminiRequest.GetTools()) > 0 {
		var tools []dto.ToolCallRequest
		for _, tool := range geminiRequest.GetTools() {
			if tool.FunctionDeclarations != nil {
				// 将 Gemini 的 FunctionDeclarations 转换为 OpenAI 的 ToolCallRequest
				functionDeclarations := normalizeGeminiFunctionDeclarations(tool.FunctionDeclarations)
				for _, function := range functionDeclarations {
					openAITool := dto.ToolCallRequest{
						Type: "function",
						Function: dto.FunctionRequest{
							Name:        function.Name,
							Description: function.Description,
							Parameters:  function.Parameters,
						},
					}
					tools = append(tools, openAITool)
				}
			}
		}
		if len(tools) > 0 {
			openaiRequest.Tools = tools
		}
	}

	// gemini system instructions
	if geminiRequest.SystemInstructions != nil {
		// 将系统指令作为第一条消息插入
		systemMessage := dto.Message{
			Role:    "system",
			Content: extractTextFromGeminiParts(geminiRequest.SystemInstructions.Parts),
		}
		openaiRequest.Messages = append([]dto.Message{systemMessage}, openaiRequest.Messages...)
	}

	return openaiRequest, nil
}

func consumePendingGeminiToolCallID(pending *[]pendingGeminiToolCall, functionName string) (string, bool) {
	if len(*pending) == 0 {
		return "", false
	}

	if functionName != "" {
		for i, toolCall := range *pending {
			if toolCall.Name == functionName {
				*pending = append((*pending)[:i], (*pending)[i+1:]...)
				return toolCall.ID, true
			}
		}
	}

	toolCall := (*pending)[0]
	*pending = (*pending)[1:]
	return toolCall.ID, true
}

func collectGeminiFunctionCallIDs(geminiRequest *dto.GeminiChatRequest) (map[string]struct{}, error) {
	used := make(map[string]struct{})
	for _, content := range geminiRequest.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall == nil || part.FunctionCall.ID == "" {
				continue
			}
			if _, exists := used[part.FunctionCall.ID]; exists {
				return nil, fmt.Errorf("duplicate Gemini functionCall.id %q cannot be faithfully relayed to an OpenAI-compatible upstream", part.FunctionCall.ID)
			}
			used[part.FunctionCall.ID] = struct{}{}
		}
	}
	return used, nil
}

func nextUniqueGeminiToolCallID(used map[string]struct{}, next *int) string {
	for {
		candidate := fmt.Sprintf("call_%d", *next)
		*next++
		if _, exists := used[candidate]; exists {
			continue
		}
		return candidate
	}
}

func convertGeminiRoleToOpenAI(geminiRole string) string {
	switch geminiRole {
	case "user":
		return "user"
	case "model":
		return "assistant"
	case "function":
		return "function"
	default:
		return "user"
	}
}

func extractTextFromGeminiParts(parts []dto.GeminiPart) string {
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// ResponseOpenAI2Gemini 将 OpenAI 响应转换为 Gemini 格式
func ResponseOpenAI2Gemini(openAIResponse *dto.OpenAITextResponse, info *relaycommon.RelayInfo) *dto.GeminiChatResponse {
	geminiResponse := &dto.GeminiChatResponse{
		Candidates: make([]dto.GeminiChatCandidate, 0, len(openAIResponse.Choices)),
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     openAIResponse.PromptTokens,
			CandidatesTokenCount: openAIResponse.CompletionTokens,
			TotalTokenCount:      openAIResponse.PromptTokens + openAIResponse.CompletionTokens,
		},
	}

	for _, choice := range openAIResponse.Choices {
		candidate := dto.GeminiChatCandidate{
			Index:         int64(choice.Index),
			SafetyRatings: []dto.GeminiChatSafetyRating{},
		}

		// 设置结束原因
		var finishReason string
		switch choice.FinishReason {
		case "stop":
			finishReason = "STOP"
		case "length":
			finishReason = "MAX_TOKENS"
		case "content_filter":
			finishReason = "SAFETY"
		case "tool_calls":
			finishReason = "STOP"
		default:
			finishReason = "STOP"
		}
		candidate.FinishReason = &finishReason

		// 转换消息内容
		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		// 处理工具调用
		toolCalls := choice.Message.ParseToolCalls()
		if len(toolCalls) > 0 {
			for _, toolCall := range toolCalls {
				// 解析参数
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				part := dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				}
				content.Parts = append(content.Parts, part)
			}
		} else {
			// 处理文本内容
			textContent := choice.Message.StringContent()
			if textContent != "" {
				part := dto.GeminiPart{
					Text: textContent,
				}
				content.Parts = append(content.Parts, part)
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	return geminiResponse
}

// StreamResponseOpenAI2Gemini 将 OpenAI 流式响应转换为 Gemini 格式
func StreamResponseOpenAI2Gemini(openAIResponse *dto.ChatCompletionsStreamResponse, info *relaycommon.RelayInfo) *dto.GeminiChatResponse {
	// 检查是否有实际内容或结束标志
	hasContent := false
	hasFinishReason := false
	for _, choice := range openAIResponse.Choices {
		if len(choice.Delta.GetContentString()) > 0 || (choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0) {
			hasContent = true
		}
		if choice.FinishReason != nil {
			hasFinishReason = true
		}
	}

	// 如果没有实际内容且没有结束标志，跳过。主要针对 openai 流响应开头的空数据
	if !hasContent && !hasFinishReason {
		return nil
	}

	geminiResponse := &dto.GeminiChatResponse{
		Candidates: make([]dto.GeminiChatCandidate, 0, len(openAIResponse.Choices)),
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     0,
			CandidatesTokenCount: 0, // 流式响应中可能没有完整的 usage 信息
			TotalTokenCount:      0,
		},
	}

	for _, choice := range openAIResponse.Choices {
		candidate := dto.GeminiChatCandidate{
			Index:         int64(choice.Index),
			SafetyRatings: []dto.GeminiChatSafetyRating{},
		}

		// 设置结束原因
		if choice.FinishReason != nil {
			var finishReason string
			switch *choice.FinishReason {
			case "stop":
				finishReason = "STOP"
			case "length":
				finishReason = "MAX_TOKENS"
			case "content_filter":
				finishReason = "SAFETY"
			case "tool_calls":
				finishReason = "STOP"
			default:
				finishReason = "STOP"
			}
			candidate.FinishReason = &finishReason
		}

		// 转换消息内容
		content := dto.GeminiChatContent{
			Role:  "model",
			Parts: make([]dto.GeminiPart, 0),
		}

		// 处理工具调用
		if choice.Delta.ToolCalls != nil {
			for _, toolCall := range choice.Delta.ToolCalls {
				// 解析参数
				var args map[string]interface{}
				if toolCall.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
						args = map[string]interface{}{"arguments": toolCall.Function.Arguments}
					}
				} else {
					args = make(map[string]interface{})
				}

				part := dto.GeminiPart{
					FunctionCall: &dto.FunctionCall{
						FunctionName: toolCall.Function.Name,
						Arguments:    args,
					},
				}
				content.Parts = append(content.Parts, part)
			}
		} else {
			// 处理文本内容
			textContent := choice.Delta.GetContentString()
			if textContent != "" {
				part := dto.GeminiPart{
					Text: textContent,
				}
				content.Parts = append(content.Parts, part)
			}
		}

		candidate.Content = content
		geminiResponse.Candidates = append(geminiResponse.Candidates, candidate)
	}

	return geminiResponse
}
