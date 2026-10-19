package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

// ChatCompletionsToResponsesRequest converts OpenAI chat/completions request payload
// into OpenAI responses payload for responses-first compatibility path.
func ChatCompletionsToResponsesRequest(request *dto.GeneralOpenAIRequest) (*dto.OpenAIResponsesRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}

	responseReq := &dto.OpenAIResponsesRequest{
		Model:            request.Model,
		Stream:           request.Stream,
		User:             request.User,
		ServiceTier:      request.ServiceTier,
		SafetyIdentifier: request.SafetyIdentifier,
	}

	if request.MaxCompletionTokens > 0 {
		responseReq.MaxOutputTokens = request.MaxCompletionTokens
	} else {
		responseReq.MaxOutputTokens = request.MaxTokens
	}

	if request.Temperature != nil {
		responseReq.Temperature = request.Temperature
	}
	if request.TopP != nil {
		responseReq.TopP = request.TopP
	}

	if len(request.Metadata) > 0 {
		responseReq.Metadata = request.Metadata
	}
	if len(request.Store) > 0 {
		responseReq.Store = request.Store
	}
	if request.PromptCacheKey != "" {
		raw, err := common.Marshal(request.PromptCacheKey)
		if err != nil {
			return nil, fmt.Errorf("marshal prompt_cache_key failed: %w", err)
		}
		responseReq.PromptCacheKey = raw
	}

	if request.ParallelToolCalls != nil {
		raw, err := common.Marshal(*request.ParallelToolCalls)
		if err != nil {
			return nil, fmt.Errorf("marshal parallel_tool_calls failed: %w", err)
		}
		responseReq.ParallelToolCalls = raw
	}

	if request.ResponseFormat != nil {
		textRaw, err := convertChatResponseFormatToResponsesText(request.ResponseFormat)
		if err != nil {
			return nil, err
		}
		responseReq.Text = textRaw
	}

	if request.ReasoningEffort != "" {
		responseReq.Reasoning = &dto.Reasoning{
			Effort: request.ReasoningEffort,
		}
	}

	toolsRaw, err := convertChatToolsToResponsesTools(request.Tools)
	if err != nil {
		return nil, err
	}
	responseReq.Tools = toolsRaw

	if request.WebSearchOptions != nil {
		responseReq.Tools, err = appendResponsesTool(responseReq.Tools, responsesWebSearchToolFromChat(request.WebSearchOptions))
		if err != nil {
			return nil, err
		}
	}

	toolChoiceRaw, err := convertChatToolChoiceToResponses(request.ToolChoice)
	if err != nil {
		return nil, err
	}
	if len(toolChoiceRaw) == 0 && request.WebSearchOptions != nil && len(request.Tools) == 0 {
		toolChoiceRaw, err = defaultResponsesWebSearchToolChoice()
		if err != nil {
			return nil, err
		}
	}
	responseReq.ToolChoice = toolChoiceRaw

	inputRaw, instructionsRaw, err := convertChatMessagesToResponsesInput(request.Messages)
	if err != nil {
		return nil, err
	}
	if len(inputRaw) > 0 {
		responseReq.Input = inputRaw
	}
	if len(instructionsRaw) > 0 {
		responseReq.Instructions = instructionsRaw
	}

	return responseReq, nil
}

// OpenAIResponsesToChatCompletionsResponse converts a responses output envelope
// back to chat/completions envelope for compatibility endpoint.
func OpenAIResponsesToChatCompletionsResponse(response *dto.OpenAIResponsesResponse, fallbackModel string) *dto.OpenAITextResponse {
	if response == nil {
		response = &dto.OpenAIResponsesResponse{}
	}

	created := int64(response.CreatedAt)
	if created <= 0 {
		created = time.Now().Unix()
	}

	model := response.Model
	if model == "" {
		model = fallbackModel
	}

	message := dto.Message{
		Role: "assistant",
	}
	incomplete := strings.EqualFold(response.Status, "incomplete")
	outputText, annotations := extractResponsesOutputTextAndAnnotations(response.Output)
	toolCalls := extractResponsesFunctionCalls(response.Output)
	if len(annotations) > 0 {
		message.Annotations = annotations
	}
	if outputText != "" {
		message.SetStringContent(outputText)
	} else if len(toolCalls) > 0 && !incomplete {
		message.SetNullContent()
	} else {
		message.SetStringContent("")
	}
	if len(toolCalls) > 0 && !incomplete {
		message.SetToolCalls(toolCalls)
	}

	finishReason := "stop"
	if incomplete {
		finishReason = "length"
	} else if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	usage := mapResponsesUsageToChatUsage(response.Usage)

	return &dto.OpenAITextResponse{
		Id:      response.ID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      message,
				FinishReason: finishReason,
			},
		},
		Usage: usage,
	}
}

