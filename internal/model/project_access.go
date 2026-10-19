package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Project sentinels are catalog errors: errors.Is compares them by code, and
// httperr.Write renders them with their own status.
var (
	ErrProjectAccessSuspended = types.Err(types.ErrorCodeProjectAccessSuspended)
	ErrProjectForbidden       = types.Err(types.ErrorCodeAccessDenied)
	ErrProjectStateConflict   = types.Err(types.ErrorCodeStateConflict)
	ErrProjectUndoExpired     = types.Err(types.ErrorCodeUndoExpired)
)

// ProjectPrincipal is the trusted identity captured when a request is accepted.
// It is not recalculated when an asynchronous job later settles its charge.
type ProjectPrincipal struct {
	ConsoleReadAll bool
	ProjectID      int
	PayerUserID    int
	ActorUserID    int
	TokenID        int
	// The member's own limits, read with the membership on every request so a
	// change applies to the next call without waiting for a cache.
	MemberAllowedModels []string
	MemberEffortCap     string
}

func ResolveProjectPrincipal(db *gorm.DB, tokenID int, now time.Time) (ProjectPrincipal, error) {
	var identity ProjectPrincipal
	if db == nil || tokenID <= 0 {
		return identity, ErrProjectForbidden
	}
	// Read access state from the database rather than a cached token snapshot.
	// This makes removal/manual revocation authoritative on every API instance.
	var token Token
	if err := db.Select("id", "user_id", "project_id", "status", "expired_time", "removal_operation_id", "project_version", "unlimited_quota", "remain_quota").First(&token, tokenID).Error; err != nil {
		return identity, err
	}
	if token.ProjectID <= 0 {
		// An unattributed row is not a suspended membership: reporting it as
		// project_access_suspended would tell the holder to contact an admin
		// about a project they were never in. Keep the ordinary token-state
		// answer the gateway gave before projects existed.
		return identity, ErrTokenAccessInactive
	}
	var project Project
	if err := db.First(&project, token.ProjectID).Error; err != nil {
		return identity, err
	}
	// A Key belongs to exactly one member, and that member is the actor.
	memberID := token.UserId
	identity = ProjectPrincipal{ProjectID: project.ID, PayerUserID: project.OwnerUserID, ActorUserID: memberID, TokenID: token.Id}
	// A Key its own owner disabled, or one that simply ran out of calendar, is
	// an ordinary token-state failure. project_access_suspended is reserved for
	// membership and project state, which tells the holder to contact an admin.
	if token.Status == common.TokenStatusExpired || token.ExpiredTime != -1 && token.ExpiredTime <= now.Unix() {
		return identity, ErrTokenExpired
	}
	if token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted {
		return identity, ErrTokenDisabled
	}
	if token.Status == common.TokenStatusExhausted || !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return identity, ErrTokenBudgetExhausted
	}
	if project.PaymentRiskHold {
		return identity, ErrProjectAccessSuspended
	}
	var member ProjectMember
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, memberID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return identity, ErrProjectAccessSuspended
		}
		return identity, err
	}
	identity.MemberAllowedModels = []string(member.AllowedModels)
	identity.MemberEffortCap = member.ReasoningEffortCap
	if project.UpgradedAt == nil && memberID != project.OwnerUserID {
		return identity, ErrProjectAccessSuspended
	}
	var enabled int64
	ids := []int{project.OwnerUserID, memberID}
	if err := db.Model(&User{}).Where("id IN ? AND status = ?", ids, common.UserStatusEnabled).Count(&enabled).Error; err != nil {
		return identity, err
	}
	want := int64(2)
	if memberID == project.OwnerUserID {
		want = 1
	}
	if enabled != want {
		return identity, ErrProjectAccessSuspended
	}
	switch member.Status {
	case ProjectMemberActive:
		// A re-join must not revive credentials revoked by a previous removal.
		if token.RemovalOperationID != "" {
			return identity, ErrProjectAccessSuspended
		}
		return identity, nil
	default:
		// Removal and suspension stop every Key in the project immediately.
		return identity, ErrProjectAccessSuspended
	}
}

func lockProjectMember(tx *gorm.DB, projectID, userID int) (*ProjectMember, error) {
	var member ProjectMember
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id = ? AND user_id = ?", projectID, userID).First(&member).Error
	return &member, err
}

