package dto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/internal/types"
)

const OpenAIDecisionModel = "gpt-6-luna-decisions"
const OpenAIDecisionWireModel = "gpt-6-luna"

func IsOpenAIDecisionModel(model string) bool { return model == OpenAIDecisionModel }

// Keep typed values and question order separate from the legacy keyed format.
// A boolean false is a different choice from the string "false".
type OpenAIDecisionRequest struct {
	Model     string                   `json:"model"`
	Input     json.RawMessage          `json:"input"`
	Questions []OpenAIDecisionQuestion `json:"questions"`
	inputText string
	imageInfo []DecisionImage
	inputSeen json.RawMessage
}

type OpenAIDecisionQuestion struct {
	Name         *string                `json:"name,omitempty"`
	Type         string                 `json:"type"`
	Instructions string                 `json:"instructions"`
	Choices      []OpenAIDecisionChoice `json:"choices,omitempty"`
	Levels       []OpenAIDecisionLevel  `json:"levels,omitempty"`
}

type OpenAIDecisionChoice struct {
	Value       json.RawMessage `json:"value"`
	Description *string         `json:"description,omitempty"`
}

type OpenAIDecisionLevel struct {
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
}

type OpenAIDecisionAnswer struct {
	Name          *string                     `json:"name"`
	Type          string                      `json:"type"`
	Probability   *float64                    `json:"probability"`
	Choice        json.RawMessage             `json:"choice"`
	Score         *float64                    `json:"score"`
	Confidence    *float64                    `json:"confidence"`
	Probabilities []OpenAIDecisionProbability `json:"probabilities"`
}

type OpenAIDecisionProbability struct {
	Value       json.RawMessage `json:"value"`
	Label       *string         `json:"label"`
	Probability *float64        `json:"probability"`
}

func strictDecisionJSON(raw []byte, target interface{}) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

func parseOpenAIDecisionRequest(body []byte) (*DecisionRequest, error) {
	var native OpenAIDecisionRequest
	if err := strictDecisionJSON(body, &native); err != nil {
		return nil, decisionError(types.ErrorCodeUnsupportedDecisionParameter, "invalid OpenAI decisions parameters")
	}
	model := native.Model
	if model == OpenAIDecisionWireModel {
		model = OpenAIDecisionModel
	}
	r := &DecisionRequest{Model: model, OpenAI: &native}
	if err := r.Validate(true); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *DecisionRequest) MarshalJSON() ([]byte, error) {
	if r.OpenAI != nil {
		native := *r.OpenAI
		if r.nativeModelOverride != "" {
			native.Model = r.nativeModelOverride
		}
		return json.Marshal(native)
	}
	type legacy DecisionRequest
	return json.Marshal((*legacy)(r))
}

func (r *DecisionRequest) QuestionCount() int {
	if r.OpenAI != nil {
		return len(r.OpenAI.Questions)
	}
	return len(r.Questions)
}

func (r *DecisionRequest) QuestionTypes() map[string]int {
	counts := map[string]int{}
	if r.OpenAI != nil {
		for _, q := range r.OpenAI.Questions {
			counts[q.Type]++
		}
	} else {
		for _, raw := range r.Questions {
			if q, err := ParseDecisionQuestion(raw); err == nil {
				counts[q.Type]++
			}
		}
	}
	return counts
}

