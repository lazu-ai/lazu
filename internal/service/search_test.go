package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func TestProbeSearchBackendDoesNotChargeLocalSetupFailure(t *testing.T) {
	sample, attempted := ProbeSearchBackend(context.Background(), &model.Channel{
		Id: 71, Type: constant.ChannelTypeSerper,
	})
	if attempted || sample.Success || sample.ErrorMessage == "" {
		t.Fatalf("local setup result = %+v attempted=%t, want a skipped provider call", sample, attempted)
	}
}

func TestCallSerperSearchNormalizesOrganicResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("expected /search, got %s", r.URL.Path)
		}
		if got := r.Header.Get("X-API-KEY"); got != "serper-key" {
			t.Fatalf("expected X-API-KEY header, got %q", got)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if payload["q"] != "apple inc" || payload["num"].(float64) != 3 {
			t.Fatalf("unexpected serper payload: %#v", payload)
		}
		if payload["tbs"] != "qdr:d" {
			t.Fatalf("expected day time_range to map to qdr:d, got %#v", payload["tbs"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"answerBox":{"answer":"Apple Inc. is a technology company."},
			"organic":[
				{"title":"Apple","link":"https://apple.com","snippet":"Official site","date":"2026-06-01","position":1}
			]
		}`))
	}))
	defer upstream.Close()
	allowLocalFetchServer(t, upstream.URL)

	baseURL := upstream.URL
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/search", nil)
	result, err := callSerperSearch(c, dto.SearchRequest{
		Query:         "apple inc",
		Type:          "web",
		TimeRange:     "day",
		MaxResults:    3,
		IncludeAnswer: true,
	}, searchCandidate{
		Provider: "serper",
		Channel: &model.Channel{
			Type:    constant.ChannelTypeSerper,
			Key:     "serper-key",
			BaseURL: &baseURL,
		},
		Config: dto.SearchBackendConfig{
			Provider:   "serper",
			MaxResults: 10,
			TimeoutMs:  5000,
		},
	})
	if err != nil {
		t.Fatalf("callSerperSearch returned error: %v", err)
	}
	if result.Answer == nil || *result.Answer != "Apple Inc. is a technology company." {
		t.Fatalf("expected answer box, got %#v", result.Answer)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected one result, got %+v", result.Results)
	}
	got := result.Results[0]
	if got.Title != "Apple" || got.URL != "https://apple.com" || got.Snippet != "Official site" || got.PublishedAt == nil || *got.PublishedAt != "2026-06-01" {
		t.Fatalf("unexpected normalized result: %+v", got)
	}
	if got.Score == nil || *got.Score != 1 {
		t.Fatalf("expected score from position, got %#v", got.Score)
	}
}

func allowLocalFetchServer(t *testing.T, rawURL string) {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	_, port, err := net.SplitHostPort(parsedURL.Host)
	if err != nil {
		t.Fatalf("parse upstream host: %v", err)
	}
	fetchSetting := system_setting.GetFetchSetting()
	oldAllowPrivateIP := fetchSetting.AllowPrivateIp
	oldAllowedPorts := append([]string(nil), fetchSetting.AllowedPorts...)
	fetchSetting.AllowPrivateIp = true
	fetchSetting.AllowedPorts = append(oldAllowedPorts, port)
	t.Cleanup(func() {
		fetchSetting.AllowPrivateIp = oldAllowPrivateIP
		fetchSetting.AllowedPorts = oldAllowedPorts
	})
}

func TestQuoteSearchRequestCountsProviderBillableUnits(t *testing.T) {
	config := dto.SearchBackendConfig{
		Provider:    "tavily",
		SearchPrice: 0.008,
	}

	basic := quoteSearchRequest(config, dto.SearchRequest{SearchDepth: "basic"}, 0)
	if basic.TotalMicrousd != 8_000 || basic.LineItems[0].Quantity != 1 {
		t.Fatalf("expected Tavily basic to cost one 0.008 credit, got %+v", basic)
	}

	advanced := quoteSearchRequest(config, dto.SearchRequest{SearchDepth: "advanced"}, 0)
	if advanced.TotalMicrousd != 16_000 || advanced.LineItems[0].Quantity != 2 {
		t.Fatalf("expected Tavily advanced to cost two 0.008 credits, got %+v", advanced)
	}

	config.UseProviderCost = true
	providerCost := quoteSearchRequest(config, dto.SearchRequest{SearchDepth: "advanced"}, 0.003)
	if providerCost.TotalMicrousd != 3_000 || providerCost.LineItems[0].Quantity != 1 {
		t.Fatalf("expected provider-reported cost to be treated as total cost, got %+v", providerCost)
	}
}

// Only request validation is the caller's fault. A backend that answers 404
// or 405 is misconfigured, and one that times out is slow: both are health
// evidence.
func TestSearchErrorIsCallerFaultOnlyForValidation(t *testing.T) {
	for message, want := range map[string]bool{
		"upstream returned 400: bad query":  true,
		"upstream returned 422: invalid":    true,
		"upstream returned 404: not found":  false,
		"upstream returned 405: method":     false,
		"upstream returned 408: timeout":    false,
		"upstream returned 401: bad key":    false,
		"upstream returned 429: rate limit": false,
		"dial tcp: connection refused":      false,
	} {
		if got := searchErrorIsCallerFault(errors.New(message)); got != want {
			t.Errorf("%q caller fault = %v, want %v", message, got, want)
		}
	}
}
