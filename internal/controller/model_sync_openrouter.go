package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

const (
	openRouterModelsURL          = "https://openrouter.ai/api/v1/models"
	openRouterModelsMaxBodyBytes = 10 << 20
	translationRequestTimeout    = 60 * time.Second
)

// translationHTTPClient is used for translation relay calls.
var translationHTTPClient = &http.Client{
	Timeout: translationRequestTimeout,
}

// openRouterArchitecture represents the architecture info from OpenRouter.
type openRouterArchitecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Tokenizer        string   `json:"tokenizer"`
}

// openRouterModel represents a model from the OpenRouter API response.
type openRouterModel struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Created             int64                  `json:"created"`
	Description         string                 `json:"description"`
	ContextLength       int64                  `json:"context_length"`
	Architecture        openRouterArchitecture `json:"architecture"`
	SupportedParameters []string               `json:"supported_parameters"`
}

type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

type syncOpenRouterRequest struct {
	Translate bool   `json:"translate"`
	Model     string `json:"model"`
	Force     bool   `json:"force"`
}

type openRouterMatchResult struct {
	LocalModel    string `json:"local_model"`
	OpenRouterID  string `json:"openrouter_id"`
	HasReleasedAt bool   `json:"has_released_at"`
	HasDescEn     bool   `json:"has_description_en"`
	HasDescZh     bool   `json:"has_description_zh"`
}

// fetchOpenRouterModels fetches the model list from OpenRouter's public API.
func fetchOpenRouterModels(ctx context.Context) ([]openRouterModel, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, openRouterModelsURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, openRouterModelsMaxBodyBytes))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("OpenRouter API returned %d", resp.StatusCode)
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		var result openRouterResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse OpenRouter response: %w", err)
		}
		return result.Data, nil
	}
	return nil, fmt.Errorf("failed after 3 attempts: %w", lastErr)
}

// buildOpenRouterIndex builds lookup maps from OpenRouter models.
func buildOpenRouterIndex(orModels []openRouterModel) (map[string]*openRouterModel, map[string]*openRouterModel) {
	fullMap := make(map[string]*openRouterModel, len(orModels))
	strippedMap := make(map[string]*openRouterModel, len(orModels))

	for i := range orModels {
		m := &orModels[i]
		lower := strings.ToLower(m.ID)
		fullMap[lower] = m

		if idx := strings.Index(m.ID, "/"); idx >= 0 {
			stripped := strings.ToLower(m.ID[idx+1:])
			if _, exists := strippedMap[stripped]; !exists {
				strippedMap[stripped] = m
			}
		}
	}
	return fullMap, strippedMap
}

func isShortNumericToken(s string) bool {
	if len(s) < 1 || len(s) > 2 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func normalizeVersionSeparators(name string) string {
	if !strings.Contains(name, "-") {
		return name
	}
	parts := strings.Split(name, "-")
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		if i+1 < len(parts) && isShortNumericToken(parts[i]) && isShortNumericToken(parts[i+1]) {
			out = append(out, parts[i]+"."+parts[i+1])
			i++
			continue
		}
		out = append(out, parts[i])
	}
	return strings.Join(out, "-")
}

func buildMatchCandidates(localName string) []string {
	seed := strings.ToLower(strings.TrimSpace(localName))
	if seed == "" {
		return nil
	}

	seen := make(map[string]struct{})
	queue := []string{seed}
	ordered := make([]string, 0, 12)

	add := func(s string) {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		queue = append(queue, s)
		ordered = append(ordered, s)
	}

	add(seed)
	for i := 0; i < len(queue); i++ {
		current := queue[i]

		add(stripDateSuffix(current))
		add(normalizeVersionSeparators(current))
		add(strings.ReplaceAll(current, ".", "-"))

		for _, suffix := range []string{"-latest", "-preview", "-thinking", "-think", ":thinking"} {
			trimmed := strings.TrimSuffix(current, suffix)
			if trimmed != current {
				add(trimmed)
				add(normalizeVersionSeparators(trimmed))
			}
		}

		if strings.HasSuffix(current, "-thinking") {
			add(strings.TrimSuffix(current, "-thinking") + ":thinking")
		}
		if strings.HasSuffix(current, "-think") {
			add(strings.TrimSuffix(current, "-think") + ":thinking")
		}
	}

	return ordered
}

