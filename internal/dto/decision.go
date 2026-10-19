package dto

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/types"
	_ "golang.org/x/image/webp"
)

const DecisionMaxBodyBytes = 13 << 20

// Keep business JSON as JSON, including option descriptions and rubric levels.
type DecisionQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type DecisionQuestionGeneration struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Model   string `json:"model,omitempty"`
}

type DecisionGenerationResult struct {
	Model         string        `json:"model"`
	RequestID     string        `json:"request_id"`
	PromptVersion string        `json:"prompt_version"`
	Usage         DecisionUsage `json:"usage"`
}

type DecisionRequest struct {
	nativeModelOverride string
	OpenAI              *OpenAIDecisionRequest      `json:"-"`
	GenerationEnabled   bool                        `json:"-"`
	Model               string                      `json:"model"`
	State               json.RawMessage             `json:"state"`
	Questions           map[string]json.RawMessage  `json:"questions,omitempty"`
	Images              []json.RawMessage           `json:"images,omitempty"`
	QuestionGeneration  *DecisionQuestionGeneration `json:"question_generation,omitempty"`
	Stream              bool                        `json:"stream,omitempty"`
	GenerationResult    *DecisionGenerationResult   `json:"-"`
	ImageInfo           []DecisionImage             `json:"-"`
}

type DecisionImage struct {
	Detail      string
	DataURL     string
	SHA256      string
	Bytes       int
	ContentType string
	Width       int
	Height      int
}

type DecisionUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type DecisionResponse struct {
	ID                 string                     `json:"id"`
	Object             string                     `json:"object"`
	Created            int64                      `json:"created"`
	Model              string                     `json:"model"`
	Answers            map[string]json.RawMessage `json:"answers"`
	Usage              DecisionUsage              `json:"usage"`
	Questions          map[string]json.RawMessage `json:"questions,omitempty"`
	QuestionGeneration *DecisionGenerationResult  `json:"question_generation,omitempty"`
	Warnings           []string                   `json:"warnings,omitempty"`
}

type DecisionCapabilities struct {
	QuestionTypes  []string `json:"question_types"`
	MaxQuestions   int      `json:"max_questions"`
	MaxImages      int      `json:"max_images"`
	MaxStateTokens int      `json:"max_state_tokens"`
	MaxStateBytes  int      `json:"max_state_bytes,omitempty"`
	MaxInputTokens int      `json:"max_input_tokens"`
	Streaming      bool     `json:"streaming"`
}

func DecisionCapabilitiesForModel(model string) *DecisionCapabilities {
	c := &DecisionCapabilities{QuestionTypes: []string{"noul", "choice", "score"}, MaxQuestions: 64}
	switch {
	case IsOpenAIDecisionModel(model):
		c.QuestionTypes = []string{"predicate", "choice", "score"}
		c.MaxImages, c.MaxStateTokens, c.MaxInputTokens = 4, 8192, 8192
	case model == "clef" || model == "clef-flash":
		c.MaxImages, c.MaxStateTokens, c.MaxInputTokens, c.MaxStateBytes = 4, 2000, 65536, 2000
	case strings.HasPrefix(model, "jev-"):
		c.MaxStateTokens, c.MaxInputTokens = 32768, 65536
	default:
		return nil
	}
	return c
}

func decisionError(code types.ErrorCode, message string) *types.LazuError {
	return types.NewError(fmt.Errorf("%s", message), code, types.ErrOptionWithSkipRetry())
}

func ParseDecisionRequest(body []byte) (*DecisionRequest, error) {
	if len(body) > DecisionMaxBodyBytes {
		return nil, decisionError(types.ErrorCodeUnsupportedDecisionInput, "decision body exceeds 13 MiB")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, decisionError(types.ErrorCodeInvalidRequest, "expected a JSON object")
	}
	if _, native := fields["input"]; native {
		return parseOpenAIDecisionRequest(body)
	}
	for key := range fields {
		switch key {
		case "model", "state", "questions", "images", "question_generation", "stream":
		case "audio", "videos":
			return nil, decisionError(types.ErrorCodeUnsupportedDecisionInput, key+" is not supported yet")
		default:
			return nil, decisionError(types.ErrorCodeUnsupportedDecisionParameter, "unsupported parameter: "+key)
		}
	}
	var request DecisionRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, decisionError(types.ErrorCodeInvalidRequest, err.Error())
	}
	if request.QuestionGeneration != nil {
		decoder := json.NewDecoder(bytes.NewReader(fields["question_generation"]))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request.QuestionGeneration); err != nil {
			return nil, decisionError(types.ErrorCodeUnsupportedDecisionParameter, err.Error())
		}
	}
	if err := request.Validate(false); err != nil {
		return nil, err
	}
	return &request, nil
}