func (r *DecisionRequest) QuestionIDs() []string {
	ids := make([]string, 0, r.QuestionCount())
	if r.OpenAI != nil {
		for _, q := range r.OpenAI.Questions {
			name := ""
			if q.Name != nil {
				name = *q.Name
			}
			ids = append(ids, name)
		}
	} else {
		for id := range r.Questions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	return ids
}

func (r *DecisionRequest) InputText() string {
	if r.OpenAI != nil {
		return r.OpenAI.inputText
	}
	var text string
	if json.Unmarshal(r.State, &text) == nil {
		return text
	}
	return string(r.State)
}

func (r *DecisionRequest) QuestionTexts() []string {
	texts := make([]string, 0, r.QuestionCount())
	if r.OpenAI != nil {
		for _, q := range r.OpenAI.Questions {
			body, _ := json.Marshal(q)
			texts = append(texts, string(body))
		}
	} else {
		for _, id := range r.QuestionIDs() {
			texts = append(texts, string(r.Questions[id]))
		}
	}
	return texts
}

func (r *OpenAIDecisionRequest) Validate() error {
	if len(r.Questions) == 0 {
		return decisionError(types.ErrorCodeMissingQuestions, "OpenAI decisions require explicit questions")
	}
	if len(r.Questions) > 64 {
		return decisionError(types.ErrorCodeInvalidDecisionQuestions, "too many questions")
	}
	names := map[string]bool{}
	for _, q := range r.Questions {
		if q.Name != nil {
			if names[*q.Name] {
				return decisionError(types.ErrorCodeInvalidDecisionQuestions, "question names must be unique")
			}
			names[*q.Name] = true
		}
		if err := q.Validate(); err != nil {
			return err
		}
	}
	// Validation runs again at relay ingress; reuse the validated image bytes.
	if bytes.Equal(r.inputSeen, r.Input) && len(r.inputSeen) > 0 {
		return nil
	}
	text, images, err := parseOpenAIDecisionInput(r.Input)
	if err != nil {
		return err
	}
	r.inputText, r.imageInfo = text, images
	r.inputSeen = append(json.RawMessage(nil), r.Input...)
	return nil
}

func (q OpenAIDecisionQuestion) Validate() error {
	invalid := func() error {
		return decisionError(types.ErrorCodeInvalidDecisionQuestions, "invalid OpenAI decision question")
	}
	if strings.TrimSpace(q.Instructions) == "" {
		return invalid()
	}
	switch q.Type {
	case "predicate":
		if q.Choices != nil || q.Levels != nil {
			return invalid()
		}
	case "choice":
		if len(q.Choices) < 2 || len(q.Choices) > 255 || q.Levels != nil {
			return invalid()
		}
		seen := map[string]bool{}
		for _, option := range q.Choices {
			key, ok := OpenAIDecisionChoiceKey(option.Value)
			if !ok || seen[key] {
				return invalid()
			}
			seen[key] = true
		}
	case "score":
		if len(q.Levels) < 2 || len(q.Levels) > 10 || q.Choices != nil {
			return invalid()
		}
		for _, level := range q.Levels {
			if strings.TrimSpace(level.Label) == "" {
				return invalid()
			}
		}
	default:
		return decisionError(types.ErrorCodeUnsupportedQuestionType, "unsupported question type")
	}
	return nil
}

// No number/null coercions: the official choice domain is string or boolean.
func OpenAIDecisionChoiceKey(raw json.RawMessage) (string, bool) {
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return "string:" + v, true
	case bool:
		return fmt.Sprint("boolean:", v), true
	default:
		return "", false
	}
}

