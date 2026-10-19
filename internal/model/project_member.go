package model

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func UpdateProjectMemberRoleOrStatus(projectID, viewerID, targetID int, role, status string) (ProjectMember, error) {
	var result ProjectMember
	if role != "" && role != ProjectRoleAdmin && role != ProjectRoleMember {
		return result, ErrProjectForbidden
	}
	if status != "" && status != ProjectMemberActive && status != ProjectMemberSuspended {
		return result, ErrProjectForbidden
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		viewer, err := lockProjectMember(tx, projectID, viewerID)
		if err != nil {
			return err
		}
		target, err := lockProjectMember(tx, projectID, targetID)
		if err != nil {
			return err
		}
		settings, err := project.GetSettings()
		if err != nil {
			return err
		}
		action := ProjectManageMember
		if role != "" {
			action = ProjectChangeRole
		}
		if !CanProjectAction(*viewer, target, action, settings) {
			return ErrProjectForbidden
		}
		if target.Status == ProjectMemberRemoved {
			return ErrProjectStateConflict
		}
		changed := role != "" && role != target.Role || status != "" && status != target.Status
		if changed {
			if role != "" {
				target.Role = role
			}
			if status != "" {
				target.Status = status
			}
			target.Version++
			if err := tx.Save(target).Error; err != nil {
				return err
			}
		}

		if changed {
			if err := queueProjectMemberNoticeTx(tx, project, *target, projectNotice{Kind: noticeMembership, ActorID: viewerID}, fmt.Sprintf("member:%d:%d", target.ID, target.Version)); err != nil {
				return err
			}
		}
		result = *target
		return nil
	})
	return result, err
}

func readMemberBudgetTx(tx *gorm.DB, projectID, userID int) (*billing.MicroUSD, int, error) {
	var budget SpendBudget
	err := tx.Where("project_id = ? AND scope = ? AND user_id = ? AND period = ?", projectID, SpendBudgetScopeUser, userID, SpendBudgetPeriodMonth).
		Order("id ASC").First(&budget).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 80, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !budget.Enabled {
		return nil, budget.WarningThresholdPct, nil
	}
	amount := budget.LimitMicrousd
	return &amount, budget.WarningThresholdPct, nil
}

// ProjectMemberPolicy carries the member limits the edit dialog saves beside
// the budget. A nil field leaves that limit unchanged.
type ProjectMemberPolicy struct {
	AllowedModels *[]string
	EffortCap     *string
}