func convertChatResponseFormatToResponsesText(format *dto.ResponseFormat) (json.RawMessage, error) {
	if format == nil {
		return nil, nil
	}
	formatType := strings.TrimSpace(format.Type)
	if formatType == "" && len(format.JsonSchema) == 0 {
		return nil, nil
	}

	responsesFormat := map[string]any{}
	if len(format.JsonSchema) > 0 && (formatType == "" || strings.EqualFold(formatType, "json_schema")) {
		if err := common.Unmarshal(format.JsonSchema, &responsesFormat); err != nil {
			return nil, fmt.Errorf("unmarshal response_format json_schema failed: %w", err)
		}
		responsesFormat["type"] = "json_schema"
	} else {
		responsesFormat["type"] = formatType
	}

	raw, err := common.Marshal(map[string]any{"format": responsesFormat})
	if err != nil {
		return nil, fmt.Errorf("marshal responses text format failed: %w", err)
	}
	return raw, nil
}

func convertChatToolsToResponsesTools(tools []dto.ToolCallRequest) (json.RawMessage, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	converted := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		item := map[string]any{
			"type": tool.Type,
		}
		if strings.EqualFold(tool.Type, "function") {
			if tool.Function.Name == "" {
				return nil, fmt.Errorf("tool function.name is required")
			}
			item["name"] = tool.Function.Name
			if tool.Function.Description != "" {
				item["description"] = tool.Function.Description
			}
			if tool.Function.Parameters != nil {
				item["parameters"] = tool.Function.Parameters
			}
		}
		converted = append(converted, item)
	}
	raw, err := common.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("marshal responses tools failed: %w", err)
	}
	return raw, nil
}

func appendResponsesTool(raw json.RawMessage, tool map[string]any) (json.RawMessage, error) {
	if len(tool) == 0 {
		return raw, nil
	}
	tools := make([]map[string]any, 0)
	if len(raw) > 0 {
		if err := common.Unmarshal(raw, &tools); err != nil {
			return nil, fmt.Errorf("unmarshal responses tools failed: %w", err)
		}
	}
	tools = append(tools, tool)
	out, err := common.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("marshal responses tools failed: %w", err)
	}
	return out, nil
}

func responsesWebSearchToolFromChat(options *dto.WebSearchOptions) map[string]any {
	tool := map[string]any{
		"type": dto.BuildInToolWebSearchPreview,
	}
	if options == nil {
		return tool
	}
	if options.SearchContextSize != "" {
		tool["search_context_size"] = options.SearchContextSize
	}
	if len(options.UserLocation) > 0 {
		if userLocation := flattenChatWebSearchUserLocation(options.UserLocation); userLocation != nil {
			tool["user_location"] = userLocation
		}
	}
	return tool
}

func flattenChatWebSearchUserLocation(raw json.RawMessage) map[string]any {
	var userLocation map[string]any
	if err := common.Unmarshal(raw, &userLocation); err != nil {
		return nil
	}
	approximate, ok := userLocation["approximate"].(map[string]any)
	if !ok {
		return userLocation
	}
	flattened := make(map[string]any, len(approximate)+1)
	locationType := common.Interface2String(userLocation["type"])
	if locationType == "" {
		locationType = "approximate"
	}
	flattened["type"] = locationType
	for key, value := range approximate {
		flattened[key] = value
	}
	return flattened
}

func defaultResponsesWebSearchToolChoice() (json.RawMessage, error) {
	raw, err := common.Marshal(map[string]string{
		"type": dto.BuildInToolWebSearchPreview,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal web_search tool_choice failed: %w", err)
	}
	return raw, nil
}

func convertChatToolChoiceToResponses(toolChoice any) (json.RawMessage, error) {
	if toolChoice == nil {
		return nil, nil
	}
	switch value := toolChoice.(type) {
	case string:
		raw, err := common.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal tool_choice string failed: %w", err)
		}
		return raw, nil
	case map[string]any:
		if strings.EqualFold(common.Interface2String(value["type"]), "function") {
			if fn, ok := value["function"].(map[string]any); ok {
				name := common.Interface2String(fn["name"])
				if name != "" {
					normalized := map[string]any{
						"type": "function",
						"name": name,
					}
					raw, err := common.Marshal(normalized)
					if err != nil {
						return nil, fmt.Errorf("marshal normalized tool_choice failed: %w", err)
					}
					return raw, nil
				}
			}
		}
		raw, err := common.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal tool_choice map failed: %w", err)
		}
		return raw, nil
	default:
		raw, err := common.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal tool_choice failed: %w", err)
		}
		return raw, nil
	}
}

