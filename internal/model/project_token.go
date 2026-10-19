package model

import (
	"errors"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrProjectKeyLimit = types.Err(types.ErrorCodeKeyLimitReached, types.D{"limit": ProjectKeyLimit})

// ProjectVisibleTokensQuery is the list-side projection of the Keys a viewer may
// see. A manager reading the whole project sees every Key in it; anyone else
// sees the Keys they own, through the same scope the member-detail list and the
// Key limit use, so the three can never disagree about what a member holds.
func ProjectVisibleTokensQuery(tx *gorm.DB, project Project, member ProjectMember, all bool) *gorm.DB {
	query := excludeInternalTokens(tx.Model(&Token{})).Where("project_id = ?", project.ID)
	if member.Status != ProjectMemberActive {
		return query.Where("1 = 0")
	}
	if all && member.IsManager() {
		return query
	}
	return ProjectMemberTokenScope(query, member.UserID)
}

func CheckProjectTokenAction(tx *gorm.DB, projectID, viewerID, tokenID int, action ProjectAction) (Token, error) {
	var token Token
	if err := excludeInternalTokens(tx).Where("id = ? AND project_id = ?", tokenID, projectID).First(&token).Error; err != nil {
		return token, err
	}
	var viewer ProjectMember
	if err := tx.Where("project_id = ? AND user_id = ?", projectID, viewerID).First(&viewer).Error; err != nil {
		return token, err
	}
	var project Project
	if err := tx.First(&project, projectID).Error; err != nil {
		return token, err
	}
	settings, err := project.GetSettings()
	if err != nil {
		return token, err
	}
	var owner ProjectMember
	if err := tx.Where("project_id = ? AND user_id = ?", projectID, token.UserId).First(&owner).Error; err != nil {
		return token, err
	}
	if project.UpgradedAt == nil && viewer.UserID != project.OwnerUserID {
		return token, ErrProjectForbidden
	}
	if !CanProjectAction(viewer, &owner, action, settings) {
		return token, ErrProjectForbidden
	}
	// A stopped Key can only be read or deleted. That is true while its owner is
	// outside the project — editing, rotating or revealing it would mutate state
	// the undo receipt expects to find unchanged, and rotation bumps the project
	// version and silently breaks the undo — and it stays true after a re-invite,
	// which restores the membership but never the Key. Rotating one then would
	// mint a secret that can never authenticate.
	stopped := owner.Status != ProjectMemberActive || token.StoppedByRemoval()
	if stopped && action != ProjectReadPersonalResource && action != ProjectDeletePersonalKey {
		return token, ErrProjectStateConflict
	}
	return token, nil
}

// StoppedByRemoval reports a Key that a removal or dissolution stopped. Such a
// Key can only be deleted; it is never revived, not even by a re-invite.
func (token *Token) StoppedByRemoval() bool {
	return token != nil && token.RemovalOperationID != ""
}

// ProjectMemberTokenScope narrows a Token query to the Keys one person owns.
// A Key belongs to exactly one member, named by tokens.user_id, and every owner
// projection — the limit count, key_count and the member-detail keys[] list —
// goes through here so they cannot drift apart.
func ProjectMemberTokenScope(tx *gorm.DB, userID int) *gorm.DB {
	return tx.Where("user_id = ?", userID)
}

func CreateProjectPersonalToken(projectID, userID int, token *Token, capUpdates ...ProjectTokenCapUpdate) error {
	return createProjectPersonalToken(projectID, userID, token, true, capUpdates...)
}

// The default-off rollout must retain the old unshared account's creation
// behavior. This entry cannot bypass limits on an already shared project.
func CreateLegacyDefaultProjectPersonalToken(projectID, userID int, token *Token, capUpdates ...ProjectTokenCapUpdate) error {
	return createProjectPersonalToken(projectID, userID, token, false, capUpdates...)
}
func createProjectPersonalToken(projectID, userID int, token *Token, enforceLimit bool, capUpdates ...ProjectTokenCapUpdate) error {
	if token == nil || token.IsInternal() {
		return ErrProjectInvalidInput
	}
	token.NormalizeRateLimits()
	if err := token.NormalizeProductMetadata(); err != nil {
		return err
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		member, err := lockProjectMember(tx, projectID, userID)
		if err != nil {
			return err
		}
		if member.Status != ProjectMemberActive || project.UpgradedAt == nil && project.OwnerUserID != userID {
			return ErrProjectForbidden
		}
		if !KeyEffortCapAllowed(token.ReasoningEffortCap, member.ReasoningEffortCap) {
			return ErrProjectInvalidInput
		}
		token.ReasoningEffortCap = NormalizeEffortCap(token.ReasoningEffortCap)
		var count int64
		// Every non-deleted Key counts, including one a removal stopped: a
		// remove/re-invite cycle would otherwise hand out ten fresh slots each
		// time. A stopped Key says so in the payload and can be deleted, which
		// is how a rejoined member frees the allowance.
		countQuery := ProjectMemberTokenScope(excludeInternalTokens(tx.Model(&Token{})).Where("project_id = ?", projectID), userID)
		if err := countQuery.Count(&count).Error; err != nil {
			return err
		}
		if !enforceLimit && (project.UpgradedAt != nil || project.OwnerUserID != userID) {
			return ErrProjectForbidden
		}
		if enforceLimit && count >= ProjectKeyLimit {
			return ErrProjectKeyLimit
		}
		token.ProjectID, token.UserId = projectID, userID
		token.Status = common.TokenStatusEnabled
		if err := tx.Create(token).Error; err != nil {
			return err
		}
		if len(capUpdates) > 0 {
			return setProjectBudgetTx(tx, projectID, 0, token.Id, capUpdates[0].Cap, 80)
		}
		return nil
	})
}

