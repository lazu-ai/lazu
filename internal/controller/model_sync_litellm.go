package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
)

const (
	litellmModelMapURL      = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	litellmMaxBodyBytes     = 15 << 20 // 15MB
	litellmMaxRetryInterval = 10 * time.Second
)

// flexNumber tolerates LiteLLM's inconsistent JSON, where a numeric field may
// arrive as a JSON number, a quoted string, null, or empty. Anything that does
// not parse degrades to 0 instead of failing the whole document.
type flexNumber float64

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = flexNumber(v)
	return nil
}

// litellmModel represents a model entry from the LiteLLM model_prices JSON.
type litellmModel struct {
	MaxTokens               flexNumber `json:"max_tokens"`
	MaxInputTokens          flexNumber `json:"max_input_tokens"`
	MaxOutputTokens         flexNumber `json:"max_output_tokens"`
	InputCostPerToken       flexNumber `json:"input_cost_per_token"`
	OutputCostPerToken      flexNumber `json:"output_cost_per_token"`
	CacheReadCostPerToken   flexNumber `json:"cache_read_input_token_cost"`
	CacheWriteCostPerToken  flexNumber `json:"cache_creation_input_token_cost"`
	Mode                    string     `json:"mode"`
	DeprecationDate         string     `json:"deprecation_date"`
	SupportsVision          bool       `json:"supports_vision"`
	SupportsFunctionCalling bool       `json:"supports_function_calling"`
	SupportsToolChoice      bool       `json:"supports_tool_choice"`
	SupportsReasoning       bool       `json:"supports_reasoning"`
	SupportsWebSearch       bool       `json:"supports_web_search"`
	SupportsResponseSchema  bool       `json:"supports_response_schema"`
	SupportsAudioInput      bool       `json:"supports_audio_input"`
	SupportsAudioOutput     bool       `json:"supports_audio_output"`
	SupportsPdfInput        bool       `json:"supports_pdf_input"`
	SupportsComputerUse     bool       `json:"supports_computer_use"`
	SupportsPromptCaching   bool       `json:"supports_prompt_caching"`
}

type syncLiteLLMRequest struct {
	Force bool `json:"force"`
}

type litellmMatchResult struct {
	LocalModel     string `json:"local_model"`
	LiteLLMKey     string `json:"litellm_key"`
	HasPricing     bool   `json:"has_pricing"`
	HasDeprecation bool   `json:"has_deprecation"`
}

// fetchLiteLLMModels downloads the LiteLLM model pricing JSON from GitHub.
// Uses the package-level httpClient and ETag/body cache defined in model_sync.go.
func fetchLiteLLMModels(ctx context.Context) (map[string]litellmModel, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, litellmModelMapURL, nil)
		if err != nil {
			return nil, err
		}
		// ETag conditional request
		cacheMutex.RLock()
		if et := etagCache[litellmModelMapURL]; et != "" {
			req.Header.Set("If-None-Match", et)
		}
		cacheMutex.RUnlock()

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(min(time.Duration(1<<attempt)*time.Second, litellmMaxRetryInterval))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, litellmMaxBodyBytes))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			// Cache body and ETag for future requests
			cacheMutex.Lock()
			if et := resp.Header.Get("ETag"); et != "" {
				etagCache[litellmModelMapURL] = et
			}
			bodyCache[litellmModelMapURL] = body
			cacheMutex.Unlock()
		case http.StatusNotModified:
			// Use cached body
			cacheMutex.RLock()
			body = bodyCache[litellmModelMapURL]
			cacheMutex.RUnlock()
			if len(body) == 0 {
				lastErr = fmt.Errorf("304 Not Modified but no cached body")
				continue
			}
		default:
			lastErr = fmt.Errorf("LiteLLM raw URL returned %d", resp.StatusCode)
			time.Sleep(min(time.Duration(1<<attempt)*time.Second, litellmMaxRetryInterval))
			continue
		}

		var result map[string]litellmModel
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse LiteLLM JSON: %w", err)
		}
		return result, nil
	}
	return nil, fmt.Errorf("failed after 3 attempts: %w", lastErr)
}

// isResolutionPrefix checks whether a key starts with a resolution-like prefix (e.g. "1024x1024/").
func isResolutionPrefix(key string) bool {
	if idx := strings.Index(key, "/"); idx > 0 {
		prefix := key[:idx]
		for _, c := range prefix {
			if c != 'x' && (c < '0' || c > '9') {
				return false
			}
		}
		return true
	}
	return false
}

