package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrProjectInvalidInput = types.Err(types.ErrorCodeInvalidRequest)

var ErrProjectVersionConflict = types.Err(types.ErrorCodeVersionConflict)

type ProjectUserRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}
type ProjectSummary struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Role            string            `json:"role"`
	Status          string            `json:"status"`
	IsDefault       bool              `json:"is_default"`
	UpgradedAt      *int64            `json:"upgraded_at"`
	Owner           ProjectUserRef    `json:"owner"`
	MemberCount     int64             `json:"member_count"`
	BalanceMicrousd *billing.MicroUSD `json:"balance_microusd"`
}

// Both switches are stored options an admin edits on the settings page, so
// turning the feature on takes a click rather than a redeploy. An environment
// variable, when set, still wins: local development and env-configured
// deployments keep working unchanged.
func projectsEnabledGlobally() bool {
	return common.GetEnvOrDefaultBool("PROJECTS_ENABLED", common.ProjectsEnabled)
}

func projectsAllowlist() []string {
	return strings.Split(common.GetEnvOrDefaultString("PROJECTS_ALLOWED_USERS", common.ProjectsAllowedUsers), ",")
}

// The feature flag gates entry points, never accounting or Key authorization.
func ProjectsEnabledForUser(userID int) bool {
	if userID <= 0 {
		return false
	}
	if projectsEnabledGlobally() {
		return true
	}
	allowed := projectsAllowlist()
	var user User
	if len(allowed) == 1 && strings.TrimSpace(allowed[0]) == "" {
		return false
	}
	if CoreDB().Select("public_id").First(&user, userID).Error != nil {
		return false
	}
	return projectsEnabledForPublicID(user.PublicID)
}

func projectsEnabledForPublicID(publicID string) bool {
	if projectsEnabledGlobally() {
		return true
	}
	if publicID == "" {
		return false
	}
	for _, value := range projectsAllowlist() {
		if strings.TrimSpace(value) == publicID {
			return true
		}
	}
	return false
}

func LoadVisibleProject(publicID string, userID int) (Project, ProjectMember, error) {
	var project Project
	var member ProjectMember
	err := CoreDB().Joins("JOIN project_members pm ON pm.project_id = projects.id").
		Where("projects.public_id = ? AND pm.user_id = ? AND pm.status IN ?", publicID, userID, []string{ProjectMemberActive, ProjectMemberSuspended}).
		First(&project).Error
	if err != nil {
		return project, member, err
	}
	err = CoreDB().Where("project_id = ? AND user_id = ?", project.ID, userID).First(&member).Error
	return project, member, err
}

func ListUserProjects(userID int) ([]ProjectSummary, string, error) {
	var memberships []ProjectMember
	if err := CoreDB().Where("user_id = ? AND status IN ?", userID, []string{ProjectMemberActive, ProjectMemberSuspended}).Order("project_id ASC").Find(&memberships).Error; err != nil {
		return nil, "", err
	}
	return readProjectSummaries(memberships)
}

func ReadProjectSummary(project Project, member ProjectMember) (ProjectSummary, error) {
	result := ProjectSummary{ID: project.PublicID, Name: project.Name, Role: member.Role, Status: member.Status, IsDefault: project.OwnerUserID == member.UserID, UpgradedAt: project.UpgradedAt}
	var owner User
	if err := CoreDB().Select("id", "public_id", "username", "quota").First(&owner, project.OwnerUserID).Error; err != nil {
		return result, err
	}
	result.Owner = ProjectUserRef{ID: owner.PublicID, Username: owner.Username}
	if member.Status != ProjectMemberActive {
		return result, nil
	}
	balance := ReadConsoleWalletBalance(context.Background(), owner.Id, owner.Quota)
	result.BalanceMicrousd = &balance
	err := CoreDB().Model(&ProjectMember{}).Where("project_id = ? AND status <> ?", project.ID, ProjectMemberRemoved).Count(&result.MemberCount).Error
	return result, err
}

// ProjectLogScope is a project's logs. Account logs are written with the
// owner's project (accountLog) and schema 37 gave older project-less rows
// theirs, so no fallback to the owner's rows remains.
func ProjectLogScope(tx *gorm.DB, project Project) *gorm.DB {
	return tx.Where("logs.project_id = ?", project.ID)
}

func ReadProjectSpend(project Project, start, end int64) (billing.MicroUSD, error) {
	return ReadProjectLedgerSpend(project.ID, 0, start, end)
}

func UpdateProjectSettings(projectID, viewerID int, expectedVersion int64, name *string, settings *ProjectSettings) (Project, error) {
	var result Project
	if name != nil && (strings.TrimSpace(*name) == "" || utf8.RuneCountInString(strings.TrimSpace(*name)) > 191) {
		return result, ErrProjectInvalidInput
	}
	if settings != nil {
		if err := settings.Validate(); err != nil {
			return result, err
		}
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, projectID).Error; err != nil {
			return err
		}
		member, err := lockProjectMember(tx, projectID, viewerID)
		if err != nil {
			return err
		}
		if member.Status != ProjectMemberActive || member.Role != ProjectRoleOwner {
			return ErrProjectForbidden
		}
		if result.Version != expectedVersion {
			return ErrProjectVersionConflict
		}
		if name != nil {
			result.Name = strings.TrimSpace(*name)
		}
		if settings != nil {
			raw, err := json.Marshal(settings)
			if err != nil {
				return err
			}
			result.Settings = raw
			if err := setProjectBudgetTx(tx, result.ID, 0, 0, settings.ProjectCapMicrousd, settings.WarningPct); err != nil {
				return err
			}
		}
		result.Version++
		return tx.Save(&result).Error
	})
	return result, err
}

// Callers serialize management mutations using the project row. Existing
// budget IDs survive edits so current charges and reservations remain valid.
func setProjectBudgetTx(tx *gorm.DB, projectID, userID, tokenID int, amount *billing.MicroUSD, warningPct int) error {
	scope := SpendBudgetScopeOrg
	if tokenID > 0 {
		scope = SpendBudgetScopeToken
	} else if userID > 0 {
		scope = SpendBudgetScopeUser
	}
	var budget SpendBudget
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("project_id = ? AND scope = ? AND user_id = ? AND token_id = ? AND period = ?", projectID, scope, userID, tokenID, SpendBudgetPeriodMonth).
		Order("id ASC").First(&budget).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		limit := billing.MicroUSD(0)
		if amount != nil {
			limit = *amount
		}
		budget = SpendBudget{ProjectID: projectID, Scope: scope, UserID: userID, TokenID: tokenID, Period: SpendBudgetPeriodMonth, LimitMicrousd: limit, WarningThresholdPct: warningPct, Enforcement: SpendBudgetEnforcementBlock, Enabled: true}
		if err := tx.Create(&budget).Error; err != nil {
			return err
		}
		if amount == nil {
			return tx.Model(&budget).Update("enabled", false).Error
		}
		return nil
	}
	budget.Enabled = amount != nil
	if amount != nil {
		budget.LimitMicrousd = *amount
	}
	budget.WarningThresholdPct = warningPct
	budget.Enforcement = SpendBudgetEnforcementBlock
	return tx.Save(&budget).Error
}

func ReadSpendBudgetUsed(budget SpendBudget, now time.Time) (billing.MicroUSD, error) {
	start, end := spendBudgetPeriodWindow(budget.Period, now)
	return sumSpendMicrousdForBudgetWithDB(CoreDB(), budget, start, end)
}