func convertChatMessagesToResponsesInput(messages []dto.Message) (json.RawMessage, json.RawMessage, error) {
	if len(messages) == 0 {
		return nil, nil, fmt.Errorf("messages is required")
	}

	var (
		instructions []string
		inputItems   []map[string]any
	)

	for idx, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" || role == "developer" {
			text := extractMessageText(msg)
			if strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
			continue
		}

		if role == "tool" {
			toolOutput, err := convertChatToolMessageToResponsesItem(msg, idx)
			if err != nil {
				return nil, nil, err
			}
			inputItems = append(inputItems, toolOutput)
			continue
		}
		if role == "" {
			role = "user"
		}

		item, err := convertChatMessageToResponsesInputItem(role, msg, idx)
		if err != nil {
			return nil, nil, err
		}
		if item != nil {
			inputItems = append(inputItems, item)
		}
		if role == "assistant" {
			toolCallItems, err := convertChatToolCallsToResponsesItems(msg.ParseToolCalls(), idx)
			if err != nil {
				return nil, nil, err
			}
			inputItems = append(inputItems, toolCallItems...)
		}
	}

	if len(inputItems) == 0 {
		return nil, nil, fmt.Errorf("messages are empty after responses input conversion")
	}

	inputRaw, err := common.Marshal(inputItems)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal responses input failed: %w", err)
	}

	var instructionsRaw json.RawMessage
	if len(instructions) > 0 {
		instructionsRaw, err = common.Marshal(strings.Join(instructions, "\n\n"))
		if err != nil {
			return nil, nil, fmt.Errorf("marshal responses instructions failed: %w", err)
		}
	}
	return inputRaw, instructionsRaw, nil
}

func convertChatMessageToResponsesInputItem(role string, msg dto.Message, idx int) (map[string]any, error) {
	content := make([]map[string]any, 0)
	if msg.IsStringContent() {
		text := msg.StringContent()
		if strings.TrimSpace(text) != "" {
			content = append(content, map[string]any{
				"type": responsesTextContentType(role),
				"text": text,
			})
		}
	} else {
		for _, part := range msg.ParseContent() {
			switch part.Type {
			case dto.ContentTypeText:
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				content = append(content, map[string]any{
					"type": responsesTextContentType(role),
					"text": part.Text,
				})
			case dto.ContentTypeImageURL:
				image := part.GetImageMedia()
				if image == nil || image.Url == "" {
					continue
				}
				content = append(content, map[string]any{
					"type":      "input_image",
					"image_url": image.Url,
				})
			case dto.ContentTypeFile:
				file := part.GetFile()
				if file == nil {
					continue
				}
				fileItem := map[string]any{
					"type": "input_file",
				}
				if file.FileId != "" {
					fileItem["file_id"] = file.FileId
				} else if file.FileData != "" {
					fileItem["file_data"] = file.FileData
					if file.FileName != "" {
						fileItem["filename"] = file.FileName
					}
				}
				content = append(content, fileItem)
			case dto.ContentTypeInputAudio, dto.ContentTypeVideoUrl:
				return nil, fmt.Errorf("message[%d] contains unsupported content type %q for responses-first mapper", idx, part.Type)
			}
		}
	}

	if len(content) == 0 {
		return nil, nil
	}
	return map[string]any{
		"role":    role,
		"content": content,
	}, nil
}

func convertChatToolCallsToResponsesItems(toolCalls []dto.ToolCallRequest, messageIndex int) ([]map[string]any, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	items := make([]map[string]any, 0, len(toolCalls))
	for callIndex, toolCall := range toolCalls {
		if !strings.EqualFold(toolCall.Type, "function") {
			return nil, fmt.Errorf("message[%d].tool_calls[%d] type %q is not supported in responses-first mapper", messageIndex, callIndex, toolCall.Type)
		}
		callID := strings.TrimSpace(toolCall.ID)
		if callID == "" {
			callID = fmt.Sprintf("call_%d_%d", messageIndex, callIndex)
		}
		if strings.TrimSpace(toolCall.Function.Name) == "" {
			return nil, fmt.Errorf("message[%d].tool_calls[%d].function.name is required", messageIndex, callIndex)
		}
		items = append(items, map[string]any{
			"type":      dto.ResponsesOutputTypeFunctionCall,
			"call_id":   callID,
			"name":      toolCall.Function.Name,
			"arguments": toolCall.Function.Arguments,
		})
	}
	return items, nil
}

