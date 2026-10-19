package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ProjectRoleOwner       = "owner"
	ProjectRoleAdmin       = "admin"
	ProjectRoleMember      = "member"
	ProjectMemberActive    = "active"
	ProjectMemberSuspended = "suspended"
	ProjectMemberRemoved   = "removed"
)

// ProjectKeyLimit caps non-deleted Keys per person per project. It is a guard
// against a runaway script, not a product rule: one Key per tool is good
// practice and spend is already bounded per member, so nobody working by hand
// should reach it. Disabled and expired Keys count; internal ones do not.
const ProjectKeyLimit = 200

// ProjectInviteValidity is fixed at three days and is never configurable.
const ProjectInviteValidity = 3 * 24 * time.Hour

// Project retains the existing owner's wallet. Upgrading to shared usage never
// creates another payer or moves money, credentials, or history.
type Project struct {
	ID                      int       `json:"-" gorm:"primaryKey"`
	PublicID                string    `json:"id" gorm:"size:64;not null;uniqueIndex"`
	OwnerUserID             int       `json:"-" gorm:"not null;uniqueIndex"`
	Name                    string    `json:"name" gorm:"size:191;not null"`
	UpgradedAt              *int64    `json:"upgraded_at"`
	Settings                JSONValue `json:"settings" gorm:"type:json"`
	StripeCustomerStartedAt int64     `json:"-"`
	StripeCustomerID        string    `json:"-" gorm:"size:64;index"`
	BillingDetails          JSONValue `json:"-" gorm:"type:json"`
	PaymentRiskHold         bool      `json:"-" gorm:"not null;default:false"`
	Version                 int64     `json:"version" gorm:"not null;default:1"`
	CreatedAt               int64     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt               int64     `json:"updated_at" gorm:"autoUpdateTime"`
}

type ProjectSettings struct {
	WhoCanTopup                 string            `json:"who_can_topup"`
	DefaultMemberBudgetMicrousd *billing.MicroUSD `json:"default_member_budget_microusd"`
	WarningPct                  int               `json:"warning_pct"`
	ProjectCapMicrousd          *billing.MicroUSD `json:"project_cap_microusd"`
	// DefaultReasoningEffortCap seeds the cap of members who join from now on.
	DefaultReasoningEffortCap string `json:"default_reasoning_effort_cap"`
}

func DefaultProjectSettings() ProjectSettings {
	budget := billing.MicroUSD(100_000_000)
	return ProjectSettings{WhoCanTopup: "owner_admin", DefaultMemberBudgetMicrousd: &budget, WarningPct: 80}
}

func (s ProjectSettings) Validate() error {
	if s.WhoCanTopup != "owner" && s.WhoCanTopup != "owner_admin" {
		return types.Err(types.ErrorCodeInvalidSettings, types.D{"field": "who_can_topup"})
	}
	if s.WarningPct < 1 || s.WarningPct > 99 {
		return types.Err(types.ErrorCodeInvalidSettings, types.D{"field": "warning_pct"})
	}
	if !ValidEffortCap(s.DefaultReasoningEffortCap) {
		return types.Err(types.ErrorCodeInvalidSettings, types.D{"field": "default_reasoning_effort_cap"})
	}
	for _, amount := range []*billing.MicroUSD{s.DefaultMemberBudgetMicrousd, s.ProjectCapMicrousd} {
		if amount != nil && (*amount < 0 || int64(*amount) > 9_007_199_254_740_991) {
			return types.Err(types.ErrorCodeInvalidSettings, types.D{"field": "budget_microusd"})
		}
	}
	return nil
}

func (p Project) GetSettings() (ProjectSettings, error) {
	var settings ProjectSettings
	if err := json.Unmarshal(p.Settings, &settings); err != nil {
		return settings, fmt.Errorf("read project settings: %w", err)
	}
	return settings, settings.Validate()
}