// CreateDefaultUserToken issues the sign-up Key through the project-aware path
// so a brand-new account's Key belongs to its default project, is charged to
// that project's balance and is enabled on arrival. Minimal legacy databases
// without the project schema keep the plain insert, matching User.AfterCreate.
func CreateDefaultUserToken(userID int, token *Token) error {
	if CoreDB() == nil {
		return errors.New("database is not initialized")
	}
	if token == nil || userID <= 0 {
		return ErrProjectInvalidInput
	}
	if !CoreDB().Migrator().HasTable(&Project{}) {
		token.UserId = userID
		return token.Insert()
	}
	var project Project
	if err := CoreDB().Where("owner_user_id = ?", userID).First(&project).Error; err != nil {
		return err
	}
	if project.UpgradedAt == nil && !ProjectsEnabledForUser(userID) {
		return CreateLegacyDefaultProjectPersonalToken(project.ID, userID, token)
	}
	return CreateProjectPersonalToken(project.ID, userID, token)
}

func UpdateProjectToken(projectID, viewerID int, token *Token, capUpdates ...ProjectTokenCapUpdate) error {
	if token == nil || token.IsInternal() {
		return ErrProjectInvalidInput
	}
	token.NormalizeRateLimits()
	if err := token.NormalizeProductMetadata(); err != nil {
		return err
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		current, err := CheckProjectTokenAction(tx, projectID, viewerID, token.Id, ProjectManagePersonalKey)
		if err != nil {
			return err
		}
		if current.ProjectVersion != token.ProjectVersion {
			return ErrProjectVersionConflict
		}
		// Read the Key owner's policy under the same project/member locks
		// used by member edits; a manager's own ceiling is irrelevant here.
		owner, err := lockProjectMember(tx, projectID, current.UserId)
		if err != nil {
			return err
		}
		token.ReasoningEffortCap = NormalizeEffortCap(token.ReasoningEffortCap)
		if token.ReasoningEffortCap != current.ReasoningEffortCap && !KeyEffortCapAllowed(token.ReasoningEffortCap, owner.ReasoningEffortCap) {
			return ErrProjectInvalidInput
		}
		if len(capUpdates) > 0 {
			settings, err := project.GetSettings()
			if err != nil {
				return err
			}
			settings.ProjectCapMicrousd = capUpdates[0].Cap
			if err := settings.Validate(); err != nil {
				return ErrProjectInvalidInput
			}
			if err := setProjectBudgetTx(tx, projectID, 0, token.Id, capUpdates[0].Cap, settings.WarningPct); err != nil {
				return err
			}
		}
		if err := updateTokenFieldsTx(tx, token); err != nil {
			return err
		}
		token.ProjectVersion++
		return nil
	})
	if err != nil {
		return err
	}
	if err := synchronizeTokenCachesAfterUpdate(token); err != nil {
		return &TokenMutationCommittedError{Operation: "update", Cause: err}
	}
	return nil
}