func validDecisionJSON(raw json.RawMessage, allowNull bool) bool {
	raw = bytes.TrimSpace(raw)
	if allowNull && bytes.Equal(raw, []byte("null")) {
		return true
	}
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case '"':
		var value string
		return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
	case '{', '[':
		if !json.Valid(raw) {
			return false
		}
		if raw[0] == '{' {
			var v map[string]json.RawMessage
			_ = json.Unmarshal(raw, &v)
			return len(v) > 0
		}
		var v []json.RawMessage
		_ = json.Unmarshal(raw, &v)
		return len(v) > 0
	}
	return false
}

var decisionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,100}$`)

func (r *DecisionRequest) Validate(requireQuestions bool) error {
	if strings.TrimSpace(r.Model) == "" {
		return decisionError(types.ErrorCodeModelNameRequired, "model is required")
	}
	if r.Stream {
		return decisionError(types.ErrorCodeUnsupportedDecisionParameter, "decisions do not support stream:true")
	}
	if r.OpenAI != nil {
		if !IsOpenAIDecisionModel(r.Model) {
			return decisionError(types.ErrorCodeUnsupportedDecisionInput, "model does not support the OpenAI decisions format")
		}
		if err := r.OpenAI.Validate(); err != nil {
			return err
		}
		r.ImageInfo = r.OpenAI.imageInfo
		return nil
	}
	state := bytes.TrimSpace(r.State)
	if len(state) == 0 || (state[0] != '"' && state[0] != '{' && state[0] != '[') {
		return decisionError(types.ErrorCodeInvalidDecisionState, "state must be a string, object or array")
	}
	if !validDecisionJSON(state, false) && len(r.Images) == 0 {
		return decisionError(types.ErrorCodeInvalidDecisionState, "state must contain material, or supply images")
	}
	caps := DecisionCapabilitiesForModel(r.Model)
	if caps == nil {
		return decisionError(types.ErrorCodeUnsupportedDecisionInput, "model does not support decisions")
	}
	if len(r.Images) > caps.MaxImages {
		return decisionError(types.ErrorCodeUnsupportedDecisionInput, "model does not support this number of images")
	}
	if r.ImageInfo == nil && len(r.Images) > 0 {
		total := 0
		for _, raw := range r.Images {
			img, err := ParseDecisionImage(raw)
			if err != nil {
				return err
			}
			total += img.Bytes
			if total > 8<<20 {
				return decisionError(types.ErrorCodeUnsupportedDecisionInput, "decoded images exceed 8 MiB")
			}
			r.ImageInfo = append(r.ImageInfo, img)
		}
	}
	if len(r.Questions) == 0 {
		if requireQuestions {
			return decisionError(types.ErrorCodeMissingQuestions, "questions are required unless question generation is enabled")
		}
		return nil
	}
	if len(r.Questions) > caps.MaxQuestions {
		return decisionError(types.ErrorCodeInvalidDecisionQuestions, "too many questions")
	}
	for id, raw := range r.Questions {
		if !decisionIDPattern.MatchString(id) {
			return decisionError(types.ErrorCodeInvalidDecisionQuestions, "invalid question ID")
		}
		if _, err := ParseDecisionQuestion(raw); err != nil {
			return err
		}
	}
	return nil
}

func ParseDecisionQuestion(raw json.RawMessage) (*DecisionQuestion, error) {
	var q DecisionQuestion
	if err := json.Unmarshal(raw, &q); err != nil || !validDecisionJSON(q.Instructions, false) {
		return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "question requires nonempty instructions")
	}
	switch q.Type {
	case "noul":
		if len(q.Criteria) > 0 {
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(q.Criteria, &criteria); err != nil || criteria == nil {
				return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "noul criteria must be an object")
			}
			for key, desc := range criteria {
				if (key != "true" && key != "false") || !validDecisionJSON(desc, true) {
					return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "invalid noul criteria")
				}
			}
		}
	case "choice":
		var criteria map[string]json.RawMessage
		if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) < 2 || len(criteria) > 255 {
			return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "choice requires 2–255 options")
		}
		for key, desc := range criteria {
			if strings.TrimSpace(key) == "" || !validDecisionJSON(desc, true) {
				return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "invalid choice option")
			}
		}
	case "score":
		var criteria []json.RawMessage
		if err := json.Unmarshal(q.Criteria, &criteria); err != nil || len(criteria) < 2 || len(criteria) > 10 {
			return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "score requires 2–10 ordered levels")
		}
		for _, desc := range criteria {
			if !validDecisionJSON(desc, false) {
				return nil, decisionError(types.ErrorCodeInvalidDecisionQuestions, "invalid score level")
			}
		}
	default:
		return nil, decisionError(types.ErrorCodeUnsupportedQuestionType, "unsupported question type")
	}
	return &q, nil
}

func ParseDecisionImage(raw json.RawMessage) (DecisionImage, error) {
	var img DecisionImage
	var dataURL string
	if json.Unmarshal(raw, &dataURL) != nil {
		var object struct {
			URL string `json:"url"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&object); err != nil {
			return img, decisionError(types.ErrorCodeUnsupportedDecisionInput, "expected an embedded image data URL")
		}
		dataURL = object.URL
	}
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:image/") || !strings.HasSuffix(strings.ToLower(parts[0]), ";base64") || len(parts[1]) > base64.StdEncoding.EncodedLen(4<<20) {
		return img, decisionError(types.ErrorCodeUnsupportedDecisionInput, "only embedded PNG, JPEG or WebP images up to 4 MiB are supported")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(decoded) == 0 || len(decoded) > 4<<20 {
		return img, decisionError(types.ErrorCodeUnsupportedDecisionInput, "invalid or oversized base64 image")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16000000 {
		return img, decisionError(types.ErrorCodeUnsupportedDecisionInput, "unsupported image format or more than 16 megapixels")
	}
	mime := "image/" + format
	if !strings.EqualFold(parts[0], "data:"+mime+";base64") {
		return img, decisionError(types.ErrorCodeUnsupportedDecisionInput, "image signature does not match declared content type")
	}
	hash := sha256.Sum256(decoded)
	img = DecisionImage{DataURL: "data:" + mime + ";base64," + parts[1], SHA256: hex.EncodeToString(hash[:]), Bytes: len(decoded), ContentType: mime, Width: config.Width, Height: config.Height}
	return img, nil
}