func (p *Project) BeforeCreate(tx *gorm.DB) error {
	if p.PublicID == "" {
		p.PublicID = common.GeneratePublicID("prj")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.OwnerUserID <= 0 || p.Name == "" || utf8.RuneCountInString(p.Name) > 191 {
		return errors.New("invalid project owner or name")
	}
	if len(p.Settings) == 0 {
		data, err := json.Marshal(DefaultProjectSettings())
		if err != nil {
			return err
		}
		p.Settings = JSONValue(data)
	}
	_, err := p.GetSettings()
	return err
}

type ProjectMember struct {
	ID                 int    `json:"-" gorm:"primaryKey"`
	ProjectID          int    `json:"-" gorm:"not null;uniqueIndex:idx_project_member,priority:1"`
	UserID             int    `json:"-" gorm:"not null;uniqueIndex:idx_project_member,priority:2;index"`
	Role               string `json:"role" gorm:"size:16;not null"`
	Status             string `json:"status" gorm:"size:16;not null"`
	InvitedBy          int    `json:"-"`
	JoinedAt           int64  `json:"joined_at"`
	RemovedAt          *int64 `json:"removed_at"`
	RemovalOperationID string `json:"-" gorm:"size:64"`
	Version            int64  `json:"version" gorm:"not null;default:1"`
	// AllowedModels narrows which models this member's Keys may call; empty
	// means every model the project serves. Each Key may narrow it further.
	AllowedModels StringSlice `json:"allowed_models" gorm:"type:json"`
	// ReasoningEffortCap is the highest reasoning effort this member's Keys may
	// use; "" means no cap. A Key's own cap can only lower it.
	ReasoningEffortCap string `json:"reasoning_effort_cap" gorm:"type:varchar(16);not null;default:''"`
}

type ProjectInvite struct {
	ID                 int               `json:"-" gorm:"primaryKey"`
	PublicID           string            `json:"id" gorm:"size:64;not null;uniqueIndex"`
	ProjectID          int               `json:"-" gorm:"not null;index"`
	Email              string            `json:"email" gorm:"size:254;not null;index"`
	Role               string            `json:"role" gorm:"size:16;not null"`
	BudgetMicrousd     *billing.MicroUSD `json:"budget_microusd" gorm:"type:bigint"`
	ReasoningEffortCap string            `json:"reasoning_effort_cap" gorm:"type:varchar(16);not null;default:''"`
	WarningPct         int               `json:"warning_pct"`
	TokenDigest        string            `json:"-" gorm:"size:64;not null;uniqueIndex"`
	ExpiresAt          int64             `json:"expires_at"`
	Status             string            `json:"status" gorm:"size:16;not null;index"`
	InvitedBy          int               `json:"-"`
	AcceptedUserID     int               `json:"-"`
	ResentAt           *int64            `json:"resent_at"`
	CreatedAt          int64             `json:"created_at" gorm:"autoCreateTime"`
}

func (p *ProjectInvite) BeforeCreate(tx *gorm.DB) error {
	if p.PublicID == "" {
		p.PublicID = common.GeneratePublicID("pinv")
	}
	return nil
}

type BudgetRequest struct {
	ApprovedBudgetMicrousd *billing.MicroUSD `json:"approved_budget_microusd" gorm:"type:bigint"`
	ID                     int               `json:"-" gorm:"primaryKey"`
	PublicID               string            `json:"id" gorm:"size:64;not null;uniqueIndex"`
	ProjectID              int               `json:"-" gorm:"not null;index"`
	UserID                 int               `json:"-" gorm:"not null;index"`
	RequestedMicrousd      billing.MicroUSD  `json:"requested_microusd" gorm:"type:bigint"`
	Reason                 string            `json:"reason" gorm:"type:text"`
	Status                 string            `json:"status" gorm:"size:16;not null;index"`
	HandledBy              int               `json:"-"`
	HandledNote            string            `json:"handled_note" gorm:"type:text"`
	HandledAt              *int64            `json:"handled_at"`
	CreatedAt              int64             `json:"created_at" gorm:"autoCreateTime"`
}

func (r *BudgetRequest) BeforeCreate(tx *gorm.DB) error {
	if r.PublicID == "" {
		r.PublicID = common.GeneratePublicID("breq")
	}
	return nil
}

// Removal records are durable undo receipts, not a fresh grant of access. The
// per-Key version fence prevents undo from overwriting a later manual change.
type ProjectRemoval struct {
	UndoResult     JSONValue `json:"-" gorm:"type:json"`
	ID             int       `json:"-" gorm:"primaryKey"`
	PublicID       string    `json:"id" gorm:"size:64;not null;uniqueIndex"`
	ProjectID      int       `json:"-" gorm:"not null;index"`
	UserID         int       `json:"-" gorm:"not null;index"`
	PerformedBy    int       `json:"-"`
	PreviousStatus string    `json:"-" gorm:"size:16"`
	PreviousRole   string    `json:"-" gorm:"size:16"`
	UndoUntil      int64     `json:"undo_until"`
	UndoneAt       *int64    `json:"undone_at"`
	CreatedAt      int64     `json:"created_at" gorm:"autoCreateTime"`
}

func (r *ProjectRemoval) BeforeCreate(tx *gorm.DB) error {
	if r.PublicID == "" {
		r.PublicID = common.GeneratePublicID("prem")
	}
	return nil
}

type ProjectRemovalKey struct {
	TokenPublicID       string `json:"-" gorm:"size:64"`
	ID                  int    `json:"-" gorm:"primaryKey"`
	RemovalID           int    `json:"-" gorm:"not null;uniqueIndex:idx_removal_key,priority:1"`
	TokenID             int    `json:"-" gorm:"not null;uniqueIndex:idx_removal_key,priority:2"`
	PreviousStatus      int    `json:"-"`
	PreviousExpiredTime int64  `json:"-"`
	ExpectedVersion     int64  `json:"-"`
	Eligible            bool   `json:"-"`
}

type ProjectBudgetHistory struct {
	ID                int               `json:"-" gorm:"primaryKey"`
	PublicID          string            `json:"id" gorm:"size:64;not null;uniqueIndex"`
	ProjectID         int               `json:"-" gorm:"index;not null"`
	UserID            int               `json:"-" gorm:"index;not null"`
	ChangedBy         int               `json:"-"`
	OldBudgetMicrousd *billing.MicroUSD `json:"old_budget_microusd" gorm:"type:bigint"`
	NewBudgetMicrousd *billing.MicroUSD `json:"new_budget_microusd" gorm:"type:bigint"`
	Reason            string            `json:"reason" gorm:"type:text"`
	CreatedAt         int64             `json:"created_at" gorm:"autoCreateTime"`
}

func (h *ProjectBudgetHistory) BeforeCreate(tx *gorm.DB) error {
	if h.PublicID == "" {
		h.PublicID = common.GeneratePublicID("pbh")
	}
	return nil
}

// ensureDefaultProjectTx uses the owner's uniqueness constraint to serialize
// competing signup/backfill attempts without changing a previously shared row.
func ensureDefaultProjectTx(tx *gorm.DB, user *User) (*Project, error) {
	if user == nil || user.Id <= 0 {
		return nil, errors.New("persisted user required")
	}
	candidate := Project{OwnerUserID: user.Id, Name: user.Username}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_user_id"}}, DoNothing: true}).Create(&candidate).Error; err != nil {
		return nil, err
	}
	var project Project
	if err := tx.Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		return nil, err
	}
	ownerStatus := ProjectMemberActive
	if user.DeletedAt.Valid {
		ownerStatus = ProjectMemberRemoved
	}
	member := ProjectMember{ProjectID: project.ID, UserID: user.Id, Role: ProjectRoleOwner, Status: ownerStatus, JoinedAt: project.CreatedAt}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "user_id"}}, DoNothing: true}).Create(&member).Error; err != nil {
		return nil, err
	}
	var existing ProjectMember
	if err := tx.Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&existing).Error; err != nil {
		return nil, err
	}
	if existing.Role != ProjectRoleOwner || (!user.DeletedAt.Valid && existing.Status != ProjectMemberActive) {
		return nil, errors.New("default project has invalid owner membership")
	}
	return &project, nil
}

// AfterCreate covers password signup, OAuth, admin-created accounts and root
// bootstrap in the same transaction. Minimal legacy/test databases without the
// project schema remain usable; production installs migrate it before startup.
func (user *User) AfterCreate(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&Project{}) {
		return nil
	}
	_, err := ensureDefaultProjectTx(tx, user)
	return err
}
