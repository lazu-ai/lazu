package model

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"gorm.io/gorm"
)

// RequestResourcePrincipal reads only server-written authentication context.
func RequestResourcePrincipal(c *gin.Context) ProjectPrincipal {
	if value, ok := c.Get("console_resource_principal"); ok {
		if principal, valid := value.(ProjectPrincipal); valid {
			return principal
		}
	}
	return ProjectPrincipal{
		ProjectID:   common.GetContextKeyInt(c, constant.ContextKeyProjectID),
		PayerUserID: common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
		ActorUserID: c.GetInt("id"),
		TokenID:     c.GetInt("token_id"),
	}
}

// API resources belong to a human within a project, and to the logical Key that
// produced them. Actor 0 is never an identity: a principal without one owns
// nothing, whether or not it carries a token id.
func requestResourceScope(tx *gorm.DB, userID int, principals []ProjectPrincipal) *gorm.DB {
	if len(principals) == 0 || principals[0].ProjectID <= 0 {
		if userID <= 0 {
			return tx.Where("1 = 0")
		}
		return tx.Where("user_id = ?", userID)
	}
	principal := principals[0]
	tx = tx.Where("project_id = ?", principal.ProjectID)
	if principal.ConsoleReadAll {
		return tx
	}
	if principal.TokenID > 0 {
		// An API-Key request reaches the resources of its own logical Key only.
		// Rotation reuses the same tokens row, so the token id is the logical Key
		// identity and a rotated credential keeps its own history; another Key of
		// the same person, and a console upload that never carried a token id,
		// both stay out of reach.
		//
		// An internal worker settling an in-flight job replays the principal the
		// job was accepted with, so it lands on the same pair and reaches exactly
		// the rows that job produced.
		if principal.ActorUserID <= 0 {
			return tx.Where("1 = 0")
		}
		return tx.Where("token_id = ? AND actor_user_id = ?", principal.TokenID, principal.ActorUserID)
	}
	// A console principal carries no token id: an active member reads its own
	// rows across the project, whichever Key (or none) produced them.
	if principal.ActorUserID <= 0 {
		return tx.Where("1 = 0")
	}
	return tx.Where("actor_user_id = ?", principal.ActorUserID)
}

// captureLegacyResourceIdentity supports trusted existing constructors while
// the HTTP adapters migrate. A supplied token's project is authoritative.
func captureLegacyResourceIdentity(tx *gorm.DB, userID int, tokenID *int) (ProjectPrincipal, error) {
	if !tx.Migrator().HasTable(&Project{}) {
		return ProjectPrincipal{ActorUserID: userID, PayerUserID: userID}, nil
	}
	var project Project
	principal := ProjectPrincipal{ActorUserID: userID}
	if tokenID != nil && *tokenID > 0 {
		var token Token
		if err := tx.Select("id", "user_id", "project_id").First(&token, *tokenID).Error; err != nil {
			return principal, err
		}
		if token.UserId != userID {
			return principal, ErrProjectForbidden
		}
		principal.TokenID = token.Id
		if token.ProjectID <= 0 {
			return principal, ErrProjectForbidden
		}
		if err := tx.First(&project, token.ProjectID).Error; err != nil {
			return principal, err
		}
	} else {
		if userID <= 0 {
			return principal, ErrProjectForbidden
		}
		if err := tx.Where("owner_user_id = ?", userID).First(&project).Error; err != nil {
			return principal, err
		}
	}
	principal.ProjectID, principal.PayerUserID = project.ID, project.OwnerUserID
	return principal, nil
}

func (job MediaJob) ResourcePrincipal() ProjectPrincipal {
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	return ProjectPrincipal{ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(), TokenID: tokenID}
}