// buildLiteLLMIndex builds lookup maps from the LiteLLM model map.
// fullMap contains the original keys (lowered), strippedMap contains keys with provider prefix removed.
// Non-chat modes and resolution-prefixed keys are filtered out.
func buildLiteLLMIndex(models map[string]litellmModel) (map[string]*litellmModel, map[string]*litellmModel) {
	fullMap := make(map[string]*litellmModel, len(models))
	strippedMap := make(map[string]*litellmModel, len(models))

	for key, m := range models {
		// Filter out non-chat modes
		if m.Mode != "" && m.Mode != "chat" && m.Mode != "completion" {
			continue
		}
		// Filter out resolution-prefixed keys (e.g. "1024x1024/dall-e-3")
		if isResolutionPrefix(key) {
			continue
		}

		lower := strings.ToLower(key)
		mCopy := m
		fullMap[lower] = &mCopy

		// Strip provider prefix (e.g. "openai/gpt-4o" → "gpt-4o")
		if idx := strings.Index(key, "/"); idx >= 0 {
			stripped := strings.ToLower(key[idx+1:])
			if _, exists := strippedMap[stripped]; !exists {
				strippedMap[stripped] = &mCopy
			}
		}
	}
	return fullMap, strippedMap
}

// matchLocalModelLiteLLM tries to match a local model name against LiteLLM models.
// Uses the same 4-step strategy as OpenRouter matching.
func matchLocalModelLiteLLM(localName string, fullMap map[string]*litellmModel, strippedMap map[string]*litellmModel) (*litellmModel, string) {
	lower := strings.ToLower(localName)

	// 1. Exact full key match
	if m, ok := fullMap[lower]; ok {
		return m, lower
	}

	// 2. Stripped name match
	if m, ok := strippedMap[lower]; ok {
		return m, lower
	}

	// 3. Strip date suffix and retry
	base := stripDateSuffix(lower)
	if base != lower {
		if m, ok := fullMap[base]; ok {
			return m, base
		}
		if m, ok := strippedMap[base]; ok {
			return m, base
		}
	}

	// 4. Strip common suffixes and retry
	for _, suffix := range []string{"-latest", "-preview"} {
		trimmed := strings.TrimSuffix(base, suffix)
		if trimmed != base {
			if m, ok := fullMap[trimmed]; ok {
				return m, trimmed
			}
			if m, ok := strippedMap[trimmed]; ok {
				return m, trimmed
			}
		}
	}

	return nil, ""
}

