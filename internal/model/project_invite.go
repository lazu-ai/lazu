package model

import (
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProjectAlreadyMember      = types.Err(types.ErrorCodeAlreadyMember)
	ErrProjectAlreadyInvited     = types.Err(types.ErrorCodeAlreadyInvited)
	ErrProjectInviteExpired      = types.Err(types.ErrorCodeInviteExpired)
	ErrProjectInviteRevoked      = types.Err(types.ErrorCodeInviteRevoked)
	ErrProjectEmailMismatch      = types.Err(types.ErrorCodeInviteEmailMismatch)
	ErrProjectEmailUnverified    = types.Err(types.ErrorCodeInviteEmailUnverified)
	ErrProjectFeatureUnavailable = types.Err(types.ErrorCodeFeatureUnavailable)
	ErrProjectInviteeUnavailable = types.Err(types.ErrorCodeInviteeFeatureUnavailable)
)

type ProjectInviteRateError struct{ RetryAfter int64 }

func (err ProjectInviteRateError) Error() string { return "resend_too_soon" }

// APIError renders the wait as the catalog's resend_too_soon.
func (err ProjectInviteRateError) APIError() *types.APIError {
	return types.Err(types.ErrorCodeResendTooSoon, types.D{"retry_after_seconds": err.RetryAfter})
}

type CreateProjectInviteInput struct {
	Email          string
	Role           string
	BudgetProvided bool
	BudgetMicrousd *billing.MicroUSD
	ProjectName    *string
	BaseURL        string
}

func projectInviteDigest(raw string) string { return common.GenerateHMAC("project-invite:" + raw) }

// EffectiveExpiresAt is the deadline that actually governs an invitation. Rows
// written before the three-day rule stored a seven-day expires_at, and trusting
// the stored value would keep those links usable on days four to seven. The
// clamp is applied at read time rather than by a one-off migration so a row
// written by an older binary still in a rolling deploy cannot slip past it.
// A resend restarts the window, so it is the later of creation and resend that
// the validity runs from.
func (invite ProjectInvite) EffectiveExpiresAt() int64 {
	issued := invite.CreatedAt
	if invite.ResentAt != nil && *invite.ResentAt > issued {
		issued = *invite.ResentAt
	}
	if issued <= 0 {
		return invite.ExpiresAt
	}
	if deadline := issued + int64(ProjectInviteValidity/time.Second); deadline < invite.ExpiresAt {
		return deadline
	}
	return invite.ExpiresAt
}

// ProjectInvitePendingScope is the SQL counterpart of EffectiveStatus: it counts
// exactly the invitations EffectiveExpiresAt still considers pending. A summary
// that trusted the stored expires_at would keep reporting a legacy seven-day
// link as pending on days four to seven while accept and preview already reject
// it. min(expires_at, issued+validity) > now is the same predicate as
// expires_at > now AND issued + validity > now, which keeps this an index-usable
// comparison instead of a full scan through Go.
func ProjectInvitePendingScope(tx *gorm.DB, now time.Time) *gorm.DB {
	// A row written before created_at was recorded has no issuance to clamp to,
	// so its stored deadline is all there is.
	issued := "CASE WHEN COALESCE(resent_at, 0) > created_at THEN COALESCE(resent_at, 0) ELSE created_at END"
	return tx.Where("status = ?", "pending").
		Where("expires_at > ?", now.Unix()).
		Where("(("+issued+") <= 0 OR ("+issued+") > ?)", now.Add(-ProjectInviteValidity).Unix())
}

func (invite ProjectInvite) EffectiveStatus(now time.Time) string {
	if invite.Status == "pending" && invite.EffectiveExpiresAt() <= now.Unix() {
		return "expired"
	}
	return invite.Status
}
func checkProjectInviteeRollout(tx *gorm.DB, email string) error {
	if projectsEnabledForPublicID("") {
		return nil
	}
	// Like GetUserByEmail, require one unambiguous account. Email is not
	// unique, so choosing the first match makes rollout access depend on row order.
	var users []User
	if err := tx.Select("public_id").Where("LOWER(email) = ?", strings.ToLower(strings.TrimSpace(email))).Limit(2).Find(&users).Error; err != nil {
		return err
	}
	if len(users) != 1 || !projectsEnabledForPublicID(users[0].PublicID) {
		return ErrProjectInviteeUnavailable
	}
	return nil
}

