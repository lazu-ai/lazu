package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// projectTokenState is the Key state the console renders. It folds the two
// states a stored row drifts into without being rewritten — calendar expiry and
// a spent allowance — into the stored status, and reports a Key stopped by a
// removal as disabled: that is what it is, and stopped_by_removal explains why.
func projectTokenState(token *model.Token, now time.Time) string {
	switch {
	case token == nil:
		return "disabled"
	case token.StoppedByRemoval(), token.Status == common.TokenStatusDisabled:
		return "disabled"
	case token.Status == common.TokenStatusExpired:
		return "expired"
	case token.Status == common.TokenStatusExhausted:
		return "exhausted"
	case token.ExpiredTime != -1 && token.ExpiredTime <= now.Unix():
		return "expired"
	case !token.UnlimitedQuota && token.RemainQuota <= 0:
		return "exhausted"
	default:
		return "enabled"
	}
}

// projectTokenModelLimits splits the stored comma-separated allowlist. An empty
// list is [], never null: the contract types it as an array of strings.
func projectTokenModelLimits(token *model.Token) []string {
	result := []string{}
	if token == nil {
		return result
	}
	for _, name := range strings.Split(token.ModelLimits, ",") {
		if name = strings.TrimSpace(name); name != "" {
			result = append(result, name)
		}
	}
	return result
}

// projectTokenFacts holds the per-Key figures that come from other tables. They
// are read once for a whole page, including applicable budgets and their spend.
type projectTokenFacts struct {
	Now          time.Time
	MonthSpend   map[int]billing.MicroUSD
	LastUsedAt   map[int]int64
	EffortDowned map[int]int64
	Members      map[int]model.ProjectMember
	Owners       map[int]model.User
	Payer        model.User
	Budgets      []model.SpendBudget
	BudgetUsed   map[uint]billing.MicroUSD
	PeriodCaps   map[int]*model.TokenPeriodCap
	Rejections   map[int][]projectTokenRejection
	// Populated only for read-only list/search pages. Detail/editor and
	// mutation payloads retain the committed absolute Key quota baseline.
	DisplayRemaining map[int]billing.MicroUSD
}

type projectTokenRejection struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