func parseOpenAIDecisionInput(raw json.RawMessage) (string, []DecisionImage, error) {
	invalid := func() (string, []DecisionImage, error) {
		return "", nil, decisionError(types.ErrorCodeUnsupportedDecisionInput, "input requires text or user messages containing text and inline images")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return invalid()
		}
		return text, nil, nil
	}
	var messages []struct {
		Type    string          `json:"type,omitempty"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if strictDecisionJSON(raw, &messages) != nil || len(messages) == 0 {
		return invalid()
	}
	var texts []string
	var images []DecisionImage
	imageBytes := 0
	for _, message := range messages {
		if message.Role != "user" || (message.Type != "" && message.Type != "message") {
			return invalid()
		}
		text = "" // JSON null must not reuse a previous message's text.
		if json.Unmarshal(message.Content, &text) == nil {
			if strings.TrimSpace(text) == "" {
				return invalid()
			}
			texts = append(texts, text)
			continue
		}
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text,omitempty"`
			ImageURL string `json:"image_url,omitempty"`
			Detail   string `json:"detail,omitempty"`
		}
		if strictDecisionJSON(message.Content, &parts) != nil || len(parts) == 0 {
			return invalid()
		}
		for _, part := range parts {
			switch part.Type {
			case "input_text":
				if strings.TrimSpace(part.Text) == "" || part.ImageURL != "" || part.Detail != "" {
					return invalid()
				}
				texts = append(texts, part.Text)
			case "input_image":
				if part.Text != "" || (part.Detail != "" && part.Detail != "auto" && part.Detail != "low" && part.Detail != "high") {
					return invalid()
				}
				url, _ := json.Marshal(part.ImageURL)
				img, err := ParseDecisionImage(url)
				if err != nil {
					return "", nil, err
				}
				img.Detail = part.Detail
				images = append(images, img)
				imageBytes += img.Bytes
				if len(images) > 4 || imageBytes > 8<<20 {
					return invalid()
				}
			default:
				return invalid()
			}
		}
	}
	return strings.Join(texts, "\n"), images, nil
}

// Validate every answer before charging or emitting any part of the response.
// Refusals are valid provider results, not missing predictions or retry signals.
func ValidateOpenAIDecisionAnswers(questions []OpenAIDecisionQuestion, answers []json.RawMessage) error {
	invalid := func() error {
		return decisionError(types.ErrorCodeUpstreamDecisionInvalid, "invalid upstream OpenAI decision answers")
	}
	if len(answers) != len(questions) {
		return invalid()
	}
	for i, q := range questions {
		var a OpenAIDecisionAnswer
		if json.Unmarshal(answers[i], &a) != nil || (a.Name == nil) != (q.Name == nil) || (a.Name != nil && *a.Name != *q.Name) {
			return invalid()
		}
		if a.Confidence != nil && !finiteRange(*a.Confidence, 0, 1) {
			return invalid()
		}
		if a.Type == "refusal" {
			if a.Probability != nil || len(a.Choice) > 0 || a.Score != nil || len(a.Probabilities) > 0 {
				return invalid()
			}
			continue
		}
		if a.Type != q.Type {
			return invalid()
		}
		if q.Type == "predicate" {
			if a.Probability == nil || !finiteRange(*a.Probability, 0, 1) {
				return invalid()
			}
			continue
		}
		if a.Confidence == nil {
			return invalid()
		}
		expected := map[string]bool{}
		if q.Type == "choice" {
			for _, option := range q.Choices {
				key, _ := OpenAIDecisionChoiceKey(option.Value)
				expected[key] = true
			}
			key, ok := OpenAIDecisionChoiceKey(a.Choice)
			if !ok || !expected[key] {
				return invalid()
			}
		} else {
			for j := range q.Levels {
				expected[fmt.Sprint(j)] = true
			}
		}
		if len(a.Probabilities) != len(expected) {
			return invalid()
		}
		seen := map[string]bool{}
		sum, weighted := 0.0, 0.0
		for _, p := range a.Probabilities {
			if p.Probability == nil || !finiteRange(*p.Probability, 0, 1) {
				return invalid()
			}
			key, ok := OpenAIDecisionChoiceKey(p.Value)
			if q.Type == "score" {
				var index int
				if json.Unmarshal(p.Value, &index) != nil || index < 0 || index >= len(q.Levels) || p.Label == nil || *p.Label != q.Levels[index].Label {
					return invalid()
				}
				key, ok = fmt.Sprint(index), true
				weighted += float64(index) * *p.Probability
			}
			if !ok || !expected[key] || seen[key] {
				return invalid()
			}
			seen[key] = true
			sum += *p.Probability
		}
		if math.Abs(sum-1) > 0.001 || (q.Type == "score" && (a.Score == nil || !finiteRange(*a.Score, 0, float64(len(q.Levels)-1)) || math.Abs(*a.Score-weighted) > 0.001)) {
			return invalid()
		}
	}
	return nil
}