// UpdateProjectTokenConnection changes only the connection policy. In particular,
// a quota settlement between the caller's read and this transaction must never
// be overwritten by the caller's older token snapshot.
func UpdateProjectTokenConnection(projectID, viewerID int, token *Token, product, vendorGroups, modelLanes bool) error {
	if token == nil || token.IsInternal() {
		return ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		current, err := CheckProjectTokenAction(tx, projectID, viewerID, token.Id, ProjectManagePersonalKey)
		if err != nil {
			return err
		}
		if current.ProjectVersion != token.ProjectVersion {
			return ErrProjectVersionConflict
		}
		columns := []string{}
		if product {
			current.Product = token.Product
			if err := current.NormalizeProductMetadata(); err != nil {
				return err
			}
			columns = append(columns, "product")
		}
		if vendorGroups {
			current.VendorGroups = token.VendorGroups.Normalize()
			columns = append(columns, "vendor_groups")
		}
		if modelLanes {
			current.ModelLanes = token.ModelLanes.Normalize()
			columns = append(columns, "model_lanes")
		}
		if len(columns) > 0 {
			current.ProjectVersion++
			columns = append(columns, "project_version")
			if err := tx.Model(&current).Select(columns).Updates(&current).Error; err != nil {
				return err
			}
		}
		// Return authoritative quota and status, not the caller's stale fields.
		return tx.First(token, current.Id).Error
	})
	if err != nil {
		return err
	}
	// Do not warm a quota snapshot captured before a concurrent settlement.
	// The next request fills the cache from the authoritative database.
	if common.RedisEnabled {
		if err := invalidateMutableTokenSnapshots(token); err != nil {
			return &TokenMutationCommittedError{Operation: "update", Cause: err}
		}
	}
	return nil
}

func DeleteProjectToken(projectID, viewerID, tokenID int) (Token, error) {
	var token Token
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		var err error
		token, err = CheckProjectTokenAction(tx, projectID, viewerID, tokenID, ProjectDeletePersonalKey)
		if err != nil {
			return err
		}
		return tx.Delete(&token).Error
	})
	if err != nil {
		return token, err
	}
	if err := synchronizeTokenCachesAfterDelete(&token); err != nil {
		return token, &TokenMutationCommittedError{Operation: "deletion", Cause: err}
	}
	return token, nil
}

func RevealProjectToken(projectID, viewerID, tokenID int) (string, string, error) {
	key, notification := "", "not_required"
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		token, err := CheckProjectTokenAction(tx, projectID, viewerID, tokenID, ProjectManagePersonalKey)
		if err != nil {
			return err
		}
		if !token.CanRevealKey() {
			return types.Err(types.ErrorCodeSecretUnavailable)
		}
		recipient := token.UserId
		if recipient != viewerID {
			var actor User
			if err := tx.Select("username").First(&actor, viewerID).Error; err != nil {
				return err
			}
			audit := TokenAuditLog{TokenID: token.Id, TokenPublicID: token.PublicID, UserID: viewerID, Username: actor.Username, Action: "token.reveal", BeforeState: "{}", AfterState: "{}"}
			if err := tx.Create(&audit).Error; err != nil {
				return err
			}
			var member ProjectMember
			if err := tx.Where("project_id = ? AND user_id = ?", projectID, recipient).First(&member).Error; err != nil {
				return err
			}
			if err := queueProjectMemberNoticeTx(tx, project, member, projectNotice{Kind: noticeKeyRevealed, ActorID: viewerID, TokenName: projectNoticeTokenName(token)}, "reveal:"+common.GeneratePublicID("event")); err != nil {
				return err
			}
			notification = "queued"
		}
		key = common.NormalizeAPIKey(token.Key)
		return nil
	})
	return key, notification, err
}

// The batch shares one authorization snapshot and transaction: a forbidden or
// foreign key rejects the entire batch, including keys authorized earlier.
func DeleteProjectTokens(projectID, viewerID int, ids []int) ([]Token, error) {
	var tokens []Token
	if len(ids) == 0 || len(ids) > 100 {
		return nil, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		seen := make(map[int]bool)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			token, err := CheckProjectTokenAction(tx, projectID, viewerID, id, ProjectDeletePersonalKey)
			if err != nil {
				return err
			}
			tokens = append(tokens, token)
		}
		for i := range tokens {
			if err := tx.Delete(&tokens[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var cacheErr error
	for i := range tokens {
		cacheErr = errors.Join(cacheErr, synchronizeTokenCachesAfterDelete(&tokens[i]))
	}
	if cacheErr != nil {
		return tokens, &TokenMutationCommittedError{Operation: "batch deletion", Cause: cacheErr}
	}
	return tokens, nil
}

type ProjectTokenCapUpdate struct{ Cap *billing.MicroUSD }
