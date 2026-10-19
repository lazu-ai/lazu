package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
)

const openAIUpstreamModel = "gpt-6-luna"

func openAIRequest(r *dto.DecisionRequest, upstreamModel string) (*dto.OpenAIDecisionRequest, error) {
	if upstreamModel == dto.OpenAIDecisionModel {
		upstreamModel = openAIUpstreamModel
	}
	if upstreamModel != openAIUpstreamModel {
		return nil, types.NewError(fmt.Errorf("unsupported official decision model"), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
	}
	if r.OpenAI != nil {
		native := *r.OpenAI
		native.Model = upstreamModel
		return &native, nil
	}
	questions, err := openAIQuestions(r.Questions)
	if err != nil {
		return nil, err
	}
	var input interface{} = r.InputText()
	if len(r.ImageInfo) > 0 {
		parts := make([]map[string]interface{}, 0, 1+len(r.ImageInfo))
		if text := r.InputText(); text != "" {
			parts = append(parts, map[string]interface{}{"type": "input_text", "text": text})
		}
		for _, img := range r.ImageInfo {
			parts = append(parts, map[string]interface{}{"type": "input_image", "image_url": img.DataURL})
		}
		input = []map[string]interface{}{{"role": "user", "content": parts}}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return &dto.OpenAIDecisionRequest{Model: upstreamModel, Input: raw, Questions: questions}, nil
}

// Legacy business JSON becomes literal text without a float64 round trip.
func decisionText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil {
		return compact.String()
	}
	return string(raw)
}

func openAIQuestions(legacy map[string]json.RawMessage) ([]dto.OpenAIDecisionQuestion, error) {
	ids := make([]string, 0, len(legacy))
	for id := range legacy {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	questions := make([]dto.OpenAIDecisionQuestion, 0, len(ids))
	for _, id := range ids {
		q, err := dto.ParseDecisionQuestion(legacy[id])
		if err != nil {
			return nil, err
		}
		name := id
		native := dto.OpenAIDecisionQuestion{Name: &name, Type: q.Type, Instructions: decisionText(q.Instructions)}
		switch q.Type {
		case "noul":
			native.Type = "predicate"
			if len(q.Criteria) > 0 {
				native.Instructions += "\nTruth criteria (JSON): " + decisionText(q.Criteria)
			}
		case "choice":
			var criteria map[string]json.RawMessage
			_ = json.Unmarshal(q.Criteria, &criteria)
			keys := make([]string, 0, len(criteria))
			for key := range criteria {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				value, _ := json.Marshal(key)
				option := dto.OpenAIDecisionChoice{Value: value}
				if string(bytes.TrimSpace(criteria[key])) != "null" {
					description := decisionText(criteria[key])
					option.Description = &description
				}
				native.Choices = append(native.Choices, option)
			}
		case "score":
			var levels []json.RawMessage
			_ = json.Unmarshal(q.Criteria, &levels)
			for i, raw := range levels {
				description := decisionText(raw)
				native.Levels = append(native.Levels, dto.OpenAIDecisionLevel{Label: fmt.Sprint(i), Description: &description})
			}
		}
		questions = append(questions, native)
	}
	return questions, nil
}

func parseOpenAIResponse(body []byte, request *dto.DecisionRequest) (*Result, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || (len(fields["error"]) > 0 && string(fields["error"]) != "null") {
		return nil, invalidResponse()
	}
	var model string
	var answers []json.RawMessage
	if json.Unmarshal(fields["model"], &model) != nil || model != openAIUpstreamModel || json.Unmarshal(fields["answers"], &answers) != nil {
		return nil, invalidResponse()
	}
	var questions []dto.OpenAIDecisionQuestion
	if request.OpenAI != nil {
		questions = request.OpenAI.Questions
	} else {
		var err error
		questions, err = openAIQuestions(request.Questions)
		if err != nil {
			return nil, err
		}
	}
	if len(questions) == 0 {
		return nil, invalidResponse()
	}
	if err := dto.ValidateOpenAIDecisionAnswers(questions, answers); err != nil {
		return nil, err
	}
	usage, err := openAIUsage(fields["usage"])
	if err != nil {
		return nil, err
	}
	result := &Result{Model: model, Usage: &dto.DecisionUsage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens}, ProviderUsage: usage}
	if request.OpenAI != nil {
		fields["model"], _ = json.Marshal(request.OpenAI.Model)
		result.NativeResponse, err = json.Marshal(fields)
		return result, err
	}
	result.Answers = make(map[string]json.RawMessage, len(answers))
	for i, raw := range answers {
		var answer dto.OpenAIDecisionAnswer
		var converted map[string]json.RawMessage
		_ = json.Unmarshal(raw, &answer)
		_ = json.Unmarshal(raw, &converted)
		id := *questions[i].Name // Legacy bridge names are always present.
		q, _ := dto.ParseDecisionQuestion(request.Questions[id])
		delete(converted, "name")
		if answer.Type == "refusal" {
			converted["refusal"] = json.RawMessage("true")
		} else {
			converted["type"], _ = json.Marshal(q.Type)
			switch q.Type {
			case "noul":
				converted["noul"] = converted["probability"]
				delete(converted, "probability")
			case "choice", "score":
				probabilities := make(map[string]float64, len(answer.Probabilities))
				for _, p := range answer.Probabilities {
					var key string
					if q.Type == "choice" {
						_ = json.Unmarshal(p.Value, &key)
					} else {
						var index int
						_ = json.Unmarshal(p.Value, &index)
						key = fmt.Sprint(index)
					}
					probabilities[key] = *p.Probability
				}
				converted["probabilities"], _ = json.Marshal(probabilities)
				if q.Type == "score" {
					var levels []json.RawMessage
					_ = json.Unmarshal(q.Criteria, &levels)
					legend := make(map[string]json.RawMessage, len(levels))
					for j, level := range levels {
						legend[fmt.Sprint(j)] = level
					}
					converted["legend"], _ = json.Marshal(legend)
				}
			}
		}
		result.Answers[id], err = json.Marshal(converted)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func openAIUsage(raw json.RawMessage) (*dto.Usage, error) {
	var fields map[string]json.RawMessage
	var usage dto.Usage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &usage) != nil {
		return nil, invalidResponse()
	}
	for _, field := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		var n int
		if json.Unmarshal(fields[field], &n) != nil || string(bytes.TrimSpace(fields[field])) == "null" || n < 0 {
			return nil, invalidResponse()
		}
	}
	if usage.InputTokens <= 0 || usage.TotalTokens < usage.InputTokens || usage.TotalTokens-usage.InputTokens != usage.OutputTokens {
		return nil, invalidResponse()
	}
	// Known detail counters must not become negative or exceed their total.
	for _, detail := range []struct {
		name  string
		total int
	}{{"input_tokens_details", usage.InputTokens}, {"output_tokens_details", usage.OutputTokens}} {
		if len(fields[detail.name]) == 0 || string(bytes.TrimSpace(fields[detail.name])) == "null" {
			continue
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(fields[detail.name], &values) != nil || values == nil {
			return nil, invalidResponse()
		}
		for name, value := range values {
			if !strings.HasSuffix(name, "_tokens") {
				continue
			}
			var n int
			if json.Unmarshal(value, &n) != nil || string(bytes.TrimSpace(value)) == "null" || n < 0 || n > detail.total {
				return nil, invalidResponse()
			}
		}
	}
	usage.PromptTokens, usage.CompletionTokens = usage.InputTokens, usage.OutputTokens
	if usage.InputTokensDetails != nil {
		usage.PromptTokensDetails = *usage.InputTokensDetails
	}
	if usage.OutputTokensDetails != nil {
		usage.CompletionTokenDetails = *usage.OutputTokensDetails
	}
	usage.InputTokenSource, usage.OutputTokenSource = dto.UsageSourceProvider, dto.UsageSourceProvider
	return &usage, nil
}
