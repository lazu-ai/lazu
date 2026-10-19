package model

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrProjectRequestPending = types.Err(types.ErrorCodeRequestPending)

func CreateProjectBudgetRequest(projectID, userID int, requested billing.MicroUSD, reason string) (BudgetRequest, error) {
	var result BudgetRequest
	reason = strings.TrimSpace(reason)
	if requested < 0 || int64(requested) > 9_007_199_254_740_991 || utf8.RuneCountInString(reason) > 2000 {
		return result, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		member, err := lockProjectMember(tx, projectID, userID)
		if err != nil {
			return err
		}
		if member.Status != ProjectMemberActive || member.Role != ProjectRoleMember {
			return ErrProjectForbidden
		}
		var pending int64
		if err := tx.Model(&BudgetRequest{}).Where("project_id = ? AND user_id = ? AND status = ?", projectID, userID, "pending").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return ErrProjectRequestPending
		}
		result = BudgetRequest{ProjectID: projectID, UserID: userID, RequestedMicrousd: requested, Reason: reason, Status: "pending"}
		return tx.Create(&result).Error
	})
	return result, err
}

func HandleProjectBudgetRequest(projectID, viewerID int, requestID, action string, amount *billing.MicroUSD, note string, now time.Time) (BudgetRequest, error) {
	var result BudgetRequest
	if action != "approved" && action != "rejected" && action != "withdrawn" {
		return result, ErrProjectInvalidInput
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 2000 || action == "rejected" && utf8.RuneCountInString(note) < 4 {
		return result, ErrProjectInvalidInput
	}
	if amount != nil && (*amount < 0 || int64(*amount) > 9_007_199_254_740_991) {
		return result, ErrProjectInvalidInput
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
		if err := tx.Where("project_id = ? AND public_id = ?", projectID, requestID).First(&result).Error; err != nil {
			return err
		}
		target, err := lockProjectMember(tx, projectID, result.UserID)
		if err != nil {
			return err
		}
		if action == "withdrawn" {
			if viewer.UserID != result.UserID || viewer.Status != ProjectMemberActive || viewer.Role != ProjectRoleMember {
				return ErrProjectForbidden
			}
		} else {
			settings, err := project.GetSettings()
			if err != nil {
				return err
			}
			if target.Role != ProjectRoleMember || !CanProjectAction(*viewer, target, ProjectManageMember, settings) {
				return ErrProjectForbidden
			}
		}
		if result.Status == action {
			return nil
		}
		if result.Status != "pending" || target.Status == ProjectMemberRemoved {
			return ErrProjectStateConflict
		}
		if action == "approved" {
			cap := amount
			result.ApprovedBudgetMicrousd = amount
			_, warning, err := readMemberBudgetTx(tx, projectID, target.UserID)
			if err != nil {
				return err
			}
			if err := updateProjectMemberBudgetTx(tx, target, viewerID, cap, warning, "request:"+result.PublicID); err != nil {
				return err
			}
		}
		result.Status = action
		result.HandledBy = viewerID
		result.HandledNote = note
		at := now.Unix()
		result.HandledAt = &at
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		if action != "withdrawn" {
			return queueProjectMemberNoticeTx(tx, project, *target, projectNotice{Kind: noticeBudgetRequest, ActorID: viewerID, Action: action, Budget: amount, Note: note}, "request:"+result.PublicID+":"+action)
		}
		return nil
	})
	return result, err
}
