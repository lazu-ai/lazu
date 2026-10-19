package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

type searchCandidate struct {
	Channel  *model.Channel
	Provider string
	Config   dto.SearchBackendConfig
}

type searchProviderResult struct {
	Results         []dto.SearchResult
	Answer          *string
	ProviderPayload json.RawMessage
	ProviderCostUSD float64
}

func ExecuteSearch(c *gin.Context, req dto.SearchRequest) (dto.SearchResponse, *types.LazuError) {
	started := time.Now()
	if lazuErr := normalizeSearchRequest(&req); lazuErr != nil {
		return dto.SearchResponse{}, lazuErr
	}
	if lazuErr := applyTokenSearchPolicy(c, &req); lazuErr != nil {
		return dto.SearchResponse{}, lazuErr
	}

	candidates, rejected, lazuErr := searchCandidates(c, req)
	if lazuErr != nil {
		return dto.SearchResponse{}, lazuErr
	}
	receipt := dto.SearchRouteReceipt{
		Selection:      req.Backend,
		Candidates:     candidateNames(candidates),
		Rejected:       rejected,
		DecisionReason: "priority",
	}
	var lastErr error
	var fallbackCount int
	rateAdmitted := false
	initialRates, hadRates := c.Get(ledger.RateLimitsContextKey)
	defer func() {
		if hadRates {
			c.Set(ledger.RateLimitsContextKey, initialRates)
		}
	}()
	for _, candidate := range candidates {
		if rateAdmitted && model.GatewayLedgerEnabled() {
			c.Set(ledger.RateLimitsContextKey, []ledger.RateLimit{})
		}
		relayInfo := searchRelayInfo(c, candidate.Channel)
		quote := quoteSearchRequest(candidate.Config, req, 0)
		quota := QuoteToQuota(quote)
		if quota > 0 || model.GatewayLedgerEnabled() {
			if err := PreConsumeQuota(c, quota, relayInfo); err != nil {
				return dto.SearchResponse{}, err
			}
			rateAdmitted = true
		}

		c.Set("search_business_channel_id", candidate.Channel.Id)
		attemptStarted := time.Now()
		result, err := callSearchProvider(c, req, candidate)
		recordSearchTrafficSample(candidate, attemptStarted, result, err)
		if err != nil {
			lastErr = err
			returnSearchPreConsumedQuota(relayInfo)
			receipt.Rejected = append(receipt.Rejected, dto.SearchRoutingRejected{
				Backend: candidate.Provider,
				Reason:  err.Error(),
			})
			fallbackCount++
			continue
		}
		receipt.SelectedBackend = candidate.Channel.Name
		receipt.ChoseProvider = candidate.Provider
		receipt.FallbackCount = fallbackCount
		receipt.Downgraded = fallbackCount > 0
		receipt.DecisionReason = "selected"
		if candidate.Config.UseProviderCost && result.ProviderCostUSD > 0 {
			quote = quoteSearchRequest(candidate.Config, req, result.ProviderCostUSD)
			quota = QuoteToQuota(quote)
		}
		finalizeSearchBilling(c, relayInfo, quote, started, candidate, req, receipt, result)
		results := result.Results
		if common.GetContextKeyBool(c, constant.ContextKeyTokenSearchLimitEnabled) {
			results = filterSearchResultsByDomains(results, parseDomainList(common.GetContextKeyString(c, constant.ContextKeyTokenSearchDomainAllow)))
		}
		response := dto.SearchResponse{
			Query:   req.Query,
			Results: results,
			Answer:  result.Answer,
			Usage: dto.SearchUsage{
				WebSearchRequests:      1,
				WebSearchBillableUnits: searchBillableUnitsFromQuote(quote),
			},
			ProviderTrace: dto.SearchProviderTrace{
				Backend: candidate.Provider,
			},
			RouteReceipt: &receipt,
		}
		if req.IncludeProviderPayload {
			response.ProviderPayload = result.ProviderPayload
		}
		return response, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no search backend could serve the request")
	}
	return dto.SearchResponse{}, types.NewErrorWithStatusCode(lastErr, types.ErrorCodeUpstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
}

func normalizeSearchRequest(req *dto.SearchRequest) *types.LazuError {
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return types.NewErrorWithStatusCode(errors.New("query is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if req.Type == "" {
		req.Type = "web"
	}
	if req.Type != "web" && req.Type != "news" {
		return types.NewErrorWithStatusCode(fmt.Errorf("unsupported search type %q", req.Type), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	req.Backend = strings.ToLower(strings.TrimSpace(req.Backend))
	if req.Backend == "" {
		req.Backend = dto.SearchBackendAuto
	}
	req.SearchDepth = strings.ToLower(strings.TrimSpace(req.SearchDepth))
	if req.SearchDepth == "" {
		req.SearchDepth = "basic"
	}
	if req.MaxResults <= 0 {
		req.MaxResults = 10
	}
	if req.MaxResults > 50 {
		req.MaxResults = 50
	}
	req.Region = strings.ToLower(strings.TrimSpace(req.Region))
	req.Country = strings.ToLower(strings.TrimSpace(req.Country))
	req.Language = strings.TrimSpace(req.Language)
	req.TimeRange = strings.ToLower(strings.TrimSpace(req.TimeRange))
	req.IncludeDomains = normalizeDomainList(req.IncludeDomains)
	req.ExcludeDomains = normalizeDomainList(req.ExcludeDomains)
	return nil
}

func applyTokenSearchPolicy(c *gin.Context, req *dto.SearchRequest) *types.LazuError {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenSearchLimitEnabled) {
		return nil
	}
	allowedProviders := tokenSearchProviders(c)
	if len(allowedProviders) == 0 {
		return types.NewErrorWithStatusCode(errors.New("token is not allowed to use search"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if req.Backend != dto.SearchBackendAuto && !stringSetContains(allowedProviders, req.Backend) {
		return types.NewErrorWithStatusCode(fmt.Errorf("token is not allowed to use search backend %q", req.Backend), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	allowedDomains := parseDomainList(common.GetContextKeyString(c, constant.ContextKeyTokenSearchDomainAllow))
	if len(allowedDomains) == 0 {
		return nil
	}
	if len(req.IncludeDomains) == 0 {
		req.IncludeDomains = allowedDomains
		return nil
	}
	for _, domain := range req.IncludeDomains {
		if !domainAllowed(domain, allowedDomains) {
			return types.NewErrorWithStatusCode(fmt.Errorf("domain %q is not allowed for this token", domain), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
	}
	return nil
}

func tokenSearchProviders(c *gin.Context) map[string]struct{} {
	result := make(map[string]struct{})
	if providers, ok := common.GetContextKeyType[model.StringSlice](c, constant.ContextKeyTokenSearchProviders); ok {
		for _, provider := range providers {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" {
				result[provider] = struct{}{}
			}
		}
		return result
	}
	if providers, ok := common.GetContextKeyType[[]string](c, constant.ContextKeyTokenSearchProviders); ok {
		for _, provider := range providers {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider != "" {
				result[provider] = struct{}{}
			}
		}
	}
	return result
}

func searchCandidates(c *gin.Context, req dto.SearchRequest) ([]searchCandidate, []dto.SearchRoutingRejected, *types.LazuError) {
	var channels []*model.Channel
	if err := model.CoreDB().
		Where("type IN ? AND status = ?", constant.SearchChannelTypes(), common.ChannelStatusEnabled).
		Order("priority desc, id asc").
		Find(&channels).Error; err != nil {
		return nil, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeQueryDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}
	if err := model.DecryptChannelKeysForUse(channels); err != nil {
		return nil, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeQueryDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
	}
	tokenRestricted := common.GetContextKeyBool(c, constant.ContextKeyTokenSearchLimitEnabled)
	allowedProviders := tokenSearchProviders(c)
	candidates := make([]searchCandidate, 0, len(channels))
	rejected := make([]dto.SearchRoutingRejected, 0)
	for _, channel := range channels {
		provider, config := searchConfigForChannel(channel)
		if provider == "" {
			rejected = append(rejected, dto.SearchRoutingRejected{Backend: constant.GetChannelTypeName(channel.Type), Reason: "invalid provider"})
			continue
		}
		if req.Backend != dto.SearchBackendAuto && req.Backend != provider && req.Backend != strings.ToLower(channel.Name) && req.Backend != strings.ToLower(channel.PublicID) && req.Backend != strconv.Itoa(channel.Id) {
			continue
		}
		if tokenRestricted && !stringSetContains(allowedProviders, provider) {
			rejected = append(rejected, dto.SearchRoutingRejected{Backend: provider, Reason: "token provider restriction"})
			continue
		}
		if !searchCapabilitiesInclude(config.Capabilities, dto.SearchCapabilityWebSearch) {
			rejected = append(rejected, dto.SearchRoutingRejected{Backend: provider, Reason: "web_search not supported"})
			continue
		}
		if strings.TrimSpace(channel.Key) == "" {
			rejected = append(rejected, dto.SearchRoutingRejected{Backend: provider, Reason: "missing api key"})
			continue
		}
		candidates = append(candidates, searchCandidate{Channel: channel, Provider: provider, Config: config})
	}
	if len(candidates) == 0 {
		status := http.StatusServiceUnavailable
		code := types.ErrorCodeGetChannelFailed
		if tokenRestricted && len(allowedProviders) == 0 {
			status = http.StatusForbidden
			code = types.ErrorCodeAccessDenied
		}
		return nil, rejected, types.NewErrorWithStatusCode(errors.New("no search backend available"), code, status, types.ErrOptionWithSkipRetry())
	}
	return candidates, rejected, nil
}

func searchConfigForChannel(channel *model.Channel) (string, dto.SearchBackendConfig) {
	if channel == nil {
		return "", dto.SearchBackendConfig{}
	}
	provider := constant.SearchProviderFromChannelType(channel.Type)
	config := dto.SearchBackendConfig{
		Provider:     provider,
		Capabilities: defaultSearchCapabilities(provider),
		MaxResults:   10,
		TimeoutMs:    8000,
	}
	if settings := channel.GetOtherSettings().SearchBackend; settings != nil {
		if strings.TrimSpace(settings.Provider) != "" {
			config.Provider = strings.ToLower(strings.TrimSpace(settings.Provider))
			provider = config.Provider
		}
		if len(settings.Capabilities) > 0 {
			config.Capabilities = normalizeSearchCapabilities(settings.Capabilities)
		}
		if settings.MaxResults > 0 {
			config.MaxResults = settings.MaxResults
		}
		if settings.TimeoutMs > 0 {
			config.TimeoutMs = settings.TimeoutMs
		}
		if settings.SearchPrice > 0 {
			config.SearchPrice = settings.SearchPrice
		}
		if settings.FetchPrice > 0 {
			config.FetchPrice = settings.FetchPrice
		}
		config.UseProviderCost = settings.UseProviderCost
	}
	return provider, config
}

func defaultSearchCapabilities(provider string) []string {
	switch provider {
	case "tavily":
		return []string{dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch, dto.SearchCapabilityAnswer}
	case "jina":
		return []string{dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch}
	default:
		return []string{dto.SearchCapabilityWebSearch}
	}
}

func normalizeSearchCapabilities(capabilities []string) []string {
	result := make([]string, 0, len(capabilities))
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		switch capability {
		case dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch, dto.SearchCapabilityAnswer:
		default:
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	if len(result) == 0 {
		return []string{dto.SearchCapabilityWebSearch}
	}
	return result
}

func searchCapabilitiesInclude(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func candidateNames(candidates []searchCandidate) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Channel != nil && candidate.Channel.Name != "" {
			result = append(result, candidate.Provider+":"+candidate.Channel.Name)
			continue
		}
		result = append(result, candidate.Provider)
	}
	return result
}

func callSearchProvider(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	switch candidate.Provider {
	case "tavily":
		return callTavilySearch(c, req, candidate)
	case "serper":
		return callSerperSearch(c, req, candidate)
	case "exa":
		return callExaSearch(c, req, candidate)
	case "brave":
		return callBraveSearch(c, req, candidate)
	case "jina":
		return callJinaSearch(c, req, candidate)
	default:
		return searchProviderResult{}, fmt.Errorf("unsupported search backend %q", candidate.Provider)
	}
}

func callTavilySearch(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	payload := map[string]interface{}{
		"api_key":             firstSearchKey(candidate.Channel.Key),
		"query":               req.Query,
		"max_results":         searchMaxResults(req, candidate),
		"include_answer":      req.IncludeAnswer,
		"include_raw_content": req.IncludeRawContent,
		"search_depth":        req.SearchDepth,
	}
	if req.Type == "news" {
		payload["topic"] = "news"
	}
	if len(req.IncludeDomains) > 0 {
		payload["include_domains"] = req.IncludeDomains
	}
	if len(req.ExcludeDomains) > 0 {
		payload["exclude_domains"] = req.ExcludeDomains
	}
	mergeProviderOptions(payload, req, candidate.Provider)
	raw, err := doSearchJSON(c, http.MethodPost, candidate.Channel.GetBaseURL()+"/search", candidate.Config.TimeoutMs, http.Header{}, payload)
	if err != nil {
		return searchProviderResult{}, err
	}
	var decoded struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Content       string   `json:"content"`
			RawContent    *string  `json:"raw_content"`
			Score         *float64 `json:"score"`
			PublishedDate *string  `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return searchProviderResult{}, err
	}
	results := make([]dto.SearchResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		content := (*string)(nil)
		if req.IncludeRawContent && item.RawContent != nil {
			content = item.RawContent
		}
		results = append(results, dto.SearchResult{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Content,
			Content:     content,
			PublishedAt: item.PublishedDate,
			Score:       item.Score,
			Source:      "web",
		})
	}
	answer := optionalAnswer(req.IncludeAnswer, decoded.Answer)
	return searchProviderResult{Results: results, Answer: answer, ProviderPayload: raw}, nil
}

func callSerperSearch(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	payload := map[string]interface{}{
		"q":   req.Query,
		"num": searchMaxResults(req, candidate),
	}
	if country := searchCountry(req); country != "" {
		payload["gl"] = country
	}
	if req.Language != "" {
		payload["hl"] = req.Language
	}
	if tbs := serperTimeRange(req.TimeRange); tbs != "" {
		payload["tbs"] = tbs
	}
	mergeProviderOptions(payload, req, candidate.Provider)
	headers := http.Header{}
	headers.Set("X-API-KEY", firstSearchKey(candidate.Channel.Key))
	endpoint := "/search"
	if req.Type == "news" {
		endpoint = "/news"
	}
	raw, err := doSearchJSON(c, http.MethodPost, candidate.Channel.GetBaseURL()+endpoint, candidate.Config.TimeoutMs, headers, payload)
	if err != nil {
		return searchProviderResult{}, err
	}
	var decoded struct {
		Organic []struct {
			Title    string `json:"title"`
			Link     string `json:"link"`
			Snippet  string `json:"snippet"`
			Date     string `json:"date"`
			Position int    `json:"position"`
		} `json:"organic"`
		News []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
			Date    string `json:"date"`
			Source  string `json:"source"`
		} `json:"news"`
		AnswerBox struct {
			Answer  string `json:"answer"`
			Snippet string `json:"snippet"`
		} `json:"answerBox"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return searchProviderResult{}, err
	}
	results := make([]dto.SearchResult, 0, len(decoded.Organic)+len(decoded.News))
	if req.Type == "news" {
		for _, item := range decoded.News {
			publishedAt := emptyStringAsNil(item.Date)
			results = append(results, dto.SearchResult{
				Title:       item.Title,
				URL:         item.Link,
				Snippet:     item.Snippet,
				PublishedAt: publishedAt,
				Source:      "news",
			})
		}
	} else {
		for _, item := range decoded.Organic {
			publishedAt := emptyStringAsNil(item.Date)
			score := serperPositionScore(item.Position)
			results = append(results, dto.SearchResult{
				Title:       item.Title,
				URL:         item.Link,
				Snippet:     item.Snippet,
				PublishedAt: publishedAt,
				Score:       score,
				Source:      "web",
			})
		}
	}
	answer := optionalAnswer(req.IncludeAnswer, firstNonEmpty(decoded.AnswerBox.Answer, decoded.AnswerBox.Snippet))
	return searchProviderResult{Results: results, Answer: answer, ProviderPayload: raw}, nil
}

func callExaSearch(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	payload := map[string]interface{}{
		"query":      req.Query,
		"numResults": searchMaxResults(req, candidate),
	}
	if req.IncludeRawContent {
		payload["contents"] = map[string]interface{}{"text": true}
	}
	if len(req.IncludeDomains) > 0 {
		payload["includeDomains"] = req.IncludeDomains
	}
	if len(req.ExcludeDomains) > 0 {
		payload["excludeDomains"] = req.ExcludeDomains
	}
	mergeProviderOptions(payload, req, candidate.Provider)
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+firstSearchKey(candidate.Channel.Key))
	raw, err := doSearchJSON(c, http.MethodPost, candidate.Channel.GetBaseURL()+"/search", candidate.Config.TimeoutMs, headers, payload)
	if err != nil {
		return searchProviderResult{}, err
	}
	var decoded struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Text          string   `json:"text"`
			Score         *float64 `json:"score"`
			PublishedDate *string  `json:"publishedDate"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return searchProviderResult{}, err
	}
	results := make([]dto.SearchResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		content := (*string)(nil)
		if req.IncludeRawContent && item.Text != "" {
			text := item.Text
			content = &text
		}
		results = append(results, dto.SearchResult{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Text,
			Content:     content,
			PublishedAt: item.PublishedDate,
			Score:       item.Score,
			Source:      "web",
		})
	}
	return searchProviderResult{Results: results, ProviderPayload: raw}, nil
}

func callBraveSearch(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	endpoint, err := url.Parse(candidate.Channel.GetBaseURL() + "/res/v1/web/search")
	if err != nil {
		return searchProviderResult{}, err
	}
	query := endpoint.Query()
	query.Set("q", req.Query)
	query.Set("count", strconv.Itoa(searchMaxResults(req, candidate)))
	if country := searchCountry(req); country != "" {
		query.Set("country", strings.ToUpper(country))
	}
	if req.Language != "" {
		query.Set("search_lang", req.Language)
	}
	if freshness := braveFreshness(req.TimeRange); freshness != "" {
		query.Set("freshness", freshness)
	}
	endpoint.RawQuery = query.Encode()
	headers := http.Header{}
	headers.Set("X-Subscription-Token", firstSearchKey(candidate.Channel.Key))
	raw, err := doSearch(c, http.MethodGet, endpoint.String(), candidate.Config.TimeoutMs, headers, nil)
	if err != nil {
		return searchProviderResult{}, err
	}
	var decoded struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return searchProviderResult{}, err
	}
	results := make([]dto.SearchResult, 0, len(decoded.Web.Results))
	for _, item := range decoded.Web.Results {
		publishedAt := emptyStringAsNil(item.Age)
		results = append(results, dto.SearchResult{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Description,
			PublishedAt: publishedAt,
			Source:      "web",
		})
	}
	return searchProviderResult{Results: results, ProviderPayload: raw}, nil
}

func callJinaSearch(c *gin.Context, req dto.SearchRequest, candidate searchCandidate) (searchProviderResult, error) {
	endpoint := strings.TrimRight(candidate.Channel.GetBaseURL(), "/") + "/" + url.PathEscape(req.Query)
	headers := http.Header{}
	headers.Set("Accept", "application/json")
	if key := firstSearchKey(candidate.Channel.Key); key != "" {
		headers.Set("Authorization", "Bearer "+key)
	}
	raw, err := doSearch(c, http.MethodGet, endpoint, candidate.Config.TimeoutMs, headers, nil)
	if err != nil {
		return searchProviderResult{}, err
	}
	var decoded struct {
		Data []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return searchProviderResult{}, err
	}
	results := make([]dto.SearchResult, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		content := (*string)(nil)
		if req.IncludeRawContent && item.Content != "" {
			value := item.Content
			content = &value
		}
		results = append(results, dto.SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Content,
			Content: content,
			Source:  "web",
		})
	}
	return searchProviderResult{Results: results, ProviderPayload: raw}, nil
}

func doSearchJSON(c *gin.Context, method string, endpoint string, timeoutMs int, headers http.Header, payload map[string]interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return doSearch(c, method, endpoint, timeoutMs, headers, bytes.NewReader(body))
}

func doSearch(c *gin.Context, method string, endpoint string, timeoutMs int, headers http.Header, body io.Reader) (json.RawMessage, error) {
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	client := GetHttpClient()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Only a real business search that reached the provider updates this field.
	// Administrative tests never set this request-local channel id.
	if channelID := c.GetInt("search_business_channel_id"); channelID > 0 {
		usedAt := common.GetTimestamp()
		if err := model.CoreDB().Model(&model.Channel{}).Where("id = ? AND last_business_used_at < ?", channelID, usedAt).UpdateColumn("last_business_used_at", usedAt).Error; err != nil {
			common.SysLog("failed to record search service last business use")
		}
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, truncateForError(string(raw), 300))
	}
	return json.RawMessage(raw), nil
}

func mergeProviderOptions(payload map[string]interface{}, req dto.SearchRequest, provider string) {
	if len(req.ProviderOptions) == 0 {
		return
	}
	raw := req.ProviderOptions[provider]
	if len(raw) == 0 {
		return
	}
	var extra map[string]interface{}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return
	}
	for key, value := range extra {
		payload[key] = value
	}
}

func searchRelayInfo(c *gin.Context, channel *model.Channel) *relaycommon.RelayInfo {
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if usingGroup == "" {
		usingGroup = model.ChannelLaneDirect
	}
	info := &relaycommon.RelayInfo{
		RequestID:         c.GetString(common.RequestIdKey),
		UserId:            common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenId:           common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenKey:          common.GetContextKeyString(c, constant.ContextKeyTokenKey),
		TokenUnlimited:    common.GetContextKeyBool(c, constant.ContextKeyTokenUnlimited),
		UsingGroup:        usingGroup,
		PricePlanID:       common.GetContextKeyInt(c, constant.ContextKeyUserPricePlanID),
		UserQuota:         common.GetContextKeyMicroUSD(c, constant.ContextKeyUserQuota),
		UserEmail:         common.GetContextKeyString(c, constant.ContextKeyUserEmail),
		OriginModelName:   dto.SearchCapabilityWebSearch,
		RequestURLPath:    c.Request.URL.String(),
		StartTime:         startTime,
		FirstResponseTime: time.Now(),
		RelayFormat:       types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelId:            channel.Id,
			ChannelBaseUrl:       channel.GetBaseURL(),
			ApiKey:               firstSearchKey(channel.Key),
			ChannelOtherSettings: channel.GetOtherSettings(),
		},
	}
	if userSetting, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting); ok {
		info.UserSetting = userSetting
	}
	return info
}

func quoteSearchRequest(config dto.SearchBackendConfig, req dto.SearchRequest, providerCostUSD float64) billing.QuoteResult {
	quantity := searchBillableUnits(config, req)
	priceUSD := config.SearchPrice
	if config.UseProviderCost && providerCostUSD > 0 {
		priceUSD = providerCostUSD
		quantity = 1
	}
	unit := billing.USD(priceUSD)
	amount := billing.MicroUSD(int64(quantity) * unit.Int64())
	line := billing.LineItem{
		Dimension:      usagepkg.DimensionWebSearch,
		Quantity:       int64(quantity),
		UnitMicrousd:   unit,
		AmountMicrousd: amount,
	}
	return billing.QuoteResult{
		LineItems:        []billing.LineItem{line},
		SubtotalMicrousd: amount,
		TotalMicrousd:    amount,
	}
}

func searchBillableUnits(config dto.SearchBackendConfig, req dto.SearchRequest) int {
	switch strings.ToLower(strings.TrimSpace(config.Provider)) {
	case "tavily":
		if strings.EqualFold(strings.TrimSpace(req.SearchDepth), "advanced") {
			return 2
		}
	}
	return 1
}

func searchBillableUnitsFromQuote(quote billing.QuoteResult) int {
	for _, item := range quote.LineItems {
		if item.Dimension == usagepkg.DimensionWebSearch && item.Quantity > 0 {
			return int(item.Quantity)
		}
	}
	return 1
}

func finalizeSearchBilling(c *gin.Context, relayInfo *relaycommon.RelayInfo, quote billing.QuoteResult, started time.Time, candidate searchCandidate, req dto.SearchRequest, receipt dto.SearchRouteReceipt, result searchProviderResult) {
	quota := QuoteToQuota(quote)
	model.UpdateUserUsedQuota(relayInfo.BillingUserID(), quota)
	if quota != 0 {
	}
	quotaDelta := quota - relayInfo.FinalPreConsumedQuota
	if err := PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true); err != nil {
		logger.LogError(c, "error settling search quota: "+err.Error())
	}
	other := map[string]interface{}{
		"request_path":              "/v1/search",
		"search_query":              req.Query,
		"search_type":               req.Type,
		"search_backend":            candidate.Provider,
		"web_search_call_count":     1,
		"web_search_billable_units": searchBillableUnitsFromQuote(quote),
		"routing":                   searchRouteReceiptMap(receipt, candidate),
	}
	if result.ProviderCostUSD > 0 {
		other["provider_cost_usd"] = result.ProviderCostUSD
	}
	model.RecordConsumeLog(c, relayInfo.ActorID(), model.RecordConsumeLogParams{
		ProjectID: relayInfo.ProjectID, PayerUserID: relayInfo.BillingUserID(),
		ChannelId:        candidate.Channel.Id,
		PromptTokens:     0,
		CompletionTokens: 0,
		ModelName:        dto.SearchCapabilityWebSearch,
		TokenName:        c.GetString("token_name"),
		AmountMicrousd:   quote.TotalMicrousd,
		PricingVersion:   quote.PricingVersion,
		LineItems:        QuoteLineItemsJSON(quote),
		Content:          "Search: " + req.Query,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(time.Since(started).Seconds()),
		IsStream:         false,
		Group:            relayInfo.UsingGroup,
		Other:            other,
		UsageFact:        usagepkg.FactFromLegacyLogFields(0, 0, other),
	})
}

func searchRouteReceiptMap(receipt dto.SearchRouteReceipt, candidate searchCandidate) map[string]interface{} {
	selected := candidate.Provider
	if candidate.Channel != nil && candidate.Channel.Name != "" {
		selected = candidate.Channel.Name
	}
	return map[string]interface{}{
		"selection":        receipt.Selection,
		"selected_backend": selected,
		"chose_provider":   candidate.Provider,
		"candidates":       receipt.Candidates,
		"rejected":         receipt.Rejected,
		"downgraded":       receipt.Downgraded,
		"fallback_count":   receipt.FallbackCount,
		"decision_reason":  receipt.DecisionReason,
	}
}

func returnSearchPreConsumedQuota(relayInfo *relaycommon.RelayInfo) {
	if relayInfo != nil && model.GatewayLedgerEnabled() {
		if err := settleRedisLedger(relayInfo, 0, true); err != nil {
			common.SysLog("durable search release: " + err.Error())
		}
		relayInfo.FinalPreConsumedQuota = 0
		return
	}
	if relayInfo == nil {
		return
	}
	releaseSpendBudgetReservationForRelay(relayInfo)
	if relayInfo.FinalPreConsumedQuota == 0 {
		return
	}
	relayInfoCopy := *relayInfo
	if err := PostConsumeQuota(&relayInfoCopy, -relayInfoCopy.FinalPreConsumedQuota, 0, false); err != nil {
		common.SysLog("error returning search pre-consumed quota: " + err.Error())
	}
	relayInfo.FinalPreConsumedQuota = 0
}

func normalizeDomainList(domains []string) []string {
	result := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		domain = normalizeDomain(domain)
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		result = append(result, domain)
	}
	return result
}

func parseDomainList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	return normalizeDomainList(parts)
}

func normalizeDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return ""
	}
	if !strings.Contains(domain, "://") {
		domain = "https://" + domain
	}
	parsed, err := url.Parse(domain)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	return strings.TrimPrefix(host, "www.")
}

func domainAllowed(domain string, allowed []string) bool {
	domain = normalizeDomain(domain)
	if domain == "" {
		return false
	}
	for _, allow := range allowed {
		allow = strings.TrimPrefix(normalizeDomain(allow), "www.")
		if domain == allow || strings.HasSuffix(domain, "."+allow) {
			return true
		}
	}
	return false
}

func filterSearchResultsByDomains(results []dto.SearchResult, allowed []string) []dto.SearchResult {
	if len(results) == 0 || len(allowed) == 0 {
		return results
	}
	filtered := make([]dto.SearchResult, 0, len(results))
	for _, result := range results {
		if domainAllowed(result.URL, allowed) {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func firstSearchKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(raw, "\n")[0])
}

func searchCountry(req dto.SearchRequest) string {
	if req.Country != "" {
		return req.Country
	}
	return req.Region
}

func searchMaxResults(req dto.SearchRequest, candidate searchCandidate) int {
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if candidate.Config.MaxResults > 0 && maxResults > candidate.Config.MaxResults {
		return candidate.Config.MaxResults
	}
	return maxResults
}

func serperTimeRange(value string) string {
	switch value {
	case "day", "d":
		return "qdr:d"
	case "week", "w":
		return "qdr:w"
	case "month", "m":
		return "qdr:m"
	case "year", "y":
		return "qdr:y"
	default:
		return ""
	}
}

func braveFreshness(value string) string {
	switch value {
	case "day", "d":
		return "pd"
	case "week", "w":
		return "pw"
	case "month", "m":
		return "pm"
	case "year", "y":
		return "py"
	default:
		return ""
	}
}

func optionalAnswer(enabled bool, value string) *string {
	value = strings.TrimSpace(value)
	if !enabled || value == "" {
		return nil
	}
	return &value
}

func emptyStringAsNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func serperPositionScore(position int) *float64 {
	if position <= 0 {
		return nil
	}
	score := 1 / float64(position)
	return &score
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func stringSetContains(set map[string]struct{}, value string) bool {
	_, ok := set[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func truncateForError(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

// recordSearchTrafficSample files one backend attempt of a customer's search
// as health evidence. It runs off the request path: a slow insert must not
// hold a search reply that is already in hand.
//
// A backend that rejected the query itself — a 400 or 422 for a malformed or
// unsupported request — told us about the caller, not about its own health,
// and is left out just as ordinary 4xx are for model lanes. An empty result
// list from a customer's query is a legitimate answer and counts as success;
// only the probe, whose query always has results, treats empty as broken.
func recordSearchTrafficSample(candidate searchCandidate, started time.Time, result searchProviderResult, err error) {
	if candidate.Channel == nil || searchErrorIsCallerFault(err) {
		return
	}
	sample := model.SearchHealthSample{
		ChannelId:   candidate.Channel.Id,
		Provider:    candidate.Provider,
		Kind:        model.SearchHealthKindTraffic,
		Success:     err == nil,
		LatencyMs:   int(time.Since(started).Milliseconds()),
		ResultCount: len(result.Results),
		SampledAt:   time.Now().UTC(),
	}
	if err != nil {
		sample.ErrorMessage = err.Error()
	}
	common.SafeGo("search-health-sample", func() {
		if recordErr := model.RecordSearchHealthSample(sample); recordErr != nil {
			common.SysLog("search health: failed to record sample: " + recordErr.Error())
		}
	})
}

// searchErrorIsCallerFault reports an upstream rejection of the request
// itself. Everything else — authentication, quota, rate limits, a missing
// endpoint, a timeout — is ours to fix and stays in the population.
func searchErrorIsCallerFault(err error) bool {
	if err == nil {
		return false
	}
	// Only request validation is the caller's: a 404 from a wrong base path,
	// a 405 from a moved endpoint or a 408 timeout says the backend is
	// broken, and must stay in the population.
	message := err.Error()
	return strings.HasPrefix(message, "upstream returned 400") ||
		strings.HasPrefix(message, "upstream returned 422")
}

// SearchProbeQuery is the fixed question the health probe asks every search
// backend. It is chosen to have results on every engine, every day, so an
// empty answer can only mean the backend is not doing its job.
const SearchProbeQuery = "OpenAI"

// searchProbeMaxResults keeps the probe on the cheapest billable unit every
// backend offers.
const searchProbeMaxResults = 5

// ProbeSearchBackend asks one search backend the fixed probe question and
// reports the attempt as a health sample. The second result says whether a
// provider call was attempted; local setup failures are neither provider
// evidence nor spend and must release the scheduler's budget reservation.
func ProbeSearchBackend(ctx context.Context, channel *model.Channel) (model.SearchHealthSample, bool) {
	sample := model.SearchHealthSample{
		Kind:      model.SearchHealthKindProbe,
		SampledAt: time.Now().UTC(),
	}
	if channel == nil {
		sample.ErrorMessage = "channel unavailable"
		return sample, false
	}
	sample.ChannelId = channel.Id
	provider, config := searchConfigForChannel(channel)
	sample.Provider = provider
	if provider == "" {
		sample.ErrorMessage = "invalid search provider"
		return sample, false
	}
	probeChannel := *channel
	channels := []*model.Channel{&probeChannel}
	if err := model.DecryptChannelKeysForUse(channels); err != nil {
		sample.ErrorMessage = err.Error()
		return sample, false
	}
	if strings.TrimSpace(probeChannel.Key) == "" {
		sample.ErrorMessage = "missing api key"
		return sample, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/v1/search", nil)
	req := dto.SearchRequest{
		Query:       SearchProbeQuery,
		Type:        "web",
		Backend:     provider,
		MaxResults:  searchProbeMaxResults,
		SearchDepth: "basic",
	}
	candidate := searchCandidate{Channel: &probeChannel, Provider: provider, Config: config}
	started := time.Now()
	result, err := callSearchProvider(c, req, candidate)
	sample.LatencyMs = int(time.Since(started).Milliseconds())
	sample.ResultCount = len(result.Results)
	sample.SampledAt = time.Now().UTC()
	switch {
	case err != nil:
		sample.ErrorMessage = err.Error()
	case sample.ResultCount == 0:
		sample.ErrorMessage = "search returned no results"
	default:
		sample.Success = true
	}
	return sample, true
}

// SearchChannelServesWebSearch reports whether /v1/search would route a
// query to this channel at all: its provider is known and its configured
// capabilities include web search.
func SearchChannelServesWebSearch(channel *model.Channel) bool {
	provider, config := searchConfigForChannel(channel)
	return provider != "" && searchCapabilitiesInclude(config.Capabilities, dto.SearchCapabilityWebSearch)
}

// SearchProvidersAllowedForToken returns the search providers the request's
// token may use, and whether the token restricts search at all. A restricted
// token with an empty list may use no search provider.
func SearchProvidersAllowedForToken(c *gin.Context) (map[string]struct{}, bool) {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenSearchLimitEnabled) {
		return nil, false
	}
	return tokenSearchProviders(c), true
}
