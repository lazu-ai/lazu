package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
)

type Adaptor struct{ Dialect string }

func GetAdaptor(dialect string) *Adaptor {
	switch dialect {
	case "typesafe", "openrouter", "cloudflare_gateway", "cloudflare_workers_ai", "openai":
		return &Adaptor{Dialect: dialect}
	}
	return nil
}

var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

// BuildRequest only emits the provider's schema. Gateway controls never reach
// the provider, even when global pass-through is enabled for chat requests.
func (a *Adaptor) BuildRequest(ctx context.Context, base, account, key, upstreamModel string, r *dto.DecisionRequest) (*http.Request, error) {
	if r == nil {
		return nil, fmt.Errorf("decision request is missing")
	}
	if r.OpenAI != nil && a.Dialect != "openai" {
		return nil, types.NewError(fmt.Errorf("official decision input requires the OpenAI dialect"), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
	}
	if base == "" {
		switch a.Dialect {
		case "typesafe":
			base = "https://api.typesafe.ai"
		case "openrouter":
			base = "https://openrouter.ai"
		case "openai":
			base = "https://api.openai.com"
		default:
			base = "https://api.cloudflare.com"
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid decision base URL")
	}
	path := strings.TrimRight(u.Path, "/")
	payload := map[string]interface{}{"model": upstreamModel, "state": r.State, "questions": r.Questions}
	if len(r.ImageInfo) > 0 {
		if a.Dialect != "cloudflare_workers_ai" && a.Dialect != "openai" {
			return nil, types.NewError(fmt.Errorf("this provider does not accept images"), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
		}
		images := make([]string, 0, len(r.ImageInfo))
		for _, img := range r.ImageInfo {
			images = append(images, img.DataURL)
		}
		payload["images"] = images
	}
	switch a.Dialect {
	case "typesafe":
		path = strings.TrimSuffix(path, "/v1") + "/v1/systemone"
	case "openrouter":
		path = strings.TrimSuffix(path, "/api") + "/api/alpha/decisions"
	case "openai":
		path = strings.TrimSuffix(path, "/v1") + "/v1/decisions"
		official, err := openAIRequest(r, upstreamModel)
		if err != nil {
			return nil, err
		}
		payload = map[string]interface{}{"model": official.Model, "input": official.Input, "questions": official.Questions}
	case "cloudflare_gateway", "cloudflare_workers_ai":
		if !accountPattern.MatchString(account) {
			return nil, fmt.Errorf("Cloudflare account ID is required")
		}
		path = strings.TrimSuffix(path, "/client/v4") + "/client/v4/accounts/" + account + "/ai/run"
		if a.Dialect == "cloudflare_gateway" {
			payload = map[string]interface{}{"model": "typesafe/jev", "input": map[string]interface{}{"state": r.State, "questions": r.Questions}}
		} else {
			if upstreamModel != "clef" && upstreamModel != "clef-flash" {
				return nil, fmt.Errorf("Cloudflare decision model must be clef or clef-flash")
			}
			path += "/@cf/cloudflare/" + upstreamModel
		}
	default:
		return nil, fmt.Errorf("unsupported decision dialect")
	}
	u.Path = path
	u.RawPath = ""
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > dto.DecisionMaxBodyBytes {
		return nil, types.NewError(fmt.Errorf("encoded decision body exceeds 13 MiB"), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	return req, nil
}

type Result struct {
	Model          string                     `json:"model"`
	Answers        map[string]json.RawMessage `json:"answers"`
	Usage          *dto.DecisionUsage         `json:"usage"`
	NativeResponse json.RawMessage            `json:"-"`
	ProviderUsage  *dto.Usage                 `json:"-"`
}

func (a *Adaptor) ParseDecisionResponse(body []byte, request *dto.DecisionRequest) (*Result, error) {
	if request == nil {
		return nil, invalidResponse()
	}
	if a.Dialect == "openai" {
		return parseOpenAIResponse(body, request)
	}
	return a.ParseResponse(body, request.Questions)
}

func (a *Adaptor) ParseResponse(body []byte, questions map[string]json.RawMessage) (*Result, error) {
	if a.Dialect == "openai" {
		return parseOpenAIResponse(body, &dto.DecisionRequest{Model: dto.OpenAIDecisionModel, Questions: questions})
	}
	if strings.HasPrefix(a.Dialect, "cloudflare_") {
		var envelope struct {
			Success *bool           `json:"success"`
			Result  json.RawMessage `json:"result"`
			Errors  json.RawMessage `json:"errors"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, invalidResponse()
		}
		if envelope.Success == nil || !*envelope.Success {
			var faults []struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(envelope.Errors, &faults) == nil {
				for _, fault := range faults {
					if fault.Code == 2018 || fault.Code == 3040 {
						return nil, types.NewError(fmt.Errorf("Cloudflare decision capacity exhausted"), types.ErrorCodeUpstreamCapacity)
					}
				}
			}
			return nil, invalidResponse()
		}
		body = envelope.Result
		if a.Dialect == "cloudflare_gateway" {
			var job struct {
				State  string          `json:"state"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(body, &job) != nil {
				return nil, invalidResponse()
			}
			if job.State != "" {
				if job.State != "Completed" || len(job.Result) == 0 {
					return nil, invalidResponse()
				}
				body = job.Result
			}
		}
	}
	var result Result
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, invalidResponse()
	}
	if result.Model == "" {
		return nil, invalidResponse()
	}
	if result.Usage != nil && (result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0) {
		return nil, invalidResponse()
	}
	if err := dto.ValidateDecisionAnswers(questions, result.Answers); err != nil {
		return nil, err
	}
	return &result, nil
}

func invalidResponse() error {
	return types.NewError(fmt.Errorf("invalid upstream decision response"), types.ErrorCodeUpstreamDecisionInvalid)
}