// matchLocalModel tries to match a local model name against OpenRouter models.
// Matching strategy:
//  1. Exact full ID match (e.g. "openai/gpt-4o")
//  2. Stripped name match (e.g. "gpt-4o")
//  3. Strip date suffix and retry (e.g. "gpt-4o-2024-08-06" → "gpt-4o")
//  4. Strip common suffixes like "-latest" and retry
func matchLocalModel(localName string, fullMap map[string]*openRouterModel, strippedMap map[string]*openRouterModel) *openRouterModel {
	for _, candidate := range buildMatchCandidates(localName) {
		if m, ok := fullMap[candidate]; ok {
			return m
		}
		if m, ok := strippedMap[candidate]; ok {
			return m
		}
	}
	return nil
}

// stripDateSuffix removes trailing date patterns from a model name.
func stripDateSuffix(name string) string {
	// Pattern 1: -YYYY-MM-DD at end
	if len(name) > 11 {
		tail := name[len(name)-11:]
		if tail[0] == '-' && isDigits(tail[1:5]) && tail[5] == '-' && isDigits(tail[6:8]) && tail[8] == '-' && isDigits(tail[9:11]) {
			return name[:len(name)-11]
		}
	}

	// Pattern 2: -YYYYMMDD at end (8 digits)
	if len(name) > 9 {
		tail := name[len(name)-9:]
		if tail[0] == '-' && isDigits(tail[1:]) {
			if strings.HasPrefix(tail[1:], "20") || strings.HasPrefix(tail[1:], "19") {
				return name[:len(name)-9]
			}
		}
	}

	// Pattern 3: -MMDD at end (4 digits like -0125)
	if len(name) > 5 {
		tail := name[len(name)-5:]
		if tail[0] == '-' && isDigits(tail[1:]) {
			month := (tail[1]-'0')*10 + (tail[2] - '0')
			if month >= 1 && month <= 12 {
				return name[:len(name)-5]
			}
		}
	}

	return name
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// generateTagsFromOpenRouter generates UI-only tags from OpenRouter model data.
// Structured capabilities are stored separately and should not be encoded as tags.
func generateTagsFromOpenRouter(orModel *openRouterModel) []string {
	tagSet := make(map[string]bool)

	idLower := strings.ToLower(orModel.ID)
	openSourcePrefixes := []string{
		"meta-llama/", "mistralai/", "google/gemma", "qwen/",
		"deepseek/", "microsoft/", "nvidia/", "cohere/command-r",
	}
	for _, prefix := range openSourcePrefixes {
		if strings.HasPrefix(idLower, prefix) {
			tagSet["开源"] = true
			break
		}
	}

	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

func capabilitiesFromOpenRouter(orModel *openRouterModel) model.ModelCapabilities {
	if orModel == nil {
		return model.ModelCapabilities{}
	}
	capabilities := model.ModelCapabilities{}
	for _, modality := range orModel.Architecture.InputModalities {
		switch strings.ToLower(modality) {
		case "image":
			capabilities.SupportsVision = true
		case "audio":
			capabilities.SupportsAudioInput = true
		case "file":
			capabilities.SupportsPDFInput = true
		}
	}
	for _, modality := range orModel.Architecture.OutputModalities {
		if strings.EqualFold(modality, "audio") {
			capabilities.SupportsAudioOutput = true
		}
	}
	for _, parameter := range orModel.SupportedParameters {
		switch parameter {
		case "tools", "tool_choice":
			capabilities.SupportsFunctionCalling = true
		case "parallel_tool_calls":
			capabilities.SupportsFunctionCalling = true
			capabilities.SupportsParallelFunctionCalling = true
		case "reasoning", "reasoning_effort", "include_reasoning":
			capabilities.SupportsReasoning = true
		case "response_format", "structured_outputs":
			capabilities.SupportsResponseSchema = true
		case "web_search_options":
			capabilities.SupportsWebSearch = true
		}
	}
	return capabilities
}

// contextLengthPattern matches tags that look like context length values (e.g. "128k", "1m", "202.8k").
var contextLengthPattern = regexp.MustCompile(`(?i)^\d+\.?\d*[kmKM]$`)

// filterContextLengthTags removes context-length-like values from a comma-separated tag string.
func filterContextLengthTags(tags string) string {
	if tags == "" {
		return ""
	}
	parts := strings.Split(tags, ",")
	var filtered []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if contextLengthPattern.MatchString(p) {
			continue
		}
		filtered = append(filtered, p)
	}
	return strings.Join(filtered, ",")
}

// mergeTagStrings merges existing comma-separated tags with new tags, deduplicating.
func mergeTagStrings(existing string, newTags []string) string {
	tagSet := make(map[string]bool)
	var ordered []string

	// Add existing tags first (preserving order, filtering context lengths)
	if existing != "" {
		for _, t := range strings.Split(existing, ",") {
			t = strings.TrimSpace(t)
			if t == "" || contextLengthPattern.MatchString(t) {
				continue
			}
			lower := strings.ToLower(t)
			if !tagSet[lower] {
				tagSet[lower] = true
				ordered = append(ordered, t)
			}
		}
	}

	// Add new tags
	for _, t := range newTags {
		t = strings.TrimSpace(t)
		lower := strings.ToLower(t)
		if t != "" && !tagSet[lower] {
			tagSet[lower] = true
			ordered = append(ordered, t)
		}
	}

	return strings.Join(ordered, ",")
}

// openRouterLinkPattern matches markdown links pointing to openrouter.ai.
var openRouterLinkPattern = regexp.MustCompile(`\[([^\]]*)\]\(https?://openrouter\.ai[^\)]*\)`)

// openRouterSentencePattern matches sentences that reference OpenRouter docs/links.
var openRouterSentencePattern = regexp.MustCompile(`(?i)[^.。]*(?:openrouter\.ai|see our \[|参阅我们的|请参阅)[^.。]*[.。]?\s*`)

// cleanOpenRouterReferences removes OpenRouter-specific references from descriptions.
func cleanOpenRouterReferences(desc string) string {
	if desc == "" {
		return ""
	}

	// Replace markdown links [text](https://openrouter.ai/...) with just the link text
	desc = openRouterLinkPattern.ReplaceAllString(desc, "$1")

	// Remove sentences that are primarily about OpenRouter documentation
	desc = openRouterSentencePattern.ReplaceAllString(desc, "")

	// Clean up excess whitespace
	desc = strings.TrimSpace(desc)
	// Collapse multiple newlines
	for strings.Contains(desc, "\n\n\n") {
		desc = strings.ReplaceAll(desc, "\n\n\n", "\n\n")
	}

	return desc
}

// getAdminTokenKey finds the first active token for the given admin user.
func getAdminTokenKey(adminUserID int, translationModel string) (string, error) {
	var tokens []model.Token
	if err := model.CoreDB().
		Where("user_id = ? AND status = ?", adminUserID, common.TokenStatusEnabled).
		Order("id ASC").
		Find(&tokens).Error; err != nil {
		return "", types.WrapErr(fmt.Errorf("load admin tokens: %w", err), types.ErrorCodeInternalError)
	}
	if len(tokens) == 0 {
		return "", types.Err(types.ErrorCodeTranslationTokenMissing, types.D{"model": translationModel})
	}

	now := common.GetTimestamp()
	var fallbackTokenKey string
	for i := range tokens {
		tk := &tokens[i]
		// Skip expired tokens even if status wasn't refreshed yet.
		if tk.ExpiredTime != -1 && tk.ExpiredTime < now {
			continue
		}
		if !tokenCanUseModel(tk, translationModel) {
			continue
		}
		// Prefer model-scoped tokens to minimize privilege exposure.
		if tk.IsModelLimitsEnabled() {
			return tk.Key, nil
		}
		if fallbackTokenKey == "" {
			fallbackTokenKey = tk.Key
		}
	}
	if fallbackTokenKey == "" {
		return "", types.Err(types.ErrorCodeTranslationTokenMissing, types.D{"model": translationModel})
	}
	return fallbackTokenKey, nil
}

func tokenCanUseModel(token *model.Token, modelName string) bool {
	if token == nil || modelName == "" {
		return false
	}
	if !token.IsModelLimitsEnabled() {
		return true
	}
	for _, limitedModel := range token.GetModelLimits() {
		if strings.EqualFold(strings.TrimSpace(limitedModel), modelName) {
			return true
		}
	}
	return false
}

func loadModelMetadataMap() (map[string]*model.ModelMetadata, error) {
	var allMeta []model.ModelMetadata
	if err := model.CoreDB().Select("model_name, released_at").Find(&allMeta).Error; err != nil {
		return nil, err
	}
	metaMap := make(map[string]*model.ModelMetadata, len(allMeta))
	for i := range allMeta {
		metaMap[allMeta[i].ModelName] = &allMeta[i]
	}
	return metaMap, nil
}

// SyncOpenRouterPreview returns a preview of which local models match OpenRouter data.
func SyncOpenRouterPreview(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	orModels, err := fetchOpenRouterModels(ctx)
	if err != nil {
		httperr.Write(c, modelSyncSourceFailed("openrouter", err))
		return
	}

	fullMap, strippedMap := buildOpenRouterIndex(orModels)

	var localModels []model.Model
	if err := model.CoreDB().Select("id, model_name, description_en, description_zh").Find(&localModels).Error; err != nil {
		httperr.Write(c, err)
		return
	}

	metaMap, err := loadModelMetadataMap()
	if err != nil {
		httperr.Write(c, err)
		return
	}

	var matched []openRouterMatchResult
	var unmatched []string

	for _, lm := range localModels {
		orModel := matchLocalModel(lm.ModelName, fullMap, strippedMap)
		if orModel == nil {
			unmatched = append(unmatched, lm.ModelName)
			continue
		}

		result := openRouterMatchResult{
			LocalModel:   lm.ModelName,
			OpenRouterID: orModel.ID,
			HasDescEn:    lm.DescriptionEn != "",
			HasDescZh:    lm.DescriptionZh != "",
		}
		if mm, ok := metaMap[lm.ModelName]; ok && mm.ReleasedAt != nil {
			result.HasReleasedAt = true
		}
		matched = append(matched, result)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"openrouter_total": len(orModels),
			"local_total":      len(localModels),
			"matched_count":    len(matched),
			"unmatched_count":  len(unmatched),
			"matched":          matched,
			"unmatched":        unmatched,
		},
	})
}

