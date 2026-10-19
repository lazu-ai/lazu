package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gorm.io/gorm"
)

// keyNameMaxLength is the longest Key name the console accepts.
const keyNameMaxLength = 30

// writeDomainFailure answers a Key, usage or log route. A missing row becomes
// notFound when one is given; catalog errors (the project model's sentinels
// included) are written as they are; anything else is logged and answered as
// internal_error.
func writeDomainFailure(c *gin.Context, err error, notFound types.ErrorCode) {
	if notFound != "" && errors.Is(err, gorm.ErrRecordNotFound) {
		httperr.Code(c, notFound)
		return
	}
	httperr.Write(c, err)
}

// keyFailure answers a Key route; a missing row is the Key.
func keyFailure(c *gin.Context, err error) {
	writeDomainFailure(c, err, types.ErrorCodeKeyNotFound)
}

// credentialFailure answers a failure to resolve a pasted Key. A Key that
// does not resolve is an invalid credential, not a missing record.
func credentialFailure(c *gin.Context, err error) {
	writeDomainFailure(c, err, types.ErrorCodeInvalidAPIKey)
}

func keyNameTooLong(c *gin.Context) {
	httperr.Code(c, types.ErrorCodeKeyNameTooLong, types.D{"max_length": keyNameMaxLength})
}

type TokenOption struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	MaskedKey string `json:"masked_key"`
}