func UpdateProjectMemberBudget(projectID, viewerID, targetID int, expectedVersion int64, amount *billing.MicroUSD, warningPct int, reason string, policies ...ProjectMemberPolicy) (ProjectMember, error) {
	var result ProjectMember
	var changedKeyDigests []string
	validation := DefaultProjectSettings()
	validation.DefaultMemberBudgetMicrousd, validation.WarningPct = amount, warningPct
	if err := validation.Validate(); err != nil {
		return result, err
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		viewer, err := lockProjectMember(tx, projectID, viewerID)
		if err != nil {
			return err
		}
		target, err := lockProjectMember(tx, projectID, targetID)
		if err != nil {
			return err
		}
		settings, err := project.GetSettings()
		if err != nil {
			return err
		}
		if !CanProjectAction(*viewer, target, ProjectManageMember, settings) {
			return ErrProjectForbidden
		}
		if target.Status == ProjectMemberRemoved {
			return ErrProjectStateConflict
		}
		if target.Version != expectedVersion {
			return ErrProjectVersionConflict
		}
		previous, previousWarning, err := readMemberBudgetTx(tx, projectID, targetID)
		if err != nil {
			return err
		}
		budgetChanged := (previous == nil) != (amount == nil) || previous != nil && amount != nil && *previous != *amount || previousWarning != warningPct
		policyChanged := false
		for _, policy := range policies {
			if policy.AllowedModels != nil {
				names := cleanModelNames(*policy.AllowedModels)
				policyChanged = policyChanged || !slices.Equal(target.AllowedModels, names)
				target.AllowedModels = names
			}
			if policy.EffortCap != nil {
				if !ValidEffortCap(*policy.EffortCap) {
					return types.Err(types.ErrorCodeInvalidEffortCap)
				}
				cap := NormalizeEffortCap(*policy.EffortCap)
				policyChanged = policyChanged || target.ReasoningEffortCap != cap
				target.ReasoningEffortCap = cap
				if cap != "" {
					var keys []Token
					if err := tx.Where("project_id = ? AND user_id = ?", projectID, targetID).Find(&keys).Error; err != nil {
						return err
					}
					for _, key := range keys {
						if key.ReasoningEffortCap == "" || EffortRank(key.ReasoningEffortCap) <= EffortRank(cap) {
							continue
						}
						if err := tx.Model(&key).Updates(map[string]interface{}{
							"reasoning_effort_cap": cap,
							"project_version":      gorm.Expr("project_version + 1"),
						}).Error; err != nil {
							return err
						}
						changedKeyDigests = append(changedKeyDigests, key.CacheDigest())
					}
				}
			}
		}
		if budgetChanged {
			if err := updateProjectMemberBudgetTx(tx, target, viewerID, amount, warningPct, reason); err != nil {
				return err
			}
			if err := queueProjectMemberNoticeTx(tx, project, *target, projectNotice{Kind: noticeBudget, ActorID: viewerID, Budget: amount}, fmt.Sprintf("budget:%d:%d", target.ID, target.Version)); err != nil {
				return err
			}
		} else if policyChanged {
			target.Version++
			if err := tx.Save(target).Error; err != nil {
				return err
			}
		}
		result = *target
		return nil
	})
	if err == nil && len(changedKeyDigests) > 0 {
		if cacheErr := cacheInvalidateTokenSnapshotsWithFence(changedKeyDigests); cacheErr != nil {
			return result, &TokenMutationCommittedError{Operation: "member effort update", Cause: cacheErr}
		}
	}

	return result, err
}

func updateProjectMemberBudgetTx(tx *gorm.DB, target *ProjectMember, changedBy int, amount *billing.MicroUSD, warningPct int, reason string) error {
	previous, _, err := readMemberBudgetTx(tx, target.ProjectID, target.UserID)
	if err != nil {
		return err
	}
	if err := setProjectBudgetTx(tx, target.ProjectID, target.UserID, 0, amount, warningPct); err != nil {
		return err
	}
	history := ProjectBudgetHistory{ProjectID: target.ProjectID, UserID: target.UserID, ChangedBy: changedBy, OldBudgetMicrousd: previous, NewBudgetMicrousd: amount, Reason: reason, CreatedAt: time.Now().UTC().Unix()}
	if err := tx.Create(&history).Error; err != nil {
		return err
	}
	target.Version++
	return tx.Save(target).Error
}