// SyncOpenRouter synchronizes model data from OpenRouter's public API using SSE for progress.
func SyncOpenRouter(c *gin.Context) {
	var req syncOpenRouterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}

	// Auto-resolve token key for translation from the current admin user
	adminUserID := c.GetInt("id")
	var tokenKey string
	translationModel := strings.TrimSpace(req.Model)
	if translationModel == "" {
		translationModel = "gpt-4o-mini"
	}
	if req.Translate {
		var err error
		tokenKey, err = getAdminTokenKey(adminUserID, translationModel)
		if err != nil {
			httperr.Write(c, err)
			return
		}
	}

	// Set SSE headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	sendSSE := func(event string, data interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(jsonData))
		c.Writer.Flush()
	}

	type progressMsg struct {
		Phase   string `json:"phase"`
		Current int    `json:"current"`
		Total   int    `json:"total"`
		Message string `json:"message"`
	}

	sendSSE("progress", progressMsg{Phase: "fetch", Message: "Fetching OpenRouter model data..."})

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	orModels, err := fetchOpenRouterModels(ctx)
	if err != nil {
		sendSSE("error", modelSyncStreamError(c, modelSyncSourceFailed("openrouter", err)))
		return
	}

	fullMap, strippedMap := buildOpenRouterIndex(orModels)

	sendSSE("progress", progressMsg{Phase: "fetch", Message: fmt.Sprintf("Fetched %d OpenRouter models", len(orModels))})

	var localModels []model.Model
	if err := model.CoreDB().Find(&localModels).Error; err != nil {
		sendSSE("error", modelSyncStreamError(c, fmt.Errorf("load local models: %w", err)))
		return
	}

	metaMap, err := loadModelMetadataMap()
	if err != nil {
		sendSSE("error", modelSyncStreamError(c, fmt.Errorf("load model metadata: %w", err)))
		return
	}

	// Phase 1: Match & update metadata + descriptions + tags
	updatedReleasedAt := 0
	updatedDescEn := 0
	updatedTags := 0
	updatedCapabilities := 0
	skippedCount := 0
	matchedCount := 0
	now := common.GetTimestamp()
	needTranslation := make(map[string]string)

	totalLocal := len(localModels)
	sendSSE("progress", progressMsg{Phase: "sync", Current: 0, Total: totalLocal, Message: "Starting model data sync..."})

	for i, lm := range localModels {
		orModel := matchLocalModel(lm.ModelName, fullMap, strippedMap)
		if orModel == nil {
			// Even for unmatched models, clean up context-length tags
			cleaned := filterContextLengthTags(lm.Tags)
			if cleaned != lm.Tags {
				model.CoreDB().
					Model(&model.Model{}).Where("id = ?", lm.Id).Update("tags", cleaned)
				updatedTags++
			}
			skippedCount++
			continue
		}
		matchedCount++
		baseUpdates := map[string]interface{}{
			"last_seen_at": now,
		}
		if strings.TrimSpace(lm.Source) == "" {
			baseUpdates["source"] = model.ModelSourceOpenRouter
		}
		if len(baseUpdates) > 0 {
			_ = model.CoreDB().Model(&model.Model{}).Where("id = ?", lm.Id).Updates(baseUpdates).Error
		}

		// Update released_at
		if orModel.Created > 0 {
			mm, exists := metaMap[lm.ModelName]
			if !exists || mm.ReleasedAt == nil || req.Force {
				releasedAt := time.Unix(orModel.Created, 0)
				model.EnsureModelMetadataWithReleasedAt(lm.ModelName, releasedAt)
				updatedReleasedAt++
			}
		}

		// Update description (with OpenRouter reference cleanup)
		cleanedDesc := cleanOpenRouterReferences(orModel.Description)
		if cleanedDesc != "" && (lm.DescriptionEn == "" || req.Force) {
			if err := model.CoreDB().Model(&model.Model{}).
				Where("id = ?", lm.Id).
				Update("description_en", cleanedDesc).Error; err == nil {
				updatedDescEn++
				if req.Translate && (lm.DescriptionZh == "" || req.Force) {
					needTranslation[lm.ModelName] = cleanedDesc
				}
			}
		} else if req.Translate && lm.DescriptionEn != "" && (lm.DescriptionZh == "" || req.Force) {
			needTranslation[lm.ModelName] = cleanOpenRouterReferences(lm.DescriptionEn)
		}

		// Generate and merge tags from OpenRouter data
		newTags := generateTagsFromOpenRouter(orModel)
		merged := mergeTagStrings(lm.Tags, newTags)
		if merged != lm.Tags {
			model.CoreDB().
				Model(&model.Model{}).Where("id = ?", lm.Id).Update("tags", merged)
			updatedTags++
		}
		capabilities := capabilitiesFromOpenRouter(orModel)
		if capabilities.HasAny() && (req.Force || capabilities != lm.Capabilities) {
			model.CoreDB().
				Model(&model.Model{}).Where("id = ?", lm.Id).Update("capabilities", capabilities)
			updatedCapabilities++
		}

		// Update context_length
		if orModel.ContextLength > 0 && (lm.ContextLength == 0 || req.Force) {
			model.CoreDB().
				Model(&model.Model{}).Where("id = ?", lm.Id).Update("context_length", orModel.ContextLength)
		}

		// Send progress every 20 models
		if (i+1)%20 == 0 || i+1 == totalLocal {
			sendSSE("progress", progressMsg{
				Phase:   "sync",
				Current: i + 1,
				Total:   totalLocal,
				Message: fmt.Sprintf("Processed %d/%d models, matched %d, skipped %d", i+1, totalLocal, matchedCount, skippedCount),
			})
		}
	}

	// Phase 2: Translation (concurrent batches)
	updatedDescZh := 0
	translationErrors := 0
	if req.Translate && len(needTranslation) > 0 {
		type entry struct {
			name string
			desc string
		}
		var entries []entry
		for name, desc := range needTranslation {
			entries = append(entries, entry{name, desc})
		}

		batchSize := 5
		concurrency := 4
		totalBatches := (len(entries) + batchSize - 1) / batchSize
		relayURL := getRelayURL()

		sendSSE("progress", progressMsg{
			Phase:   "translate",
			Current: 0,
			Total:   len(entries),
			Message: fmt.Sprintf("Starting translation for %d model descriptions (%d batches, concurrency %d)...", len(entries), totalBatches, concurrency),
		})

		// Build all batches
		type batchResult struct {
			translated map[string]string
			batchSize  int
		}

		var batches [][]entry
		for i := 0; i < len(entries); i += batchSize {
			end := i + batchSize
			if end > len(entries) {
				end = len(entries)
			}
			batches = append(batches, entries[i:end])
		}

		// Process batches with concurrency limit
		sem := make(chan struct{}, concurrency)
		resultCh := make(chan batchResult, len(batches))

		for _, batch := range batches {
			sem <- struct{}{} // acquire slot
			batch := batch
			common.SafeCtxGo(ctx, "openrouter-description-translation", func() {
				defer func() { <-sem }() // release slot

				inputMap := make(map[string]string)
				for _, e := range batch {
					inputMap[e.name] = e.desc
				}
				inputJSON, _ := json.Marshal(inputMap)

				prompt := fmt.Sprintf(`Translate the following model descriptions from English to Chinese. Return a JSON object where keys are model names and values are the translated Chinese descriptions. Keep the translations concise and professional. Only return the JSON object, no other text.

Input:
%s`, string(inputJSON))

				translated := callRelayForTranslation(ctx, relayURL, tokenKey, adminUserID, translationModel, prompt)
				resultCh <- batchResult{translated: translated, batchSize: len(batch)}
			})
		}

		// Collect results
		processedCount := 0
		for range batches {
			br := <-resultCh
			processedCount += br.batchSize
			if br.translated != nil {
				for modelName, zhDesc := range br.translated {
					if zhDesc == "" {
						continue
					}
					if err := model.CoreDB().Model(&model.Model{}).
						Where("model_name = ?", modelName).
						Update("description_zh", zhDesc).Error; err == nil {
						updatedDescZh++
					}
				}
			} else {
				translationErrors += br.batchSize
			}

			sendSSE("progress", progressMsg{
				Phase:   "translate",
				Current: processedCount,
				Total:   len(entries),
				Message: fmt.Sprintf("Translation progress %d/%d, completed %d, failed %d", processedCount, len(entries), updatedDescZh, translationErrors),
			})
		}
	}

	model.RecomputeFollowingSellPricesLogged(nil, nil, "openrouter sync")

	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()

	sendSSE("done", gin.H{
		"success":              true,
		"openrouter_total":     len(orModels),
		"matched":              matchedCount,
		"updated_released_at":  updatedReleasedAt,
		"updated_desc_en":      updatedDescEn,
		"updated_desc_zh":      updatedDescZh,
		"updated_tags":         updatedTags,
		"updated_capabilities": updatedCapabilities,
		"skipped":              skippedCount,
		"translation_errors":   translationErrors,
	})
}