func readProjectTokenFacts(pc middleware.ProjectContext, tokens []*model.Token) (projectTokenFacts, error) {
	facts := projectTokenFacts{Now: time.Now().UTC(), MonthSpend: map[int]billing.MicroUSD{}, LastUsedAt: map[int]int64{}, EffortDowned: map[int]int64{},
		Members: map[int]model.ProjectMember{}, Owners: map[int]model.User{}, PeriodCaps: map[int]*model.TokenPeriodCap{}, Rejections: map[int][]projectTokenRejection{}}
	ids := make([]int, 0, len(tokens))
	ownerIDs := make([]int, 0, len(tokens))
	seenOwners := make(map[int]bool, len(tokens))
	for _, token := range tokens {
		if token != nil && token.Id > 0 {
			ids = append(ids, token.Id)
			if !seenOwners[token.UserId] {
				ownerIDs = append(ownerIDs, token.UserId)
				seenOwners[token.UserId] = true
			}
		}
	}
	if len(ids) == 0 {
		return facts, nil
	}
	spend, err := model.ReadProjectTokenMonthSpend(pc.Project.ID, ids, facts.Now)
	if err != nil {
		return facts, err
	}
	facts.MonthSpend = spend
	lastUsed, err := model.GetTokenLastUsedAt(ids)
	if err != nil {
		return facts, err
	}
	facts.LastUsedAt = lastUsed
	now := facts.Now
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	downed, err := model.CountTokenEffortClampedSince(ids, monthStart)
	if err != nil {
		return facts, err
	}
	facts.EffortDowned = downed
	var members []model.ProjectMember
	if err := model.CoreDB().Where("project_id = ? AND user_id IN ?", pc.Project.ID, ownerIDs).Find(&members).Error; err != nil {
		return facts, err
	}
	for _, member := range members {
		facts.Members[member.UserID] = member
	}
	var owners []model.User
	if err := model.CoreDB().Unscoped().Select("id", "public_id", "username").Where("id IN ?", ownerIDs).Find(&owners).Error; err != nil {
		return facts, err
	}
	for _, owner := range owners {
		facts.Owners[owner.Id] = owner
	}
	if err := model.CoreDB().Select("quota", "has_topped_up", "rpm_limit", "tpm_limit").First(&facts.Payer, pc.Project.OwnerUserID).Error; err != nil {
		return facts, err
	}
	facts.Payer.Quota = model.ReadConsoleWalletBalance(context.Background(), pc.Project.OwnerUserID, facts.Payer.Quota)
	// Disabled Key rows must remain visible here: period_cap has always used
	// the first definition, even when a later definition is enabled.
	query := model.CoreDB().Where("project_id = ?", pc.Project.ID).Where(
		model.CoreDB().Where("scope = ? AND enabled = ?", model.SpendBudgetScopeOrg, true).
			Or("scope = ? AND user_id IN ? AND enabled = ?", model.SpendBudgetScopeUser, ownerIDs, true).
			Or("scope = ? AND token_id IN ?", model.SpendBudgetScopeToken, ids))
	if err := query.Order("id ASC").Find(&facts.Budgets).Error; err != nil {
		return facts, err
	}
	enabled := make([]model.SpendBudget, 0, len(facts.Budgets))
	for _, budget := range facts.Budgets {
		if budget.Enabled {
			enabled = append(enabled, budget)
		}
	}
	facts.BudgetUsed, err = model.ReadSpendBudgetUsedBatch(enabled, now)
	if err != nil {
		return facts, err
	}
	for _, budget := range facts.Budgets {
		if budget.Scope != model.SpendBudgetScopeToken {
			continue
		}
		if _, seen := facts.PeriodCaps[budget.TokenID]; seen {
			continue
		}
		facts.PeriodCaps[budget.TokenID] = nil
		if budget.Enabled && budget.LimitMicrousd > 0 {
			period := model.SpendBudgetPeriodMonth
			if strings.ToLower(strings.TrimSpace(budget.Period)) == model.SpendBudgetPeriodDay {
				period = model.SpendBudgetPeriodDay
			}
			facts.PeriodCaps[budget.TokenID] = &model.TokenPeriodCap{Period: period,
				LimitUSD: float64(budget.LimitMicrousd) / float64(billing.MicroUSDPerUSD),
				SpentUSD: float64(facts.BudgetUsed[budget.ID]) / float64(billing.MicroUSDPerUSD)}
		}
	}
	if model.TelemetryDB() != nil {
		var rows []struct {
			TokenID int
			Reason  string
			Count   int64
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		if err := model.ProjectLogScope(model.TelemetryDB().Model(&model.Log{}), pc.Project).
			Where("type = ? AND token_id IN ? AND created_at >= ? AND created_at <= ?", model.LogTypeError, ids, start.Unix(), now.Unix()).
			Select("token_id, COALESCE(NULLIF(rejection_reason, ''), 'unknown') AS reason, COUNT(*) AS count").
			Group("token_id, COALESCE(NULLIF(rejection_reason, ''), 'unknown')").Order("token_id ASC, reason ASC").Scan(&rows).Error; err != nil {
			return facts, err
		}
		for _, row := range rows {
			facts.Rejections[row.TokenID] = append(facts.Rejections[row.TokenID], projectTokenRejection{Reason: row.Reason, Count: row.Count})
		}
	}
	return facts, nil
}

func projectTokenPayload(c *gin.Context, pc middleware.ProjectContext, token *model.Token) (gin.H, error) {
	facts, err := readProjectTokenFacts(pc, []*model.Token{token})
	if err != nil {
		return nil, err
	}
	return projectTokenPayloadWithFacts(c, pc, token, facts)
}

func projectTokenPayloadWithFacts(c *gin.Context, pc middleware.ProjectContext, token *model.Token, facts projectTokenFacts) (gin.H, error) {
	result := publicTokenPayloadWithPeriodCap(c, token, facts.PeriodCaps[token.Id])
	settings, err := pc.Project.GetSettings()
	if err != nil {
		return nil, err
	}
	ownerUserID := token.UserId
	member, ok := facts.Members[ownerUserID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	user, ok := facts.Owners[ownerUserID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	owner := &struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}{user.PublicID, user.Username, member.Role}
	manage := model.CanProjectAction(pc.Member, &member, model.ProjectManagePersonalKey, settings)
	remove := model.CanProjectAction(pc.Member, &member, model.ProjectDeletePersonalKey, settings)
	reveal := manage
	if pc.Project.UpgradedAt == nil && pc.Member.UserID != pc.Project.OwnerUserID {
		manage, reveal, remove = false, false, false
	}
	// A stopped Key offers only deletion: while its owner is outside the project
	// an undo may still be pending, and a Key the removal fenced never comes
	// back even after a re-invite. The model layer refuses the rest with
	// state_conflict — do not offer buttons that will fail.
	if member.Status != model.ProjectMemberActive || token.StoppedByRemoval() {
		manage, reveal = false, false
	}
	now := facts.Now
	result["project_id"] = pc.Project.PublicID
	result["owner"] = owner
	result["version"] = token.ProjectVersion
	// This payload backs /api/token/ list, search, detail, create and update, so
	// the contract string replaces an established numeric field on all of them.
	// publicTokenPayload already emitted status_code (the stored number) beside
	// it, so a reader that wants the number keeps working on every path.
	result["status"] = projectTokenState(token, now)
	result["model_limits"] = projectTokenModelLimits(token)
	result["month_spend_microusd"] = facts.MonthSpend[token.Id]
	result["reasoning_effort_cap"] = token.ReasoningEffortCap
	result["member_effort_cap"] = member.ReasoningEffortCap
	result["effort_downed_month"] = facts.EffortDowned[token.Id]
	result["created_at"] = token.CreatedTime
	if lastUsed, ok := facts.LastUsedAt[token.Id]; ok && lastUsed > 0 {
		result["last_used_at"] = lastUsed
	} else {
		result["last_used_at"] = nil
	}
	result["secret_available"] = token.CanRevealKey()
	result["stopped_by_removal"] = token.StoppedByRemoval()
	result["can_reveal_key"] = reveal && token.CanRevealKey()
	result["capabilities"] = gin.H{"can_reveal": reveal && token.CanRevealKey(), "can_update": manage, "can_disable": manage, "can_delete": remove, "can_rotate": reveal}
	payer := facts.Payer
	ceilingRPM, ceilingTPM := setting.AccountRateLimit(payer.HasToppedUp, payer.RPMLimit, payer.TPMLimit)
	result["effective_rate_limits"] = tokenRateLimitPayloadForAccount(token, ceilingRPM, ceilingTPM)
	result["cap_microusd"] = nil
	remaining := max(billing.MicroUSD(0), payer.Quota)
	kind := "balance"
	rank := map[string]int{"key": 0, "member": 1, "project": 2, "balance": 3}
	consider := func(candidate billing.MicroUSD, candidateKind string) {
		candidate = max(billing.MicroUSD(0), candidate)
		if candidate < remaining || candidate == remaining && rank[candidateKind] < rank[kind] {
			remaining, kind = candidate, candidateKind
		}
	}
	if !token.UnlimitedQuota {
		remaining := token.RemainQuota
		if display, ok := facts.DisplayRemaining[token.Id]; ok {
			remaining = display
			result["remain_quota"] = display
		}
		consider(remaining, "key")
	}
	for _, budget := range facts.Budgets {
		if !budget.Enabled || budget.ProjectID != pc.Project.ID || budget.Scope == model.SpendBudgetScopeUser && budget.UserID != ownerUserID || budget.Scope == model.SpendBudgetScopeToken && budget.TokenID != token.Id {
			continue
		}
		used := facts.BudgetUsed[budget.ID]
		candidateKind := "project"
		if budget.Scope == model.SpendBudgetScopeUser {
			candidateKind = "member"
		} else if budget.Scope == model.SpendBudgetScopeToken {
			candidateKind = "key"
			if budget.Period == model.SpendBudgetPeriodMonth {
				result["cap_microusd"] = budget.LimitMicrousd
			}
		}
		consider(budget.LimitMicrousd-used, candidateKind)
	}
	result["effective_limit"] = gin.H{"kind": kind, "remaining_microusd": remaining}
	rejections := facts.Rejections[token.Id]
	if rejections == nil {
		rejections = []projectTokenRejection{}
	}
	result["rejected_today"] = rejections
	return result, nil
}
func projectTokenPayloads(c *gin.Context, pc middleware.ProjectContext, tokens []*model.Token) ([]gin.H, error) {
	result := make([]gin.H, 0, len(tokens))
	facts, err := readProjectTokenFacts(pc, tokens)
	if err != nil {
		return nil, err
	}
	facts.DisplayRemaining = tokenDisplayRemaining(c, tokens)
	for _, token := range tokens {
		row, err := projectTokenPayloadWithFacts(c, pc, token, facts)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, nil
}
func ListProjectTokens(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	query := model.ProjectVisibleTokensQuery(model.CoreDB(), pc.Project, pc.Member, true)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		projectFailure(c, err)
		return
	}
	var tokens []*model.Token
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&tokens).Error; err != nil {
		projectFailure(c, err)
		return
	}
	items, err := projectTokenPayloads(c, pc, tokens)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}})
}

func RotateProjectToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	tokenID, err := resolveIDParam("tokens", c.Param("id"))
	if err != nil {
		projectFailure(c, err)
		return
	}
	var input struct {
		OldExpires      string `json:"old_expires"`
		ProjectPublicID string `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		httperr.Invalid(c, "old_expires")
		return
	}
	pc, ok := legacyProjectContext(c, input.ProjectPublicID)
	if !ok {
		return
	}
	result, err := model.RotateProjectToken(pc.Project.ID, pc.Member.UserID, tokenID, c.GetHeader("Idempotency-Key"), input.OldExpires, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	wakeProjectNotifications()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