func convertChatToolMessageToResponsesItem(msg dto.Message, idx int) (map[string]any, error) {
	callID := strings.TrimSpace(msg.ToolCallId)
	if callID == "" {
		return nil, fmt.Errorf("message[%d].tool_call_id is required for responses-first mapper", idx)
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  extractMessageText(msg),
	}, nil
}

func extractMessageText(msg dto.Message) string {
	if msg.IsStringContent() {
		return msg.StringContent()
	}
	parts := msg.ParseContent()
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != dto.ContentTypeText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func responsesTextContentType(role string) string {
	if strings.EqualFold(role, "assistant") {
		return "output_text"
	}
	return "input_text"
}

func extractResponsesOutputTextAndAnnotations(outputs []dto.ResponsesOutput) (string, json.RawMessage) {
	var builder strings.Builder
	annotations := make([]interface{}, 0)
	for _, output := range outputs {
		for _, content := range output.Content {
			switch content.Type {
			case "text", "output_text", "input_text":
				if strings.TrimSpace(content.Text) == "" {
					continue
				}
				if builder.Len() > 0 {
					builder.WriteString("\n")
				}
				builder.WriteString(content.Text)
				if len(content.Annotations) > 0 {
					for _, annotation := range content.Annotations {
						annotations = append(annotations, responsesAnnotationToChatAnnotation(annotation))
					}
				}
			}
		}
	}
	if len(annotations) == 0 {
		return builder.String(), nil
	}
	raw, err := common.Marshal(annotations)
	if err != nil {
		return builder.String(), nil
	}
	return builder.String(), raw
}

func responsesAnnotationToChatAnnotation(annotation interface{}) interface{} {
	annotationMap, ok := annotation.(map[string]interface{})
	if !ok || common.Interface2String(annotationMap["type"]) != "url_citation" {
		return annotation
	}
	if _, alreadyChatShape := annotationMap["url_citation"]; alreadyChatShape {
		return annotation
	}

	citation := make(map[string]interface{}, len(annotationMap))
	for key, value := range annotationMap {
		if key == "type" {
			continue
		}
		citation[key] = value
	}
	return map[string]interface{}{
		"type":         "url_citation",
		"url_citation": citation,
	}
}

func extractResponsesFunctionCalls(outputs []dto.ResponsesOutput) []dto.ToolCallResponse {
	toolCalls := make([]dto.ToolCallResponse, 0)
	for _, output := range outputs {
		if output.Type != dto.ResponsesOutputTypeFunctionCall {
			continue
		}
		callID := output.CallID
		if callID == "" {
			callID = output.ID
		}
		toolCalls = append(toolCalls, dto.ToolCallResponse{
			ID:   callID,
			Type: "function",
			Function: dto.FunctionResponse{
				Name:      output.Name,
				Arguments: output.Arguments,
			},
		})
	}
	return toolCalls
}

func mapResponsesUsageToChatUsage(usage *dto.Usage) dto.Usage {
	if usage == nil {
		return dto.Usage{}
	}
	usagepkg.NormalizeDTOUsageFields(usage)
	mapped := dto.Usage{
		PromptTokens:         usage.PromptTokens,
		CompletionTokens:     usage.CompletionTokens,
		TotalTokens:          usage.TotalTokens,
		PromptCacheHitTokens: usage.PromptCacheHitTokens,
		PromptTokensDetails:  usage.PromptTokensDetails,
		CompletionTokenDetails: dto.OutputTokenDetails{
			TextTokens:      usage.CompletionTokenDetails.TextTokens,
			AudioTokens:     usage.CompletionTokenDetails.AudioTokens,
			ImageTokens:     usage.CompletionTokenDetails.ImageTokens,
			ReasoningTokens: usage.CompletionTokenDetails.ReasoningTokens,
		},
	}

	if mapped.PromptTokens == 0 && usage.InputTokens != 0 {
		mapped.PromptTokens = usage.InputTokens
	}
	if mapped.CompletionTokens == 0 && usage.OutputTokens != 0 {
		mapped.CompletionTokens = usage.OutputTokens
	}
	if mapped.TotalTokens == 0 {
		mapped.TotalTokens = mapped.PromptTokens + mapped.CompletionTokens
	}
	if usage.InputTokensDetails != nil && mapped.PromptTokensDetails.CachedTokens == 0 {
		mapped.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
	}
	if mapped.PromptTokensDetails.CachedTokens == 0 && mapped.PromptCacheHitTokens > 0 {
		mapped.PromptTokensDetails.CachedTokens = mapped.PromptCacheHitTokens
	}
	usagepkg.NormalizeDTOUsageFields(&mapped)
	return mapped
}