// callRelayForTranslation calls the local relay endpoint for translation.
func callRelayForTranslation(ctx context.Context, relayURL, tokenKey string, adminUserID int, modelName, prompt string) map[string]string {
	reqBody := map[string]interface{}{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0.3,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	requestCtx, cancel := context.WithTimeout(ctx, translationRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, relayURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		common.SysLog(fmt.Sprintf("translation request creation failed: %v", err))
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenKey)
	req.Header.Set("X-Lazu-User", strconv.Itoa(adminUserID))

	resp, err := translationHTTPClient.Do(req)
	if err != nil {
		common.SysLog(fmt.Sprintf("translation request failed: %v", err))
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		common.SysLog(fmt.Sprintf("translation response read failed: %v", err))
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		common.SysLog(fmt.Sprintf("translation API returned %d: %s", resp.StatusCode, string(respBody)))
		return nil
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		common.SysLog(fmt.Sprintf("translation response parse failed: %v", err))
		return nil
	}
	if len(chatResp.Choices) == 0 {
		common.SysLog("translation returned no choices")
		return nil
	}

	content := chatResp.Choices[0].Message.Content
	result := make(map[string]string)
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		common.SysLog(fmt.Sprintf("translation JSON parse failed: %v, content: %s", err, content))
		return nil
	}
	return result
}

// getRelayURL returns the URL for the local relay endpoint.
func getRelayURL() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}
	return "http://localhost:" + port
}
