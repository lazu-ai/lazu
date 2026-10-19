package service

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func rebuildLoggedStreamResponseBody(info *relaycommon.RelayInfo, payload *ModelLogPayload) []byte {
	if payload == nil {
		return nil
	}
	if !payload.IsStream || len(payload.ResponseChunks) == 0 {
		return payload.ResponseBody
	}
	if body := rebuildChatCompletionStreamBody(info, payload); len(body) > 0 {
		return body
	}
	if body := rebuildResponsesStreamBody(info, payload); len(body) > 0 {
		return body
	}
	return payload.ResponseBody
}

type streamChoiceAccumulator struct {
	role         string
	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	toolCalls    map[int]*streamToolCallAccumulator
}

type streamToolCallAccumulator struct {
	id               string
	callType         string
	name             string
	thoughtSignature string
	arguments        strings.Builder
}

func rebuildChatCompletionStreamBody(info *relaycommon.RelayInfo, payload *ModelLogPayload) []byte {
	accumulators := make(map[int]*streamChoiceAccumulator)
	var (
		responseID string
		model      string
		created    int64
	)
	usage := usageFromAny(payload.Usage)
	parsedChunk := false

	for _, rawChunk := range payload.ResponseChunks {
		var chunk dto.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(rawChunk), &chunk); err != nil {
			return nil
		}
		if chunk.Object != "chat.completion.chunk" {
			return nil
		}
		parsedChunk = true
		if chunk.Id != "" {
			responseID = chunk.Id
		}
		if chunk.Created > 0 {
			created = chunk.Created
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if usage == nil && chunk.Usage != nil {
			copyUsage := *chunk.Usage
			usage = &copyUsage
		}

		for _, choice := range chunk.Choices {
			acc := accumulators[choice.Index]
			if acc == nil {
				acc = &streamChoiceAccumulator{
					toolCalls: make(map[int]*streamToolCallAccumulator),
				}
				accumulators[choice.Index] = acc
			}
			if choice.Delta.Role != "" {
				acc.role = choice.Delta.Role
			}
			if content := choice.Delta.GetContentString(); content != "" {
				acc.content.WriteString(content)
			}
			if reasoning := choice.Delta.GetReasoningContent(); reasoning != "" {
				acc.reasoning.WriteString(reasoning)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				acc.finishReason = *choice.FinishReason
			}
			for callPos, toolCall := range choice.Delta.ToolCalls {
				toolIndex := callPos
				if toolCall.Index != nil {
					toolIndex = *toolCall.Index
				}
				toolAcc := acc.toolCalls[toolIndex]
				if toolAcc == nil {
					toolAcc = &streamToolCallAccumulator{}
					acc.toolCalls[toolIndex] = toolAcc
				}
				if toolCall.ID != "" {
					toolAcc.id = toolCall.ID
				}
				if callType, ok := toolCall.Type.(string); ok && callType != "" {
					toolAcc.callType = callType
				}
				if toolCall.Function.Name != "" {
					toolAcc.name = toolCall.Function.Name
				}
				if toolCall.ExtraContent != nil && toolCall.ExtraContent.Google != nil &&
					toolCall.ExtraContent.Google.ThoughtSignature != "" {
					toolAcc.thoughtSignature = toolCall.ExtraContent.Google.ThoughtSignature
				}
				if toolCall.Function.Arguments != "" {
					toolAcc.arguments.WriteString(toolCall.Function.Arguments)
				}
			}
		}
	}

	if !parsedChunk || len(accumulators) == 0 {
		return nil
	}

	if model == "" && info != nil {
		if info.ChannelMeta != nil && info.UpstreamModelName != "" {
			model = info.UpstreamModelName
		} else {
			model = info.OriginModelName
		}
	}

	indexes := make([]int, 0, len(accumulators))
	for index := range accumulators {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	choices := make([]dto.OpenAITextResponseChoice, 0, len(indexes))
	for _, index := range indexes {
		acc := accumulators[index]
		message := dto.Message{
			Role: acc.role,
		}
		if message.Role == "" {
			message.Role = "assistant"
		}
		if acc.content.Len() > 0 {
			message.SetStringContent(acc.content.String())
		} else {
			message.SetNullContent()
		}
		if acc.reasoning.Len() > 0 {
			message.ReasoningContent = acc.reasoning.String()
		}
		if len(acc.toolCalls) > 0 {
			message.SetToolCalls(buildToolCalls(acc.toolCalls))
			if acc.content.Len() == 0 {
				message.SetNullContent()
			}
		}

		finishReason := acc.finishReason
		if finishReason == "" {
			if len(acc.toolCalls) > 0 {
				finishReason = constant.FinishReasonToolCalls
			} else {
				finishReason = "stop"
			}
		}

		choices = append(choices, dto.OpenAITextResponseChoice{
			Index:        index,
			Message:      message,
			FinishReason: finishReason,
		})
	}

	response := dto.OpenAITextResponse{
		Id:      responseID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: choices,
	}
	if usage != nil {
		response.Usage = *usage
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil
	}
	return body
}

func buildToolCalls(accumulators map[int]*streamToolCallAccumulator) []dto.ToolCallResponse {
	indexes := make([]int, 0, len(accumulators))
	for index := range accumulators {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	toolCalls := make([]dto.ToolCallResponse, 0, len(indexes))
	for _, index := range indexes {
		acc := accumulators[index]
		callType := acc.callType
		if callType == "" {
			callType = "function"
		}
		toolIndex := index
		toolCall := dto.ToolCallResponse{
			Index: &toolIndex,
			ID:    acc.id,
			Type:  callType,
			Function: dto.FunctionResponse{
				Name:      acc.name,
				Arguments: acc.arguments.String(),
			},
		}
		if acc.thoughtSignature != "" {
			toolCall.ExtraContent = &dto.ToolCallExtraContent{
				Google: &dto.GoogleToolCallExtraContent{
					ThoughtSignature: acc.thoughtSignature,
				},
			}
		}
		toolCalls = append(toolCalls, toolCall)
	}
	return toolCalls
}

func rebuildResponsesStreamBody(info *relaycommon.RelayInfo, payload *ModelLogPayload) []byte {
	var (
		lastResponse *dto.OpenAIResponsesResponse
		outputText   strings.Builder
	)
	usage := usageFromAny(payload.Usage)
	parsedChunk := false

	for _, rawChunk := range payload.ResponseChunks {
		var chunk dto.ResponsesStreamResponse
		if err := json.Unmarshal([]byte(rawChunk), &chunk); err != nil {
			return nil
		}
		if chunk.Type == "" {
			return nil
		}
		parsedChunk = true
		if chunk.Response != nil {
			copyResponse := *chunk.Response
			lastResponse = &copyResponse
		}
		if chunk.Delta != "" {
			outputText.WriteString(chunk.Delta)
		}
	}

	if !parsedChunk {
		return nil
	}

	if lastResponse != nil {
		if outputText.Len() > 0 && !responsesOutputHasText(lastResponse) {
			lastResponse.Output = mergeResponsesOutputText(lastResponse.Output, outputText.String())
		}
		if usage != nil && lastResponse.Usage == nil {
			copyUsage := *usage
			lastResponse.Usage = &copyUsage
		}
		body, err := json.Marshal(lastResponse)
		if err != nil {
			return nil
		}
		return body
	}

	if outputText.Len() == 0 {
		return nil
	}

	response := dto.OpenAIResponsesResponse{
		Object: "response",
		Status: "completed",
		Output: buildResponsesOutputText(outputText.String()),
	}
	if info != nil {
		if info.ChannelMeta != nil && info.UpstreamModelName != "" {
			response.Model = info.UpstreamModelName
		} else {
			response.Model = info.OriginModelName
		}
	}
	if usage != nil {
		copyUsage := *usage
		response.Usage = &copyUsage
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil
	}
	return body
}

func responsesOutputHasText(response *dto.OpenAIResponsesResponse) bool {
	if response == nil {
		return false
	}
	for _, output := range response.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) != "" {
				return true
			}
		}
	}
	return false
}