func DissolveProject(projectID, viewerID int, confirmName string) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		member, err := lockProjectMember(tx, projectID, viewerID)
		if err != nil {
			return err
		}
		if member.Status != ProjectMemberActive || member.Role != ProjectRoleOwner {
			return ErrProjectForbidden
		}
		if confirmName != project.Name {
			return types.Err(types.ErrorCodeConfirmMismatch)
		}
		if project.UpgradedAt == nil {
			return nil
		}
		now := time.Now().UTC().Unix()
		var affected []ProjectMember
		if err := tx.Where("project_id = ? AND user_id <> ? AND status <> ?", projectID, project.OwnerUserID, ProjectMemberRemoved).Find(&affected).Error; err != nil {
			return err
		}
		for _, target := range affected {
			if err := queueProjectMemberNoticeTx(tx, project, target, projectNotice{Kind: noticeDissolved, ActorID: viewerID}, fmt.Sprintf("dissolve:%d:%d:%d", project.ID, project.Version, target.UserID)); err != nil {
				return err
			}
		}
		// Dissolution ends every outstanding undo window. Leaving the handle in
		// place would let an old removal receipt be replayed after the project is
		// shared again and re-admit that member.
		if err := tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id <> ?", projectID, project.OwnerUserID).
			Updates(map[string]interface{}{"status": ProjectMemberRemoved, "removed_at": now, "removal_operation_id": "", "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		if err := tx.Model(&ProjectInvite{}).Where("project_id = ? AND status = ?", projectID, "pending").Update("status", "revoked").Error; err != nil {
			return err
		}
		if err := tx.Model(&BudgetRequest{}).Where("project_id = ? AND status = ?", projectID, "pending").Updates(map[string]interface{}{"status": "rejected", "handled_by": viewerID, "handled_note": "Project dissolved", "handled_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&ProjectNotification{}).Where("project_id = ? AND event_key LIKE ? AND status IN ?", projectID, "invite:%", []string{"pending", "sending"}).Updates(map[string]interface{}{"status": "cancelled", "payload": "", "claimed_at": nil}).Error; err != nil {
			return err
		}
		// A permanent fence remains even if the project is shared again. A
		// retired row with user_id 0 belongs to the owner, so zero must not be
		// read as "somebody else" here.
		if err := tx.Model(&Token{}).Where("project_id = ? AND user_id > 0 AND user_id <> ?", projectID, project.OwnerUserID).
			Updates(map[string]interface{}{"status": common.TokenStatusDisabled, "removal_operation_id": "dissolved", "project_version": gorm.Expr("project_version + 1")}).Error; err != nil {
			return err
		}
		return tx.Model(&project).Updates(map[string]interface{}{"upgraded_at": nil, "version": gorm.Expr("version + 1")}).Error
	})
}

type ProjectMemberView struct {
	UserID             string            `json:"user_id"`
	Username           string            `json:"username"`
	Email              string            `json:"email"`
	Role               string            `json:"role"`
	Status             string            `json:"status"`
	JoinedAt           int64             `json:"joined_at"`
	InvitedBy          *ProjectUserRef   `json:"invited_by"`
	BudgetMicrousd     *billing.MicroUSD `json:"budget_microusd"`
	WarningPct         int               `json:"warning_pct"`
	Version            int64             `json:"version"`
	MonthSpendMicrousd billing.MicroUSD  `json:"month_spend_microusd"`
	RejectedCountToday int64             `json:"rejected_count_today"`
	KeyCount           int64             `json:"key_count"`
	LastActiveAt       *int64            `json:"last_active_at"`
	AllowedModels      []string          `json:"allowed_models"`
	ReasoningEffortCap string            `json:"reasoning_effort_cap"`
	EffortDownedMonth  int64             `json:"effort_downed_month"`
}