func (r *DecisionRequest) IsStream(*gin.Context) bool { return false }
func (r *DecisionRequest) SetModelName(model string) {
	if model != "" {
		r.Model = model
		if r.OpenAI != nil {
			r.nativeModelOverride = model
		}
	}
}
func (r *DecisionRequest) GetTokenCountMeta() *types.TokenCountMeta {
	questions, _ := json.Marshal(r.Questions)
	meta := &types.TokenCountMeta{CombineText: string(r.State) + "\n" + string(questions)}
	if r.OpenAI != nil {
		meta.CombineText = r.InputText() + "\n" + strings.Join(r.QuestionTexts(), "\n")
	}
	for _, img := range r.ImageInfo {
		detail := img.Detail
		if detail == "" {
			detail = "auto"
		}
		meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeImage, MimeType: img.ContentType, OriginData: img.DataURL, Detail: detail})
	}
	return meta
}

// Validate the complete answer set before emitting any response or charging.
// Raw objects preserve provider extensions without altering probabilities.
func ValidateDecisionAnswers(questions map[string]json.RawMessage, answers map[string]json.RawMessage) error {
	invalid := func() error {
		return decisionError(types.ErrorCodeUpstreamDecisionInvalid, "upstream returned invalid decision answers")
	}
	if len(questions) != len(answers) {
		return invalid()
	}
	for id, raw := range questions {
		q, err := ParseDecisionQuestion(raw)
		if err != nil {
			return err
		}
		var a struct {
			Type          string                     `json:"type"`
			Noul          *float64                   `json:"noul"`
			Choice        string                     `json:"choice"`
			Score         *float64                   `json:"score"`
			Probabilities map[string]float64         `json:"probabilities"`
			Legend        map[string]json.RawMessage `json:"legend"`
			Confidence    *float64                   `json:"confidence"`
		}
		if err := json.Unmarshal(answers[id], &a); err != nil || a.Type != q.Type {
			return invalid()
		}
		if a.Confidence != nil && !finiteRange(*a.Confidence, 0, 1) {
			return invalid()
		}
		if q.Type == "noul" {
			if a.Noul == nil || !finiteRange(*a.Noul, 0, 1) {
				return invalid()
			}
			continue
		}
		var expected map[string]json.RawMessage
		if q.Type == "choice" {
			_ = json.Unmarshal(q.Criteria, &expected)
			if _, ok := expected[a.Choice]; !ok {
				return invalid()
			}
		} else {
			var levels []json.RawMessage
			_ = json.Unmarshal(q.Criteria, &levels)
			if a.Score == nil || !finiteRange(*a.Score, 0, float64(len(levels)-1)) || len(a.Legend) != len(levels) {
				return invalid()
			}
			expected = make(map[string]json.RawMessage, len(levels))
			for i := range levels {
				key := fmt.Sprint(i)
				expected[key] = levels[i]
				if _, ok := a.Legend[key]; !ok {
					return invalid()
				}
			}
		}
		if len(a.Probabilities) != len(expected) {
			return invalid()
		}
		sum := 0.0
		for key := range expected {
			p, ok := a.Probabilities[key]
			if !ok || !finiteRange(p, 0, 1) {
				return invalid()
			}
			sum += p
		}
		if math.Abs(sum-1) > 0.02 {
			return invalid()
		}
	}
	return nil
}

func finiteRange(n, low, high float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= low && n <= high
}