func buildResponsesOutputText(text string) []dto.ResponsesOutput {
	return []dto.ResponsesOutput{
		{
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []dto.ResponsesOutputContent{
				{
					Type: "output_text",
					Text: text,
				},
			},
		},
	}
}

func mergeResponsesOutputText(outputs []dto.ResponsesOutput, text string) []dto.ResponsesOutput {
	if strings.TrimSpace(text) == "" {
		return outputs
	}
	if len(outputs) == 0 {
		return buildResponsesOutputText(text)
	}

	merged := append([]dto.ResponsesOutput(nil), outputs...)
	for index := range merged {
		if merged[index].Type != "message" {
			continue
		}
		if merged[index].Role == "" {
			merged[index].Role = "assistant"
		}
		if merged[index].Status == "" {
			merged[index].Status = "completed"
		}
		merged[index].Content = append(merged[index].Content, dto.ResponsesOutputContent{
			Type: "output_text",
			Text: text,
		})
		return merged
	}

	return append(merged, buildResponsesOutputText(text)...)
}

func usageFromAny(value any) *dto.Usage {
	switch typed := value.(type) {
	case nil:
		return nil
	case *dto.Usage:
		if typed == nil {
			return nil
		}
		copyUsage := *typed
		return &copyUsage
	case dto.Usage:
		copyUsage := typed
		return &copyUsage
	default:
		data, err := json.Marshal(typed)
		if err != nil || len(data) == 0 {
			return nil
		}
		var usage dto.Usage
		if err := json.Unmarshal(data, &usage); err != nil {
			return nil
		}
		return &usage
	}
}