// generateTagsFromLiteLLM generates capability tags from LiteLLM model supports_* fields.
func generateTagsFromLiteLLM(m *litellmModel) []string {
	tagSet := make(map[string]bool)

	if m.SupportsVision {
		tagSet["多模态"] = true
		tagSet["图片理解"] = true
	}
	if m.SupportsFunctionCalling || m.SupportsToolChoice {
		tagSet["工具调用"] = true
	}
	if m.SupportsReasoning {
		tagSet["推理"] = true
	}
	if m.SupportsWebSearch {
		tagSet["搜索"] = true
	}
	if m.SupportsResponseSchema {
		tagSet["结构化输出"] = true
	}
	if m.SupportsAudioInput || m.SupportsAudioOutput {
		tagSet["音频"] = true
	}
	if m.SupportsPdfInput {
		tagSet["文件理解"] = true
	}
	if m.SupportsComputerUse {
		tagSet["计算机使用"] = true
	}
	if m.SupportsPromptCaching {
		tagSet["缓存"] = true
	}

	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

func capabilitiesFromLiteLLM(m *litellmModel) model.ModelCapabilities {
	if m == nil {
		return model.ModelCapabilities{}
	}
	return model.ModelCapabilities{
		SupportsFunctionCalling:         m.SupportsFunctionCalling || m.SupportsToolChoice,
		SupportsParallelFunctionCalling: m.SupportsFunctionCalling && m.SupportsToolChoice,
		SupportsVision:                  m.SupportsVision,
		SupportsReasoning:               m.SupportsReasoning,
		SupportsResponseSchema:          m.SupportsResponseSchema,
		SupportsPromptCaching:           m.SupportsPromptCaching,
		SupportsAudioInput:              m.SupportsAudioInput,
		SupportsAudioOutput:             m.SupportsAudioOutput,
		SupportsWebSearch:               m.SupportsWebSearch,
		SupportsPDFInput:                m.SupportsPdfInput,
		SupportsComputerUse:             m.SupportsComputerUse,
	}
}

// SyncLiteLLMPreview returns a preview of which local models match LiteLLM data.
func SyncLiteLLMPreview(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	litellmModels, err := fetchLiteLLMModels(ctx)
	if err != nil {
		httperr.Write(c, modelSyncSourceFailed("litellm", err))
		return
	}

	fullMap, strippedMap := buildLiteLLMIndex(litellmModels)

	var localModels []model.Model
	if err := model.CoreDB().Select("id, model_name").Find(&localModels).Error; err != nil {
		httperr.Write(c, err)
		return
	}

	var matched []litellmMatchResult
	var unmatched []string

	for _, lm := range localModels {
		llmModel, matchedKey := matchLocalModelLiteLLM(lm.ModelName, fullMap, strippedMap)
		if llmModel == nil {
			unmatched = append(unmatched, lm.ModelName)
			continue
		}

		matched = append(matched, litellmMatchResult{
			LocalModel:     lm.ModelName,
			LiteLLMKey:     matchedKey,
			HasPricing:     llmModel.InputCostPerToken > 0 || llmModel.OutputCostPerToken > 0,
			HasDeprecation: llmModel.DeprecationDate != "",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"litellm_total":   len(fullMap),
			"local_total":     len(localModels),
			"matched_count":   len(matched),
			"unmatched_count": len(unmatched),
			"matched":         matched,
			"unmatched":       unmatched,
		},
	})
}

// SyncLiteLLM synchronizes model pricing and metadata from LiteLLM using SSE for progress.
func SyncLiteLLM(c *gin.Context) {
	var req syncLiteLLMRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}

	// Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	sendSSE := func(event string, data interface{}) bool {
		jsonData, _ := json.Marshal(data)
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(jsonData)); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}

	type progressMsg struct {
		Phase   string `json:"phase"`
		Current int    `json:"current"`
		Total   int    `json:"total"`
		Message string `json:"message"`
	}

	sendSSE("progress", progressMsg{Phase: "fetch", Message: "Fetching LiteLLM pricing data..."})

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	litellmModels, err := fetchLiteLLMModels(ctx)
	if err != nil {
		sendSSE("error", modelSyncStreamError(c, modelSyncSourceFailed("litellm", err)))
		return
	}

	fullMap, strippedMap := buildLiteLLMIndex(litellmModels)

	sendSSE("progress", progressMsg{Phase: "fetch", Message: fmt.Sprintf("Fetched %d LiteLLM models", len(fullMap))})

	var localModels []model.Model
	if err := model.CoreDB().Find(&localModels).Error; err != nil {
		sendSSE("error", modelSyncStreamError(c, fmt.Errorf("load local models: %w", err)))
		return
	}

	// Sync counters
	matchedCount := 0
	skippedCount := 0
	updatedPricing := 0
	updatedMaxOutput := 0
	updatedContext := 0
	updatedCapabilities := 0
	updatedDeprecation := 0
	totalLocal := len(localModels)
	now := common.GetTimestamp()

	// Collected sell-price entries to upsert after the catalog transaction commits.
	// model_sell_prices upserts run on the core store outside tx, so we only
	// write them once the catalog sync has committed successfully.
	type pendingSellPrice struct {
		modelName     string
		inputPerMtok  float64
		outputPerMtok float64
	}
	var pendingSellPrices []pendingSellPrice

	sendSSE("progress", progressMsg{Phase: "sync", Current: 0, Total: totalLocal, Message: "Starting model data sync..."})

	tx := model.CoreDB().Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for i, lm := range localModels {
		llmModel, _ := matchLocalModelLiteLLM(lm.ModelName, fullMap, strippedMap)
		if llmModel == nil {
			skippedCount++
			continue
		}
		matchedCount++

		updates := make(map[string]interface{})
		updates["last_seen_at"] = now
		if strings.TrimSpace(lm.Source) == "" {
			updates["source"] = model.ModelSourceLiteLLM
		}

		// Update official pricing. LiteLLM publishes USD per token; the
		// catalog stores USD per million tokens everywhere else.
		if llmModel.InputCostPerToken > 0 || llmModel.OutputCostPerToken > 0 {
			if lm.OfficialInputPrice == 0 || req.Force {
				updates["official_input_price"] = float64(llmModel.InputCostPerToken) * 1_000_000
			}
			if lm.OfficialOutputPrice == 0 || req.Force {
				updates["official_output_price"] = float64(llmModel.OutputCostPerToken) * 1_000_000
			}
			if _, ok := updates["official_input_price"]; ok {
				updatedPricing++
			} else if _, ok := updates["official_output_price"]; ok {
				updatedPricing++
			}
		}
		// Cache prices are stored per million tokens.
		if llmModel.CacheReadCostPerToken > 0 && (lm.OfficialCacheReadPerMtok == 0 || req.Force) {
			updates["official_cache_read_per_mtok"] = float64(llmModel.CacheReadCostPerToken) * 1_000_000
		}
		if llmModel.CacheWriteCostPerToken > 0 && (lm.OfficialCacheWritePerMtok == 0 || req.Force) {
			updates["official_cache_write_per_mtok"] = float64(llmModel.CacheWriteCostPerToken) * 1_000_000
		}

		// Update max_output_tokens
		if llmModel.MaxOutputTokens > 0 && (lm.MaxOutputTokens == 0 || req.Force) {
			updates["max_output_tokens"] = int64(llmModel.MaxOutputTokens)
			updatedMaxOutput++
		}

		// Update context_length from max_input_tokens (supplement only when empty)
		if llmModel.MaxInputTokens > 0 && lm.ContextLength == 0 {
			updates["context_length"] = int64(llmModel.MaxInputTokens)
			updatedContext++
		}

		// Update deprecation_date
		if llmModel.DeprecationDate != "" && (lm.DeprecationDate == "" || req.Force) {
			updates["deprecation_date"] = llmModel.DeprecationDate
			updatedDeprecation++
		}
		capabilities := capabilitiesFromLiteLLM(llmModel)
		if capabilities.HasAny() && (req.Force || capabilities != lm.Capabilities) {
			updates["capabilities"] = capabilities
			updatedCapabilities++
		}

		// Collect a sell-price entry when LiteLLM has pricing. Sell price is the
		// official LiteLLM price (markup 1.0x), converted from per-token to
		// per-million-tokens. Skip models that already have an active sell price
		// unless Force is set, to avoid clobbering existing seed rows.
		if llmModel.InputCostPerToken > 0 || llmModel.OutputCostPerToken > 0 {
			// A price that follows the official price is never clobbered: it
			// moves with the official price the sync just wrote.
			if active, hasActive := model.GetActiveModelSellPrice(lm.ModelName); !hasActive || (req.Force && !active.FollowOfficial) {
				pendingSellPrices = append(pendingSellPrices, pendingSellPrice{
					modelName:     lm.ModelName,
					inputPerMtok:  float64(llmModel.InputCostPerToken) * 1_000_000,
					outputPerMtok: float64(llmModel.OutputCostPerToken) * 1_000_000,
				})
			}
		}

		// Apply DB updates within transaction
		if len(updates) > 0 {
			tx.Model(&model.Model{}).Where("id = ?", lm.Id).Updates(updates)
		}

		// Send progress adaptively: every max(1, total/20) models, ensuring at least 20 updates
		progressInterval := max(1, totalLocal/20)
		if (i+1)%progressInterval == 0 || i+1 == totalLocal {
			if !sendSSE("progress", progressMsg{
				Phase:   "sync",
				Current: i + 1,
				Total:   totalLocal,
				Message: fmt.Sprintf("Processed %d/%d models, matched %d, skipped %d", i+1, totalLocal, matchedCount, skippedCount),
			}) {
				tx.Rollback()
				return // client disconnected
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		sendSSE("error", modelSyncStreamError(c, fmt.Errorf("commit litellm sync: %w", err)))
		return
	}

	// Upsert sell prices only after the catalog transaction has committed, so
	// model_sell_prices rows are never written for a sync that was rolled back.
	updatedSellPrices := 0
	if len(pendingSellPrices) > 0 {
		sendSSE("progress", progressMsg{
			Phase:   "sell-prices",
			Current: 0,
			Total:   len(pendingSellPrices),
			Message: fmt.Sprintf("Writing sell prices for %d models...", len(pendingSellPrices)),
		})
		for _, entry := range pendingSellPrices {
			sellPrice := model.ModelSellPrice{
				ModelName:     entry.modelName,
				InputPerMtok:  &entry.inputPerMtok,
				OutputPerMtok: &entry.outputPerMtok,
				Source:        "litellm_sync",
			}
			if err := model.UpsertActiveModelSellPrice(sellPrice); err != nil {
				common.SysLog(fmt.Sprintf("failed to upsert sell price for %s: %s", entry.modelName, err.Error()))
				continue
			}
			updatedSellPrices++
		}
	}
	model.RecomputeFollowingSellPricesLogged(nil, nil, "litellm sync")

	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()

	sendSSE("done", gin.H{
		"success":              true,
		"litellm_total":        len(fullMap),
		"matched":              matchedCount,
		"updated_pricing":      updatedPricing,
		"updated_max_output":   updatedMaxOutput,
		"updated_context":      updatedContext,
		"updated_capabilities": updatedCapabilities,
		"updated_deprecation":  updatedDeprecation,
		"updated_sell_prices":  updatedSellPrices,
		"skipped":              skippedCount,
	})
}