func CreateProjectInvite(projectID, viewerID int, input CreateProjectInviteInput, now time.Time) (ProjectInvite, error) {
	var result ProjectInvite
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	parsed, err := mail.ParseAddress(input.Email)
	if err != nil || parsed.Address != input.Email || len(input.Email) > 254 {
		return result, types.Err(types.ErrorCodeInviteEmailInvalid)
	}
	if input.Role != ProjectRoleAdmin && input.Role != ProjectRoleMember {
		return result, types.Err(types.ErrorCodeInviteRoleInvalid)
	}
	if input.ProjectName != nil && (strings.TrimSpace(*input.ProjectName) == "" || utf8.RuneCountInString(strings.TrimSpace(*input.ProjectName)) > 191) {
		return result, types.Err(types.ErrorCodeProjectNameInvalid)
	}
	validation := DefaultProjectSettings()
	validation.DefaultMemberBudgetMicrousd = input.BudgetMicrousd
	if err := validation.Validate(); err != nil {
		return result, err
	}
	if !ProjectsEnabledForUser(viewerID) {
		return result, ErrProjectFeatureUnavailable
	}
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		viewer, err := lockProjectMember(tx, projectID, viewerID)
		if err != nil {
			return err
		}
		settings, err := project.GetSettings()
		if err != nil {
			return err
		}
		proposed := ProjectMember{ProjectID: projectID, Role: input.Role}
		if !CanProjectAction(*viewer, &proposed, ProjectInviteMember, settings) {
			return ErrProjectForbidden
		}
		// Do not upgrade the project or enqueue an invitation its recipient
		// cannot accept during an account allowlist rollout.
		if err := checkProjectInviteeRollout(tx, input.Email); err != nil {
			return err
		}
		var members int64
		if err := tx.Model(&ProjectMember{}).Joins("JOIN users ON users.id = project_members.user_id").
			Where("project_members.project_id = ? AND project_members.status <> ? AND LOWER(users.email) = ?", projectID, ProjectMemberRemoved, input.Email).Count(&members).Error; err != nil {
			return err
		}
		if members > 0 {
			return ErrProjectAlreadyMember
		}
		var pending int64
		// The stored deadline is clamped the same way EffectiveExpiresAt clamps
		// it, so an outstanding seven-day row no longer blocks a fresh invite
		// once its real three-day window has closed.
		if err := tx.Model(&ProjectInvite{}).Where("project_id = ? AND email = ? AND status = ? AND expires_at > ? AND COALESCE(resent_at, created_at) > ?",
			projectID, input.Email, "pending", now.Unix(), now.Add(-ProjectInviteValidity).Unix()).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return ErrProjectAlreadyInvited
		}
		raw, err := common.GenerateRandomCharsKey(48)
		if err != nil {
			return err
		}
		amount := input.BudgetMicrousd
		if !input.BudgetProvided {
			amount = settings.DefaultMemberBudgetMicrousd
		}
		result = ProjectInvite{ProjectID: projectID, Email: input.Email, Role: input.Role, BudgetMicrousd: amount, WarningPct: settings.WarningPct, ReasoningEffortCap: NormalizeEffortCap(settings.DefaultReasoningEffortCap), TokenDigest: projectInviteDigest(raw), ExpiresAt: now.Add(ProjectInviteValidity).Unix(), Status: "pending", InvitedBy: viewerID, CreatedAt: now.Unix()}
		if project.UpgradedAt == nil {
			at := now.Unix()
			project.UpgradedAt = &at
			if input.ProjectName != nil {
				project.Name = strings.TrimSpace(*input.ProjectName)
			}
			project.Version++
			if err := tx.Save(&project).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return queueProjectInviteTx(tx, project, result, raw, input.BaseURL)
	})
	return result, err
}

func ChangeProjectInvite(projectID, viewerID int, inviteID string, resend bool, baseURL string, now time.Time) (ProjectInvite, error) {
	var invite ProjectInvite
	if !ProjectsEnabledForUser(viewerID) {
		return invite, ErrProjectFeatureUnavailable
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
		if err := tx.Where("project_id = ? AND public_id = ?", projectID, inviteID).First(&invite).Error; err != nil {
			return err
		}
		settings, err := project.GetSettings()
		if err != nil {
			return err
		}
		target := ProjectMember{ProjectID: projectID, Role: invite.Role}
		if !CanProjectAction(*viewer, &target, ProjectInviteMember, settings) {
			return ErrProjectForbidden
		}
		if invite.Status == "revoked" && !resend {
			return nil
		}
		if invite.Status != "pending" {
			return ErrProjectStateConflict
		}
		if !resend {
			invite.Status = "revoked"
			if err := cancelProjectInviteMailTx(tx, projectID, invite.TokenDigest); err != nil {
				return err
			}
			return tx.Save(&invite).Error
		}
		if err := checkProjectInviteeRollout(tx, invite.Email); err != nil {
			return err
		}
		last := invite.CreatedAt
		if invite.ResentAt != nil {
			last = *invite.ResentAt
		}
		if now.Unix() < last+60 {
			return ProjectInviteRateError{RetryAfter: last + 60 - now.Unix()}
		}
		raw, err := common.GenerateRandomCharsKey(48)
		if err != nil {
			return err
		}
		if err := cancelProjectInviteMailTx(tx, projectID, invite.TokenDigest); err != nil {
			return err
		}
		at := now.Unix()
		invite.ResentAt = &at
		invite.TokenDigest = projectInviteDigest(raw)
		invite.ExpiresAt = now.Add(ProjectInviteValidity).Unix()
		if err := tx.Save(&invite).Error; err != nil {
			return err
		}
		return queueProjectInviteTx(tx, project, invite, raw, baseURL)
	})
	return invite, err
}