// RemoveProjectMember serializes on the project row. Invitations and role
// changes must use the same lock before changing membership. Removal stops the
// member's Keys immediately; only the 10-minute undo can bring them back.
func RemoveProjectMember(projectID, viewerID, targetID int, now time.Time) (*ProjectRemoval, error) {
	var receipt ProjectRemoval
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
		selfLeave := viewerID == targetID && viewer.Status == ProjectMemberActive && viewer.Role != ProjectRoleOwner
		if !selfLeave && !CanProjectAction(*viewer, target, ProjectManageMember, settings) {
			return ErrProjectForbidden
		}
		if target.Status == ProjectMemberRemoved {
			if project.UpgradedAt == nil || target.RemovalOperationID == "" {
				return ErrProjectStateConflict
			}
			return tx.Where("public_id = ? AND project_id = ? AND user_id = ?", target.RemovalOperationID, projectID, targetID).First(&receipt).Error
		}
		receipt = ProjectRemoval{ProjectID: projectID, UserID: targetID, PerformedBy: viewerID, PreviousStatus: target.Status, PreviousRole: target.Role, UndoUntil: now.Add(10 * time.Minute).Unix(), CreatedAt: now.Unix()}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		var tokens []Token
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id = ? AND user_id = ?", projectID, targetID).Order("id").Find(&tokens).Error; err != nil {
			return err
		}
		for _, token := range tokens {
			// Every Key stops at once. Eligibility only records what undo may restore.
			eligible := target.Status == ProjectMemberActive && token.Status == common.TokenStatusEnabled && (token.ExpiredTime == -1 || token.ExpiredTime > now.Unix()) && token.RemovalOperationID == ""
			snapshot := ProjectRemovalKey{RemovalID: receipt.ID, TokenID: token.Id, TokenPublicID: token.PublicID, PreviousStatus: token.Status, PreviousExpiredTime: token.ExpiredTime, ExpectedVersion: token.ProjectVersion + 1, Eligible: eligible}
			if err := tx.Create(&snapshot).Error; err != nil {
				return err
			}
			token.RemovalOperationID = receipt.PublicID
			token.ProjectVersion++
			// Keep the original status/expiry intact. The removal fence is independent
			// and cannot inadvertently turn a disabled or expired credential back on.
			if err := tx.Model(&token).Select("removal_operation_id", "project_version").Updates(&token).Error; err != nil {
				return err
			}
		}
		target.Status = ProjectMemberRemoved
		at := now.Unix()
		target.RemovedAt = &at
		target.RemovalOperationID = receipt.PublicID
		target.Version++
		if err := tx.Save(target).Error; err != nil {
			return err
		}
		return queueProjectMemberNoticeTx(tx, project, *target, projectNotice{Kind: noticeRemoved, ActorID: viewerID}, "removed:"+receipt.PublicID)
	})
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

type ProjectUndoResult struct {
	Status         string   `json:"status"`
	RestoredKeyIDs []string `json:"restored_key_ids"`
	SkippedKeyIDs  []string `json:"skipped_key_ids"`
}

func UndoProjectRemoval(projectID, viewerID, targetID int, removalID string, now time.Time) (ProjectUndoResult, error) {
	result := ProjectUndoResult{RestoredKeyIDs: []string{}, SkippedKeyIDs: []string{}}
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
		var receipt ProjectRemoval
		if err := tx.Where("public_id = ? AND project_id = ? AND user_id = ?", removalID, projectID, targetID).First(&receipt).Error; err != nil {
			return err
		}
		if receipt.UndoneAt != nil {
			if len(receipt.UndoResult) == 0 {
				return ErrProjectStateConflict
			}
			return json.Unmarshal(receipt.UndoResult, &result)
		}
		if receipt.UndoUntil <= now.Unix() {
			return ErrProjectUndoExpired
		}
		if project.UpgradedAt == nil || target.RemovalOperationID != removalID || target.Status != ProjectMemberRemoved {
			return ErrProjectStateConflict
		}
		var snapshots []ProjectRemovalKey
		if err := tx.Where("removal_id = ?", receipt.ID).Find(&snapshots).Error; err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			var token Token
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&token, snapshot.TokenID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) && snapshot.TokenPublicID != "" {
					result.SkippedKeyIDs = append(result.SkippedKeyIDs, snapshot.TokenPublicID)
					continue
				}
				return err
			}
			unchanged := !token.DeletedAt.Valid && token.ProjectVersion == snapshot.ExpectedVersion && token.RemovalOperationID == removalID && token.Status == snapshot.PreviousStatus && token.ExpiredTime == snapshot.PreviousExpiredTime
			unexpired := token.ExpiredTime == -1 || token.ExpiredTime > now.Unix()
			if !unchanged || !unexpired {
				result.SkippedKeyIDs = append(result.SkippedKeyIDs, token.PublicID)
				continue
			}
			token.RemovalOperationID = ""
			token.ProjectVersion++
			if err := tx.Model(&token).Select("removal_operation_id", "project_version").Updates(&token).Error; err != nil {
				return err
			}
			// Originally disabled credentials stay disabled; clearing our fence does
			// not claim that they were enabled or restored to usable status.
			if snapshot.Eligible {
				result.RestoredKeyIDs = append(result.RestoredKeyIDs, token.PublicID)
			} else {
				result.SkippedKeyIDs = append(result.SkippedKeyIDs, token.PublicID)
			}
		}
		target.Status = receipt.PreviousStatus
		target.RemovalOperationID = ""
		target.RemovedAt = nil
		target.Version++
		if err := tx.Save(target).Error; err != nil {
			return err
		}
		at := now.Unix()
		receipt.UndoneAt = &at
		result.Status = target.Status
		payload, err := json.Marshal(result)
		if err != nil {
			return err
		}
		receipt.UndoResult = JSONValue(payload)
		if err := tx.Save(&receipt).Error; err != nil {
			return err
		}
		result.Status = target.Status
		return queueProjectMemberNoticeTx(tx, project, *target, projectNotice{Kind: noticeRestored, ActorID: viewerID}, "undo:"+receipt.PublicID)
	})
	return result, err
}