func ReadProjectMemberView(project Project, member ProjectMember, now time.Time) (ProjectMemberView, error) {
	result := ProjectMemberView{Role: member.Role, Status: member.Status, JoinedAt: member.JoinedAt, Version: member.Version,
		AllowedModels: []string(member.AllowedModels), ReasoningEffortCap: member.ReasoningEffortCap}
	if result.AllowedModels == nil {
		result.AllowedModels = []string{}
	}
	var user User
	if err := CoreDB().Select("public_id", "username", "email").First(&user, member.UserID).Error; err != nil {
		return result, err
	}
	result.UserID, result.Username, result.Email = user.PublicID, user.Username, user.Email
	if member.InvitedBy > 0 {
		var inviter User
		if err := CoreDB().Unscoped().Select("public_id", "username").First(&inviter, member.InvitedBy).Error; err != nil {
			return result, err
		}
		result.InvitedBy = &ProjectUserRef{ID: inviter.PublicID, Username: inviter.Username}
	}
	amount, warning, err := readMemberBudgetTx(CoreDB(), project.ID, member.UserID)
	if err != nil {
		return result, err
	}
	result.BudgetMicrousd, result.WarningPct = amount, warning
	memberTokens := func() *gorm.DB {
		return ProjectMemberTokenScope(excludeInternalTokens(CoreDB().Model(&Token{})).Where("project_id = ?", project.ID), member.UserID)
	}
	if err := memberTokens().Count(&result.KeyCount).Error; err != nil {
		return result, err
	}
	if TelemetryDB() == nil {
		return result, errors.New("log database is not initialized")
	}
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	logs := func() *gorm.DB {
		return ProjectLogScope(TelemetryDB().Model(&Log{}), project).Where("user_id = ?", member.UserID)
	}
	lastActive, err := readProjectMemberLastActive(project, []int{member.UserID})
	if err != nil {
		return result, err
	}
	if last := lastActive[member.UserID]; last > 0 {
		result.LastActiveAt = &last
	}
	result.MonthSpendMicrousd, err = ReadProjectLedgerSpend(project.ID, member.UserID, monthStart, now.Unix()+1)
	if err != nil {
		return result, err
	}

	err = logs().Where("request_ts >= ? AND type = ? AND created_at >= ? AND created_at <= ?", logRequestTSFloor(dayStart), LogTypeError, dayStart, now.Unix()).Count(&result.RejectedCountToday).Error
	if err != nil {
		return result, err
	}
	err = logs().Where(effortClampedCondition(TelemetryDB())+" AND request_ts >= ? AND type = ? AND created_at >= ?", logRequestTSFloor(monthStart), LogTypeConsume, monthStart).Count(&result.EffortDownedMonth).Error
	return result, err
}

func cleanModelNames(names []string) StringSlice {
	seen := map[string]bool{}
	out := StringSlice{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// logRequestTSFloor bounds a created_at condition by the request_ts partition
// key, so a period read prunes to its partitions and index range. A log is
// created after its request was admitted, within a day of it.
func logRequestTSFloor(createdAt int64) int64 {
	return (createdAt - 86400) * 1000
}

// readProjectMemberLastActive returns when each member's newest successful
// request in the project was logged. On PostgreSQL each member walks the
// (user_id, request_ts) index from the newest row and stops at the first that
// matches; a MAX over the member's logs read every row a heavy member wrote.
func readProjectMemberLastActive(project Project, userIDs []int) (map[int]int64, error) {
	result := make(map[int]int64, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		UserID int
		Last   int64
	}
	db := TelemetryDB()
	scope := "(logs.project_id = ? OR ((logs.project_id = 0 OR logs.project_id IS NULL) AND logs.user_id = ?))"
	var err error
	if db.Dialector.Name() == "postgres" {
		err = db.Raw(`SELECT member.user_id, COALESCE(newest.created_at, 0) AS last
			FROM unnest(?::int[]) AS member(user_id)
			LEFT JOIN LATERAL (SELECT logs.created_at FROM logs
				WHERE logs.user_id = member.user_id AND logs.type = ? AND `+scope+`
				ORDER BY logs.request_ts DESC LIMIT 1) newest ON TRUE`,
			postgresIntArray(userIDs), LogTypeConsume, project.ID, project.OwnerUserID).Scan(&rows).Error
	} else {
		err = db.Model(&Log{}).Where("logs.user_id IN ? AND logs.type = ? AND "+scope, userIDs, LogTypeConsume, project.ID, project.OwnerUserID).
			Select("logs.user_id AS user_id, COALESCE(MAX(logs.created_at),0) AS last").Group("logs.user_id").Scan(&rows).Error
	}
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.UserID] = row.Last
	}
	return result, nil
}

// postgresIntArray renders ids as a PostgreSQL array literal for one bound
// parameter; GORM would expand a slice into a row constructor.
func postgresIntArray(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