func LookupProjectInvite(raw string) (ProjectInvite, error) {
	var invite ProjectInvite
	if len(raw) != 48 {
		return invite, gorm.ErrRecordNotFound
	}
	err := CoreDB().Where("token_digest = ?", projectInviteDigest(raw)).First(&invite).Error
	return invite, err
}

// ProjectInviteOpensSignup reports whether raw is a live invitation addressed
// to email. A deployment with registration closed still lets the person an
// invitation names create the account it was sent to; the code they receive
// proves the address, exactly as it does for an ordinary sign-up.
func ProjectInviteOpensSignup(raw, email string, now time.Time) bool {
	if raw == "" || !projectsEnabledForPublicID("") {
		return false
	}
	invite, err := LookupProjectInvite(raw)
	if err != nil {
		return false
	}
	return invite.EffectiveStatus(now) == "pending" && strings.EqualFold(invite.Email, strings.TrimSpace(email))
}

func AcceptProjectInvite(raw string, userID int, now time.Time, baseURL string) (ProjectMember, error) {
	var result ProjectMember
	if !ProjectsEnabledForUser(userID) {
		return result, ErrProjectFeatureUnavailable
	}
	initial, err := LookupProjectInvite(raw)
	if err != nil {
		return result, err
	}
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, initial.ProjectID).Error; err != nil {
			return err
		}
		var invite ProjectInvite
		if err := tx.Where("id = ? AND token_digest = ?", initial.ID, projectInviteDigest(raw)).First(&invite).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(user.Email), invite.Email) {
			return ErrProjectEmailMismatch
		}
		if !user.HasVerifiedEmail() {
			return ErrProjectEmailUnverified
		}
		if user.Status != common.UserStatusEnabled {
			return ErrProjectForbidden
		}
		if invite.Status == "accepted" && invite.AcceptedUserID == userID {
			return tx.Where("project_id = ? AND user_id = ?", project.ID, userID).First(&result).Error
		}
		if invite.Status == "revoked" || project.UpgradedAt == nil {
			return ErrProjectInviteRevoked
		}
		if invite.EffectiveStatus(now) == "expired" {
			return ErrProjectInviteExpired
		}
		if invite.Status != "pending" {
			return ErrProjectStateConflict
		}
		err := tx.Where("project_id = ? AND user_id = ?", project.ID, userID).First(&result).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && result.Status != ProjectMemberRemoved {
			return ErrProjectAlreadyMember
		}
		result.ProjectID, result.UserID = project.ID, userID
		result.Role, result.Status = invite.Role, ProjectMemberActive
		result.InvitedBy, result.JoinedAt = invite.InvitedBy, now.Unix()
		result.RemovedAt, result.RemovalOperationID = nil, ""
		// Pending invitations retain the policy captured when issued, including
		// on resend and when a removed member rejoins.
		result.AllowedModels = nil
		result.ReasoningEffortCap = invite.ReasoningEffortCap
		if result.ID == 0 {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		} else {
			result.Version++
			if err := tx.Save(&result).Error; err != nil {
				return err
			}
		}
		if err := updateProjectMemberBudgetTx(tx, &result, invite.InvitedBy, invite.BudgetMicrousd, invite.WarningPct, "invitation"); err != nil {
			return err
		}
		invite.Status, invite.AcceptedUserID = "accepted", userID
		if err := tx.Save(&invite).Error; err != nil {
			return err
		}
		if err := cancelProjectInviteFollowUpsTx(tx, project.ID, invite.TokenDigest); err != nil {
			return err
		}
		return queueProjectJoinedTx(tx, project, result, invite, baseURL)
	})
	return result, err
}

func cancelProjectInviteMailTx(tx *gorm.DB, projectID int, digest string) error {
	keys := []string{projectInviteMailKey(digest), projectInviteReminderKey(digest), projectInviteExpiredKey(digest)}
	return tx.Model(&ProjectNotification{}).Where("project_id = ? AND event_key IN ? AND status IN ?", projectID, keys, []string{"pending", "sending"}).Updates(map[string]interface{}{"status": "cancelled", "payload": "", "claimed_at": nil}).Error
}

// cancelProjectInviteFollowUpsTx withdraws the reminder and the expiry notice
// once an invitation has been answered. The invitation mail itself has gone.
func cancelProjectInviteFollowUpsTx(tx *gorm.DB, projectID int, digest string) error {
	keys := []string{projectInviteReminderKey(digest), projectInviteExpiredKey(digest)}
	return tx.Model(&ProjectNotification{}).Where("project_id = ? AND event_key IN ? AND status = ?", projectID, keys, "pending").Updates(map[string]interface{}{"status": "cancelled", "payload": "", "claimed_at": nil}).Error
}
