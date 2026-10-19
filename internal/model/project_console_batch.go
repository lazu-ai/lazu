package model

import (
	"context"
	"errors"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// These readers aggregate each store independently. Page hydration never joins
// Core identities or money to Telemetry rows and never performs a query per row.
func readProjectLogIdentities(project Project, logs []*Log) (map[int]User, map[int]Token, error) {
	userIDs := map[int]struct{}{project.OwnerUserID: {}}
	tokenIDs := make(map[int]struct{})
	for _, row := range logs {
		if row.UserId > 0 {
			userIDs[row.UserId] = struct{}{}
		}
		if row.TokenId > 0 {
			tokenIDs[row.TokenId] = struct{}{}
		}
	}
	var users []User
	if err := CoreDB().Unscoped().Select("id", "public_id").Where("id IN ?", consoleIDKeys(userIDs)).Find(&users).Error; err != nil {
		return nil, nil, err
	}
	byUser := make(map[int]User, len(users))
	for _, user := range users {
		byUser[user.Id] = user
	}
	if _, found := byUser[project.OwnerUserID]; !found {
		return nil, nil, gorm.ErrRecordNotFound
	}
	byToken := make(map[int]Token, len(tokenIDs))
	if len(tokenIDs) > 0 {
		var tokens []Token
		if err := CoreDB().Unscoped().Select("id", "public_id").Where("id IN ?", consoleIDKeys(tokenIDs)).Find(&tokens).Error; err != nil {
			return nil, nil, err
		}
		for _, token := range tokens {
			byToken[token.Id] = token
		}
	}
	return byUser, byToken, nil
}

func consoleIDKeys(ids map[int]struct{}) []int {
	result := make([]int, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}

func readProjectSummaries(memberships []ProjectMember) ([]ProjectSummary, string, error) {
	result := make([]ProjectSummary, 0, len(memberships))
	if len(memberships) == 0 {
		return result, "", nil
	}
	projectIDs := make(map[int]struct{}, len(memberships))
	activeIDs := make(map[int]struct{}, len(memberships))
	for _, member := range memberships {
		projectIDs[member.ProjectID] = struct{}{}
		if member.Status == ProjectMemberActive {
			activeIDs[member.ProjectID] = struct{}{}
		}
	}
	var projects []Project
	if err := CoreDB().Where("id IN ?", consoleIDKeys(projectIDs)).Find(&projects).Error; err != nil {
		return nil, "", err
	}
	byProject := make(map[int]Project, len(projects))
	ownerIDs := make(map[int]struct{}, len(projects))
	for _, project := range projects {
		byProject[project.ID] = project
		ownerIDs[project.OwnerUserID] = struct{}{}
	}
	if len(byProject) != len(projectIDs) {
		return nil, "", gorm.ErrRecordNotFound
	}
	var owners []User
	if err := CoreDB().Select("id", "public_id", "username", "quota").Where("id IN ?", consoleIDKeys(ownerIDs)).Find(&owners).Error; err != nil {
		return nil, "", err
	}
	byOwner := make(map[int]User, len(owners))
	for _, owner := range owners {
		byOwner[owner.Id] = owner
	}
	if len(byOwner) != len(ownerIDs) {
		return nil, "", gorm.ErrRecordNotFound
	}
	committed := make(map[int]billing.MicroUSD, len(activeIDs))
	for _, member := range memberships {
		if member.Status == ProjectMemberActive {
			owner := byOwner[byProject[member.ProjectID].OwnerUserID]
			committed[owner.Id] = owner.Quota
		}
	}
	balances := ReadConsoleWalletBalances(context.Background(), committed)
	counts := make(map[int]int64, len(activeIDs))
	if len(activeIDs) > 0 {
		var rows []struct {
			ProjectID int
			Count     int64
		}
		if err := CoreDB().Model(&ProjectMember{}).Select("project_id, COUNT(*) AS count").
			Where("project_id IN ? AND status <> ?", consoleIDKeys(activeIDs), ProjectMemberRemoved).
			Group("project_id").Scan(&rows).Error; err != nil {
			return nil, "", err
		}
		for _, row := range rows {
			counts[row.ProjectID] = row.Count
		}
	}
	defaultID := ""
	for _, member := range memberships {
		project := byProject[member.ProjectID]
		owner := byOwner[project.OwnerUserID]
		row := ProjectSummary{ID: project.PublicID, Name: project.Name, Role: member.Role, Status: member.Status,
			IsDefault: project.OwnerUserID == member.UserID, UpgradedAt: project.UpgradedAt,
			Owner: ProjectUserRef{ID: owner.PublicID, Username: owner.Username}}
		if member.Status == ProjectMemberActive {
			balance := balances[owner.Id]
			row.BalanceMicrousd, row.MemberCount = &balance, counts[project.ID]
		}
		if row.IsDefault {
			defaultID = project.PublicID
		}
		result = append(result, row)
	}
	return result, defaultID, nil
}

// ReadProjectMemberViews preserves the single-member view's permissions and
// time windows while loading a whole page with five queries, independent of
// its row count. Only finalized Core ProjectSpend rows supply money.
func ReadProjectMemberViews(project Project, members []ProjectMember, now time.Time) ([]ProjectMemberView, error) {
	result := make([]ProjectMemberView, 0, len(members))
	if len(members) == 0 {
		return result, nil
	}
	if TelemetryDB() == nil {
		return nil, errors.New("log database is not initialized")
	}
	memberIDs := make(map[int]struct{}, len(members))
	userIDs := make(map[int]struct{}, len(members))
	for _, member := range members {
		memberIDs[member.UserID], userIDs[member.UserID] = struct{}{}, struct{}{}
		if member.InvitedBy > 0 {
			userIDs[member.InvitedBy] = struct{}{}
		}
	}
	var users []User
	if err := CoreDB().Unscoped().Select("id", "public_id", "username", "email", "deleted_at").
		Where("id IN ?", consoleIDKeys(userIDs)).Find(&users).Error; err != nil {
		return nil, err
	}
	byUser := make(map[int]User, len(users))
	for _, user := range users {
		byUser[user.Id] = user
	}
	var budgets []SpendBudget
	if err := CoreDB().Where("project_id = ? AND scope = ? AND user_id IN ? AND period = ?", project.ID,
		SpendBudgetScopeUser, consoleIDKeys(memberIDs), SpendBudgetPeriodMonth).Order("id ASC").Find(&budgets).Error; err != nil {
		return nil, err
	}
	byBudget := make(map[int]SpendBudget, len(budgets))
	for _, budget := range budgets {
		if _, found := byBudget[budget.UserID]; !found {
			byBudget[budget.UserID] = budget
		}
	}
	var tokenCounts []struct {
		UserID int
		Count  int64
	}
	if err := excludeInternalTokens(CoreDB().Model(&Token{})).
		Where("project_id = ? AND user_id IN ?", project.ID, consoleIDKeys(memberIDs)).
		Select("user_id, COUNT(*) AS count").Group("user_id").Scan(&tokenCounts).Error; err != nil {
		return nil, err
	}
	byKeyCount := make(map[int]int64, len(tokenCounts))
	for _, row := range tokenCounts {
		byKeyCount[row.UserID] = row.Count
	}
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var amounts []struct {
		ActorUserID    int
		AmountMicrousd billing.MicroUSD
	}
	if err := CoreDB().Model(&ProjectSpend{}).Where("project_id = ? AND actor_user_id IN ? AND occurred_at >= ? AND occurred_at < ?",
		project.ID, consoleIDKeys(memberIDs), monthStart, time.Unix(now.Unix()+1, 0).UTC()).
		Select("actor_user_id, COALESCE(SUM(amount_microusd),0) AS amount_microusd").Group("actor_user_id").Scan(&amounts).Error; err != nil {
		return nil, err
	}
	byAmount := make(map[int]billing.MicroUSD, len(amounts))
	for _, row := range amounts {
		byAmount[row.ActorUserID] = row.AmountMicrousd
	}
	// Each count reads only its own period. Over all history this read every
	// log of a heavy member and decoded Other: 1.17 s on 2026-10-15.
	lastActive, err := readProjectMemberLastActive(project, consoleIDKeys(memberIDs))
	if err != nil {
		return nil, err
	}
	type memberCount struct {
		UserID int
		Count  int64
	}
	countByMember := func(rows []memberCount) map[int]int64 {
		counts := make(map[int]int64, len(rows))
		for _, row := range rows {
			counts[row.UserID] = row.Count
		}
		return counts
	}
	var rejected, downed []memberCount
	memberLogs := func() *gorm.DB {
		return ProjectLogScope(TelemetryDB().Model(&Log{}), project).Where("user_id IN ?", consoleIDKeys(memberIDs))
	}
	if err := memberLogs().Where("request_ts >= ? AND type = ? AND created_at >= ? AND created_at <= ?", logRequestTSFloor(dayStart.Unix()), LogTypeError, dayStart.Unix(), now.Unix()).
		Select("user_id, COUNT(*) AS count").Group("user_id").Scan(&rejected).Error; err != nil {
		return nil, err
	}
	if err := memberLogs().Where(effortClampedCondition(TelemetryDB())+" AND request_ts >= ? AND type = ? AND created_at >= ?", logRequestTSFloor(monthStart.Unix()), LogTypeConsume, monthStart.Unix()).
		Select("user_id, COUNT(*) AS count").Group("user_id").Scan(&downed).Error; err != nil {
		return nil, err
	}
	rejectedByMember, downedByMember := countByMember(rejected), countByMember(downed)
	byActivity := make(map[int]struct {
		LastActiveAt, RejectedCountToday, EffortDownedMonth int64
	}, len(memberIDs))
	for userID := range memberIDs {
		byActivity[userID] = struct{ LastActiveAt, RejectedCountToday, EffortDownedMonth int64 }{lastActive[userID], rejectedByMember[userID], downedByMember[userID]}
	}
	for _, member := range members {
		user, found := byUser[member.UserID]
		if !found || user.DeletedAt.Valid {
			return nil, gorm.ErrRecordNotFound
		}
		row := ProjectMemberView{UserID: user.PublicID, Username: user.Username, Email: user.Email,
			Role: member.Role, Status: member.Status, JoinedAt: member.JoinedAt, Version: member.Version,
			AllowedModels: []string(member.AllowedModels), ReasoningEffortCap: member.ReasoningEffortCap,
			WarningPct: 80, KeyCount: byKeyCount[member.UserID], MonthSpendMicrousd: byAmount[member.UserID]}
		if row.AllowedModels == nil {
			row.AllowedModels = []string{}
		}
		if member.InvitedBy > 0 {
			inviter, found := byUser[member.InvitedBy]
			if !found {
				return nil, gorm.ErrRecordNotFound
			}
			row.InvitedBy = &ProjectUserRef{ID: inviter.PublicID, Username: inviter.Username}
		}
		if budget, found := byBudget[member.UserID]; found {
			row.WarningPct = budget.WarningThresholdPct
			if budget.Enabled {
				row.BudgetMicrousd = &budget.LimitMicrousd
			}
		}
		stats := byActivity[member.UserID]
		row.RejectedCountToday, row.EffortDownedMonth = stats.RejectedCountToday, stats.EffortDownedMonth
		if stats.LastActiveAt > 0 {
			row.LastActiveAt = &stats.LastActiveAt
		}
		result = append(result, row)
	}
	return result, nil
}