type TokenInspectRequest struct {
	APIKey         string `json:"api_key" binding:"required"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
	Page           int    `json:"page"`
	PageSize       int    `json:"page_size"`
	ModelLimit     int    `json:"model_limit"`
	LogModelName   string `json:"log_model_name"`
	LogKeyword     string `json:"log_keyword"`
}

func maskTokenKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "************"
	}
	if len(raw) <= 4 {
		return "************"
	}
	if strings.Contains(raw, "...") || strings.HasPrefix(raw, "************") {
		return raw
	}
	return "************" + raw[len(raw)-4:]
}

func publicVendorIDs(ids model.IntSlice) []string {
	if len(ids) == 0 {
		return []string{}
	}
	rows := make([]model.Vendor, 0, len(ids))
	if err := model.CoreDB().Select("id, public_id").Where("id IN ?", []int(ids)).Find(&rows).Error; err != nil {
		return []string{}
	}
	publicByID := make(map[int]string, len(rows))
	for _, row := range rows {
		if row.PublicID != "" {
			publicByID[row.Id] = row.PublicID
		}
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if publicID := publicByID[id]; publicID != "" {
			result = append(result, publicID)
		}
	}
	return result
}

// accountRateLimitFromContext is the account ceiling a Key's own limits sit
// under (see middleware.accountRateLimitFromContext).
func accountRateLimitFromContext(c *gin.Context) (rpm, tpm int) {
	if c == nil {
		return setting.AccountRateLimit(true, 0, 0)
	}
	hasToppedUp := true
	if _, ok := common.GetContextKey(c, constant.ContextKeyUserHasToppedUp); ok {
		hasToppedUp = common.GetContextKeyBool(c, constant.ContextKeyUserHasToppedUp)
	}
	return setting.AccountRateLimit(hasToppedUp,
		common.GetContextKeyInt(c, constant.ContextKeyUserRPMLimit),
		common.GetContextKeyInt(c, constant.ContextKeyUserTPMLimit))
}

func effectiveRateLimitValue(configured int, ceiling int, enabled bool) int {
	if !enabled || configured <= 0 {
		return ceiling
	}
	if ceiling > 0 && configured > ceiling {
		return ceiling
	}
	return configured
}

func publicTokenRateLimitPayload(c *gin.Context, token *model.Token) gin.H {
	rpm, tpm := accountRateLimitFromContext(c)
	return tokenRateLimitPayloadForAccount(token, rpm, tpm)
}

func tokenRateLimitPayloadForAccount(token *model.Token, ceilingRPM, ceilingTPM int) gin.H {
	enabled := token != nil && token.RateLimitsEnabled
	rpm := 0
	tpm := 0
	if token != nil {
		rpm = token.RPMLimit
		tpm = token.TPMLimit
	}
	return gin.H{
		"ceiling":      gin.H{"rpm": ceilingRPM, "tpm": ceilingTPM},
		"configured":   gin.H{"rpm": rpm, "tpm": tpm},
		"effective":    gin.H{"rpm": effectiveRateLimitValue(rpm, ceilingRPM, enabled), "tpm": effectiveRateLimitValue(tpm, ceilingTPM, enabled)},
		"tpm_counting": "prompt_plus_max_output_times_completion_count",
	}
}

func publicTokenPayload(c *gin.Context, token *model.Token) gin.H {
	if token == nil {
		return nil
	}
	return publicTokenPayloadWithPeriodCap(c, token, model.ReadTokenPeriodCap(token.ProjectID, token.Id))
}

func publicTokenPayloadWithPeriodCap(c *gin.Context, token *model.Token, periodCap *model.TokenPeriodCap) gin.H {
	if token == nil {
		return nil
	}
	return gin.H{
		"id":             token.PublicID,
		"masked_key":     maskTokenKey(token.DisplayKey()),
		"can_reveal_key": token.CanRevealKey(),
		"status":         token.Status,
		// status_code is the stored numeric status on every projection that
		// carries a status. The project payload replaces "status" with the
		// contract string, so a reader that wants the number reads this field
		// and never has to guess which shape an endpoint happens to emit.
		"status_code":                  token.Status,
		"name":                         token.Name,
		"product":                      token.Product,
		"metadata":                     token.Metadata,
		"created_time":                 token.CreatedTime,
		"last_used_at":                 token.LastUsedAt,
		"expired_time":                 token.ExpiredTime,
		"remain_quota":                 token.RemainQuota,
		"unlimited_quota":              token.UnlimitedQuota,
		"rate_limits_enabled":          token.RateLimitsEnabled,
		"rpm_limit":                    token.RPMLimit,
		"tpm_limit":                    token.TPMLimit,
		"effective_rate_limits":        publicTokenRateLimitPayload(c, token),
		"model_limits_enabled":         token.ModelLimitsEnabled,
		"model_limits":                 token.ModelLimits,
		"allow_ips":                    token.AllowIps,
		"ip_whitelist_enabled":         token.IPWhitelistEnabled,
		"ip_blacklist":                 token.IPBlacklist,
		"ip_blacklist_enabled":         token.IPBlacklistEnabled,
		"used_quota":                   token.UsedQuota,
		"vendor_limits_enabled":        token.VendorLimitsEnabled,
		"vendor_limits":                publicVendorIDs(token.VendorLimits),
		"vendor_groups":                token.VendorGroups.Normalize(),
		"model_lanes":                  token.ModelLanes.Normalize(),
		"decision_question_generation": token.DecisionQuestionGeneration,
		"reasoning_effort_cap":         token.ReasoningEffortCap,
		"search_limits_enabled":        token.SearchLimitsEnabled,
		"search_providers":             token.SearchProviders,
		"search_domain_allowlist":      token.SearchDomainAllowlist,
		"model_scope_vendors":          publicVendorIDs(token.ModelScopeVendors),
		"usage_alert_percent":          token.UsageAlertPercent,
		"note":                         token.Note,
		"period_cap":                   periodCap,
	}
}

/** A percentage outside 0-100 is a typo, not an instruction; 0 turns the
 *  alert off. */
func clampUsageAlertPercent(value int) int {
	if value < 0 || value > 100 {
		return 0
	}
	return value
}

func trimTokenNote(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 256 {
		return trimmed[:256]
	}
	return trimmed
}

func publicTokenPayloads(c *gin.Context, tokens []*model.Token) []gin.H {
	result := make([]gin.H, 0, len(tokens))
	remaining := tokenDisplayRemaining(c, tokens)
	for _, token := range tokens {
		payload := publicTokenPayload(c, token)
		if token != nil && !token.UnlimitedQuota {
			payload["remain_quota"] = remaining[token.Id]
		}
		result = append(result, payload)
	}
	return result
}

// Only read-only list and usage paths call this helper. Base payloads remain
// committed so saving an editor cannot overwrite quota with a held Hot value.
func tokenDisplayRemaining(c *gin.Context, tokens []*model.Token) map[int]billing.MicroUSD {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return model.ReadConsoleTokenRemaining(ctx, tokens)
}

func tokenStatusFilter(c *gin.Context) int {
	status, err := strconv.Atoi(strings.TrimSpace(c.Query("status")))
	if err != nil || status < common.TokenStatusEnabled || status > common.TokenStatusExhausted {
		return 0
	}
	return status
}

func GetAllTokens(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive {
		keyFailure(c, model.ErrProjectForbidden)
		return
	}
	pageInfo := common.GetPageQuery(c)
	query := model.ProjectVisibleTokensQuery(model.CoreDB(), pc.Project, pc.Member, false)
	if status := tokenStatusFilter(c); status != 0 {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		keyFailure(c, err)
		return
	}
	var tokens []*model.Token
	if err := query.Order("id DESC").Offset(pageInfo.GetStartIdx()).Limit(pageInfo.GetPageSize()).Find(&tokens).Error; err != nil {
		keyFailure(c, err)
		return
	}
	payload, err := projectTokenPayloads(c, pc, tokens)
	if err != nil {
		keyFailure(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(payload)
	common.ApiSuccess(c, pageInfo)
}

func GetTokenOptions(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive {
		keyFailure(c, model.ErrProjectForbidden)
		return
	}
	limit := 200
	if parsed, err := strconv.Atoi(c.Query("size")); err == nil {
		if parsed > 0 && parsed < 1000 {
			limit = parsed
		}
	}
	var tokens []*model.Token
	err := model.ProjectVisibleTokensQuery(model.CoreDB(), pc.Project, pc.Member, false).Order("id DESC").Limit(limit).Find(&tokens).Error
	if err != nil {
		keyFailure(c, err)
		return
	}
	options := make([]TokenOption, 0, len(tokens))
	for _, token := range tokens {
		if token == nil || token.Status != common.TokenStatusEnabled {
			continue
		}
		options = append(options, TokenOption{
			Id:        token.PublicID,
			Name:      token.Name,
			MaskedKey: maskTokenKey(token.DisplayKey()),
		})
	}
	common.ApiSuccess(c, options)
}

func SearchTokens(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive {
		keyFailure(c, model.ErrProjectForbidden)
		return
	}
	query := model.ProjectVisibleTokensQuery(model.CoreDB(), pc.Project, pc.Member, false)
	query = model.FilterTokenSearch(query, c.Query("keyword"), c.Query("token"))
	if status := tokenStatusFilter(c); status != 0 {
		query = query.Where("status = ?", status)
	}
	var tokens []*model.Token
	if err := query.Order("id DESC").Find(&tokens).Error; err != nil {
		keyFailure(c, err)
		return
	}
	payload, err := projectTokenPayloads(c, pc, tokens)
	if err != nil {
		keyFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": payload})
}

func GetToken(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	id, err := resolveIDParam("tokens", c.Param("id"))
	if err != nil {
		keyFailure(c, err)
		return
	}
	token, err := model.CheckProjectTokenAction(model.CoreDB(), pc.Project.ID, pc.Member.UserID, id, model.ProjectReadPersonalResource)
	if err != nil {
		keyFailure(c, err)
		return
	}
	payload, err := projectTokenPayload(c, pc, &token)
	if err != nil {
		keyFailure(c, err)
		return
	}
	common.ApiSuccess(c, payload)
}

// RevealTokenKey is intentionally separate from list and detail projections.
// UserAuth plus the owner predicate prohibit support/admin cross-user reads.
func RevealTokenKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	id, err := resolveIDParam("tokens", c.Param("id"))
	if err != nil {
		keyFailure(c, err)
		return
	}
	key, notification, err := model.RevealProjectToken(pc.Project.ID, pc.Member.UserID, id)
	if err != nil {
		keyFailure(c, err)
		return
	}
	if notification == "queued" {
		wakeProjectNotifications()
	}
	common.ApiSuccess(c, gin.H{"key": key, "notification_status": notification})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	token, err := model.GetTokenById(tokenId)
	principal := model.RequestResourcePrincipal(c)
	if err == nil && (principal.ProjectID <= 0 || token.ProjectID != principal.ProjectID || token.UserId != principal.ActorUserID) {
		err = model.ErrProjectForbidden
	}
	if err != nil {
		keyFailure(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	parts := strings.Split(authHeader, " ")
	if authHeader == "" || len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		httperr.Code(c, types.ErrorCodeInvalidAPIKey)
		return
	}
	tokenKey := parts[1]

	token, err := model.GetTokenByKey(common.NormalizeAPIKey(tokenKey), false)
	if err != nil {
		credentialFailure(c, err)
		return
	}

	token, err = model.ResolveTokenCredential(token.Id, tokenKey, time.Now().UTC())
	if err != nil {
		credentialFailure(c, err)
		return
	}
	if _, err := model.ResolveProjectPrincipal(model.CoreDB(), token.Id, time.Now().UTC()); err != nil {
		credentialFailure(c, err)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":                  "token_usage",
			"name":                    token.Name,
			"total_granted":           token.RemainQuota + token.UsedQuota,
			"total_used":              token.UsedQuota,
			"total_available":         tokenDisplayRemaining(c, []*model.Token{token})[token.Id],
			"unlimited_quota":         token.UnlimitedQuota,
			"product":                 token.Product,
			"metadata":                token.Metadata,
			"rate_limits_enabled":     token.RateLimitsEnabled,
			"rpm_limit":               token.RPMLimit,
			"tpm_limit":               token.TPMLimit,
			"effective_rate_limits":   publicTokenRateLimitPayload(c, token),
			"model_limits":            token.GetModelLimitsMap(),
			"model_limits_enabled":    token.ModelLimitsEnabled,
			"search_limits_enabled":   token.SearchLimitsEnabled,
			"search_providers":        token.SearchProviders,
			"search_domain_allowlist": token.SearchDomainAllowlist,
			"expires_at":              expiredAt,
		},
	})
}

func normalizeTokenInputKey(raw string) string {
	return common.NormalizeAPIKey(raw)
}

func inspectTokenUsage(c *gin.Context, requireOwner bool) {
	var req TokenInspectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "api_key")
		return
	}
	userID := 0
	if requireOwner {
		userID = c.GetInt("id")
		if userID <= 0 {
			httperr.Code(c, types.ErrorCodeSessionRequired)
			return
		}
	}

	tokenKey := normalizeTokenInputKey(req.APIKey)
	if tokenKey == "" {
		httperr.Invalid(c, "api_key")
		return
	}

	token, err := model.GetTokenByKey(tokenKey, false)
	if err != nil {
		credentialFailure(c, err)
		return
	}

	token, err = model.ResolveTokenCredential(token.Id, tokenKey, time.Now().UTC())
	if err != nil {
		credentialFailure(c, err)
		return
	}
	if requireOwner {
		pc, ok := legacyProjectContext(c, "")
		if !ok {
			return
		}
		if _, err := model.CheckProjectTokenAction(model.CoreDB(), pc.Project.ID, userID, token.Id, model.ProjectReadPersonalResource); err != nil {
			keyFailure(c, err)
			return
		}
	} else {
		if _, err := model.ResolveProjectPrincipal(model.CoreDB(), token.Id, time.Now().UTC()); err != nil {
			credentialFailure(c, err)
			return
		}
	}

	now := common.GetTimestamp()
	startTimestamp := req.StartTimestamp
	endTimestamp := req.EndTimestamp
	if endTimestamp <= 0 {
		endTimestamp = now
	}
	if startTimestamp <= 0 {
		startTimestamp = endTimestamp - 30*24*3600
	}
	if startTimestamp < 0 {
		startTimestamp = 0
	}
	if endTimestamp < startTimestamp {
		httperr.Code(c, types.ErrorCodeUsageRangeInvalid)
		return
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = common.ItemsPerPage
	}
	if pageSize > 100 {
		pageSize = 100
	}
	modelLimit := req.ModelLimit
	if modelLimit <= 0 {
		modelLimit = 8
	}
	if modelLimit > 50 {
		modelLimit = 50
	}

	summary, err := model.GetTokenConsumeSummaryByTokenID(token.Id, startTimestamp, endTimestamp)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	modelUsage, err := model.GetTokenModelUsageByTokenID(token.Id, startTimestamp, endTimestamp, modelLimit)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	startIdx := (page - 1) * pageSize
	logModelName := strings.TrimSpace(req.LogModelName)
	logKeyword := strings.TrimSpace(req.LogKeyword)
	if len(logKeyword) > 80 {
		logKeyword = logKeyword[:80]
	}

	recentLogs, totalLogs, err := model.GetTokenConsumeLogsByTokenIDWithFilters(
		token.Id,
		startTimestamp,
		endTimestamp,
		startIdx,
		pageSize,
		model.TokenConsumeLogFilter{
			ModelName: logModelName,
			Keyword:   logKeyword,
		},
	)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	common.ApiSuccess(c, gin.H{
		"token": gin.H{
			"id":                    token.PublicID,
			"name":                  token.Name,
			"masked_key":            maskTokenKey(token.DisplayKey()),
			"can_reveal_key":        token.CanRevealKey(),
			"status":                token.Status,
			"status_code":           token.Status,
			"product":               token.Product,
			"metadata":              token.Metadata,
			"expires_at":            expiredAt,
			"unlimited_quota":       token.UnlimitedQuota,
			"total_granted":         token.RemainQuota + token.UsedQuota,
			"total_used":            token.UsedQuota,
			"total_available":       tokenDisplayRemaining(c, []*model.Token{token})[token.Id],
			"model_limits_enabled":  token.ModelLimitsEnabled,
			"model_limits":          token.GetModelLimitsMap(),
			"rate_limits_enabled":   token.RateLimitsEnabled,
			"rpm_limit":             token.RPMLimit,
			"tpm_limit":             token.TPMLimit,
			"effective_rate_limits": publicTokenRateLimitPayload(c, token),
			"group":                 token.Group,
		},
		"window": gin.H{
			"start_timestamp": startTimestamp,
			"end_timestamp":   endTimestamp,
			"duration_days":   int((endTimestamp - startTimestamp) / int64(24*time.Hour/time.Second)),
		},
		"summary": gin.H{
			"request_count":     summary.RequestCount,
			"amount_microusd":   summary.AmountMicrousd,
			"prompt_tokens":     summary.PromptTokens,
			"completion_tokens": summary.CompletionTokens,
			"total_tokens":      summary.TotalTokens(),
			"avg_use_time":      summary.AvgUseTime,
		},
		"by_model": modelUsage,
		"logs": gin.H{
			"page":      page,
			"page_size": pageSize,
			"total":     totalLogs,
			"items":     recentLogs,
			"filters": gin.H{
				"log_model_name": logModelName,
				"log_keyword":    logKeyword,
			},
		},
	})
}

func InspectTokenUsage(c *gin.Context) {
	inspectTokenUsage(c, true)
}

func PublicInspectTokenUsage(c *gin.Context) {
	inspectTokenUsage(c, false)
}

func AddToken(c *gin.Context) {
	input := struct {
		model.Token
		ProjectPublicID    string                `json:"project_id"`
		ProjectModelLimits json.RawMessage       `json:"model_limits"`
		CapMicrousd        json.RawMessage       `json:"cap_microusd"`
		PeriodCap          *model.TokenPeriodCap `json:"period_cap"`
		Expires            *int64                `json:"expired_time"`
		Unlimited          *bool                 `json:"unlimited_quota"`
	}{}
	// Bound through the body cache so the connect path below can ask which
	// fields the client actually sent, rather than reading an absent field as
	// an empty one.
	err := c.ShouldBindBodyWith(&input, binding.JSON)
	token := input.Token
	if err != nil {
		httperr.Invalid(c, "body")
		return
	}
	pc, ok := legacyProjectContext(c, input.ProjectPublicID)
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive {
		keyFailure(c, model.ErrProjectForbidden)
		return
	}

	if input.Expires != nil {
		token.ExpiredTime = *input.Expires
	} else if input.ProjectPublicID != "" || c.Query("project_id") != "" {
		token.ExpiredTime = -1
	}
	if input.Unlimited != nil {
		token.UnlimitedQuota = *input.Unlimited
	} else if input.ProjectPublicID != "" || c.Query("project_id") != "" {
		token.UnlimitedQuota = true
	}
	if len(input.ProjectModelLimits) > 0 {
		var names []string
		if err := json.Unmarshal(input.ProjectModelLimits, &names); err == nil {
			token.ModelLimits = strings.Join(names, ",")
			token.ModelLimitsEnabled = len(names) > 0 || (token.ModelLimitsEnabled && len(token.ModelScopeVendors) > 0)
		} else if err := json.Unmarshal(input.ProjectModelLimits, &token.ModelLimits); err != nil {
			httperr.Invalid(c, "model_limits")
			return
		}
	}
	var caps []model.ProjectTokenCapUpdate
	if len(input.CapMicrousd) > 0 {
		var cap *billing.MicroUSD
		if err := json.Unmarshal(input.CapMicrousd, &cap); err != nil || cap != nil && (*cap < 0 || int64(*cap) > 9_007_199_254_740_991) {
			httperr.Invalid(c, "cap_microusd")
			return
		}
		caps = append(caps, model.ProjectTokenCapUpdate{Cap: cap})
	}
	if len(token.Name) > keyNameMaxLength {
		keyNameTooLong(c)
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		httperr.Write(c, fmt.Errorf("generate token key: %w", err))
		return
	}
	cleanToken := model.Token{
		UserId:                     c.GetInt("id"),
		Name:                       token.Name,
		Product:                    token.Product,
		Metadata:                   token.Metadata,
		Key:                        key,
		CreatedTime:                common.GetTimestamp(),
		ExpiredTime:                token.ExpiredTime,
		RemainQuota:                token.RemainQuota,
		UnlimitedQuota:             token.UnlimitedQuota,
		RateLimitsEnabled:          token.RateLimitsEnabled,
		RPMLimit:                   token.RPMLimit,
		TPMLimit:                   token.TPMLimit,
		ModelLimitsEnabled:         token.ModelLimitsEnabled,
		ModelLimits:                token.ModelLimits,
		AllowIps:                   token.AllowIps,
		IPWhitelistEnabled:         token.IPWhitelistEnabled,
		IPBlacklist:                token.IPBlacklist,
		IPBlacklistEnabled:         token.IPBlacklistEnabled,
		VendorLimitsEnabled:        token.VendorLimitsEnabled,
		VendorLimits:               token.VendorLimits,
		VendorGroups:               token.VendorGroups.Normalize(),
		ModelLanes:                 token.ModelLanes.Normalize(),
		DecisionQuestionGeneration: token.DecisionQuestionGeneration,
		SearchLimitsEnabled:        token.SearchLimitsEnabled,
		SearchProviders:            token.SearchProviders,
		SearchDomainAllowlist:      token.SearchDomainAllowlist,
		ModelScopeVendors:          token.ModelScopeVendors,
		UsageAlertPercent:          clampUsageAlertPercent(token.UsageAlertPercent),
		Note:                       trimTokenNote(token.Note),
		Group:                      model.ChannelLaneDirect,
		ReasoningEffortCap:         model.NormalizeEffortCap(token.ReasoningEffortCap),
	}
	if err := cleanToken.DecisionQuestionGeneration.Validate(); err != nil {
		httperr.Invalid(c, "decision_question_generation")
		return
	}
	if !model.ValidEffortCap(cleanToken.ReasoningEffortCap) {
		httperr.Invalid(c, "reasoning_effort_cap")
		return
	}
	if input.ProjectPublicID == "" && c.Query("project_id") == "" && pc.Project.UpgradedAt == nil && !model.ProjectsEnabledForUser(pc.Member.UserID) {
		err = model.CreateLegacyDefaultProjectPersonalToken(pc.Project.ID, pc.Member.UserID, &cleanToken, caps...)
	} else {
		err = model.CreateProjectPersonalToken(pc.Project.ID, pc.Member.UserID, &cleanToken, caps...)
	}
	if err != nil {
		keyFailure(c, err)
		return
	}
	// The cap lives in its own table and needs the Key's id, so it is written
	// after creation. A failure here is not worth losing the Key over: the
	// editor shows the cap as unset and one save fixes it.
	if input.PeriodCap != nil {
		if capErr := model.SaveTokenPeriodCap(pc.Project.ID, cleanToken.Id, pc.Member.UserID, input.PeriodCap); capErr != nil {
			common.SysLog("failed to save token period cap: " + capErr.Error())
		}
	}
	payload, err := projectTokenPayload(c, pc, &cleanToken)
	if err != nil {
		keyFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	model.WriteTokenAuditLog(c, model.TokenAuditActionCreate, nil, &cleanToken)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":         cleanToken.PublicID,
			"key":        key,
			"masked_key": maskTokenKey(key),
			"token":      payload,
		},
	})
	return
}

func DeleteToken(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	id, err := resolveIDParam("tokens", c.Param("id"))
	if err != nil {
		keyFailure(c, err)
		return
	}
	token, err := model.DeleteProjectToken(pc.Project.ID, pc.Member.UserID, id)
	if err != nil {
		if model.IsTokenMutationCommittedError(err) {
			model.WriteTokenAuditLog(c, model.TokenAuditActionDelete, &token, nil)
		}
		keyFailure(c, err)
		return
	}
	model.WriteTokenAuditLog(c, model.TokenAuditActionDelete, &token, nil)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func UpdateToken(c *gin.Context) {
	statusOnly := c.Query("status_only")
	connectOnly := statusOnly == "" && c.Query("connect_only") == "true"
	var connectProduct, connectVendorGroups, connectModelLanes bool
	input := struct {
		ExpectedVersion *int64 `json:"expected_version"`
		model.Token
		ProjectPublicID    string                `json:"project_id"`
		ProjectModelLimits json.RawMessage       `json:"model_limits"`
		CapMicrousd        json.RawMessage       `json:"cap_microusd"`
		PeriodCap          *model.TokenPeriodCap `json:"period_cap"`
	}{}
	// Bound through the body cache so the connect path below can ask which
	// fields the client actually sent, rather than reading an absent field as
	// an empty one.
	err := c.ShouldBindBodyWith(&input, binding.JSON)
	token := input.Token
	if err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if len(input.ProjectModelLimits) > 0 {
		var names []string
		if err := json.Unmarshal(input.ProjectModelLimits, &names); err == nil {
			token.ModelLimits = strings.Join(names, ",")
			token.ModelLimitsEnabled = len(names) > 0 || (token.ModelLimitsEnabled && len(token.ModelScopeVendors) > 0)
		} else if err := json.Unmarshal(input.ProjectModelLimits, &token.ModelLimits); err != nil {
			httperr.Invalid(c, "model_limits")
			return
		}
	}
	pc, ok := legacyProjectContext(c, input.ProjectPublicID)
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive {
		keyFailure(c, model.ErrProjectForbidden)
		return
	}

	if len(token.Name) > keyNameMaxLength {
		keyNameTooLong(c)
		return
	}
	if token.Id == 0 && token.PublicID != "" {
		token.Id, err = resolveIDParam("tokens", token.PublicID)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httperr.Code(c, types.ErrorCodeKeyNotFound)
		return
	}
	if err != nil || token.Id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	loadedToken, err := model.CheckProjectTokenAction(model.CoreDB(), pc.Project.ID, pc.Member.UserID, token.Id, model.ProjectManagePersonalKey)
	if err != nil {
		keyFailure(c, err)
		return
	}
	cleanToken := &loadedToken
	before := *cleanToken
	if input.ExpectedVersion != nil {
		cleanToken.ProjectVersion = *input.ExpectedVersion
	}
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			httperr.Code(c, types.ErrorCodeKeyEnableBlocked, types.D{"reason": "expired"})
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			httperr.Code(c, types.ErrorCodeKeyEnableBlocked, types.D{"reason": "exhausted"})
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else if connectOnly {
		// The connect dialog knows two things about a Key — which tool it is
		// for and which lane it takes — and reads nothing else. Sending the
		// whole record back from there would write a quota and an expiry the
		// dialog loaded before the user's last edit.
		//
		// It sends only what it changed, so a field that is absent keeps the
		// value the Key already has: picking a tool must not erase a lane.
		var sent map[string]json.RawMessage
		if err := c.ShouldBindBodyWith(&sent, binding.JSON); err != nil {
			httperr.Invalid(c, "body")
			return
		}
		if _, ok := sent["product"]; ok {
			connectProduct = true
			cleanToken.Product = token.Product
		}
		if _, ok := sent["vendor_groups"]; ok {
			connectVendorGroups = true
			cleanToken.VendorGroups = token.VendorGroups.Normalize()
		}
		if _, ok := sent["model_lanes"]; ok {
			connectModelLanes = true
			cleanToken.ModelLanes = token.ModelLanes.Normalize()
		}
	} else if c.Query("policy_only") == "true" {
		switch {
		case strings.TrimSpace(token.Name) == "":
			httperr.Invalid(c, "name")
			return
		case len(input.ProjectModelLimits) == 0:
			httperr.Invalid(c, "model_limits")
			return
		case len(input.CapMicrousd) == 0:
			httperr.Invalid(c, "cap_microusd")
			return
		}
		cleanToken.Name = strings.TrimSpace(token.Name)
		cleanToken.ModelLimits = token.ModelLimits
		var sentPolicy map[string]json.RawMessage
		if err := c.ShouldBindBodyWith(&sentPolicy, binding.JSON); err != nil {
			httperr.Invalid(c, "body")
			return
		}
		if _, present := sentPolicy["decision_question_generation"]; present {
			cleanToken.DecisionQuestionGeneration = token.DecisionQuestionGeneration
		}
		cleanToken.ReasoningEffortCap = model.NormalizeEffortCap(token.ReasoningEffortCap)
		// The project policy form cannot edit vendor scopes; preserve them.
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled || (cleanToken.ModelLimitsEnabled && len(cleanToken.ModelScopeVendors) > 0)
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.Product = token.Product
		cleanToken.Metadata = token.Metadata
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.RateLimitsEnabled = token.RateLimitsEnabled
		cleanToken.RPMLimit = token.RPMLimit
		cleanToken.TPMLimit = token.TPMLimit
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.IPWhitelistEnabled = token.IPWhitelistEnabled
		cleanToken.IPBlacklist = token.IPBlacklist
		cleanToken.IPBlacklistEnabled = token.IPBlacklistEnabled
		cleanToken.VendorLimitsEnabled = token.VendorLimitsEnabled
		cleanToken.VendorLimits = token.VendorLimits
		cleanToken.VendorGroups = token.VendorGroups.Normalize()
		cleanToken.ModelLanes = token.ModelLanes.Normalize()
		cleanToken.DecisionQuestionGeneration = token.DecisionQuestionGeneration
		cleanToken.SearchLimitsEnabled = token.SearchLimitsEnabled
		cleanToken.SearchProviders = token.SearchProviders
		cleanToken.SearchDomainAllowlist = token.SearchDomainAllowlist
		cleanToken.ModelScopeVendors = token.ModelScopeVendors
		cleanToken.UsageAlertPercent = clampUsageAlertPercent(token.UsageAlertPercent)
		cleanToken.Note = trimTokenNote(token.Note)
		cleanToken.Group = model.ChannelLaneDirect
		cleanToken.ReasoningEffortCap = model.NormalizeEffortCap(token.ReasoningEffortCap)
	}
	if err := cleanToken.DecisionQuestionGeneration.Validate(); err != nil {
		httperr.Invalid(c, "decision_question_generation")
		return
	}
	if !model.ValidEffortCap(cleanToken.ReasoningEffortCap) {
		httperr.Invalid(c, "reasoning_effort_cap")
		return
	}
	var capUpdates []model.ProjectTokenCapUpdate
	if !connectOnly && len(input.CapMicrousd) > 0 {
		var cap *billing.MicroUSD
		if err := json.Unmarshal(input.CapMicrousd, &cap); err != nil {
			httperr.Invalid(c, "cap_microusd")
			return
		}
		capUpdates = append(capUpdates, model.ProjectTokenCapUpdate{Cap: cap})
	}
	if connectOnly {
		err = model.UpdateProjectTokenConnection(pc.Project.ID, pc.Member.UserID, cleanToken, connectProduct, connectVendorGroups, connectModelLanes)
	} else {
		err = model.UpdateProjectToken(pc.Project.ID, pc.Member.UserID, cleanToken, capUpdates...)
		// Only the full editor touches the periodic cap. A status toggle or a
		// connect-dialog save has no opinion about it and must not clear it.
		// An explicit team monthly cap was already saved by UpdateProjectToken.
		if err == nil && statusOnly == "" && c.Query("policy_only") != "true" && len(input.CapMicrousd) == 0 {
			if capErr := model.SaveTokenPeriodCap(pc.Project.ID, cleanToken.Id, pc.Member.UserID, input.PeriodCap); capErr != nil {
				common.SysLog("failed to save token period cap: " + capErr.Error())
			}
		}
	}
	if err != nil {
		if model.IsTokenMutationCommittedError(err) {
			model.WriteTokenAuditLog(c, model.TokenAuditActionUpdate, &before, cleanToken)
		}
		keyFailure(c, err)
		return
	}
	model.WriteTokenAuditLog(c, model.TokenAuditActionUpdate, &before, cleanToken)
	payload, err := projectTokenPayload(c, pc, cleanToken)
	if err != nil {
		keyFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    payload,
	})
	return
}

type TokenBatch struct {
	Ids []string `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	var input struct {
		Ids       []string `json:"ids"`
		ProjectID string   `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Ids) == 0 || len(input.Ids) > 100 {
		httperr.Invalid(c, "ids")
		return
	}
	pc, ok := legacyProjectContext(c, input.ProjectID)
	if !ok {
		return
	}
	ids, err := model.ResolvePublicIntIDs("tokens", input.Ids)
	if err != nil {
		keyFailure(c, err)
		return
	}
	tokens, err := model.DeleteProjectTokens(pc.Project.ID, pc.Member.UserID, ids)
	if err == nil || model.IsTokenMutationCommittedError(err) {
		for i := range tokens {
			model.WriteTokenAuditLog(c, model.TokenAuditActionDelete, &tokens[i], nil)
		}
	}
	if err != nil {
		keyFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": len(tokens)})
}
