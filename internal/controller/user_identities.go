package controller

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

type linkedIdentity struct {
	Email     string `json:"email"`
	LinkedAt  int64  `json:"linked_at"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Removable bool   `json:"removable"`
}

// listUserIdentities lists the identities an account signs in with. A linked
// Telegram account is a contact channel, not a sign-in method, so it is left
// out.
func listUserIdentities(userID int) ([]model.UserIdentity, error) {
	var identities []model.UserIdentity
	if err := model.CoreDB().Where("user_id = ? AND provider <> ?", userID, model.IdentityProviderTelegram).Order("created_at asc").Find(&identities).Error; err != nil {
		return nil, err
	}
	return identities, nil
}

// linkedProviderLabel names the provider a social-only account signs in with,
// so mail to that user talks about the button they actually press.
func linkedProviderLabel(userID int) string {
	identities, err := listUserIdentities(userID)
	if err != nil || len(identities) == 0 {
		return "第三方账号"
	}
	labels := make([]string, 0, len(identities))
	for _, identity := range identities {
		labels = append(labels, socialProviderLabel(identity.Provider))
	}
	return strings.Join(labels, " / ")
}

// GetSelfIdentities lists the sign-in methods linked to the current account.
func GetSelfIdentities(c *gin.Context) {
	userID := c.GetInt("id")
	user, err := model.GetUserById(userID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	identities, err := listUserIdentities(userID)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	hasPassword := !user.GetSetting().NeedsPassword
	rows := make([]linkedIdentity, 0, len(identities))
	for _, identity := range identities {
		rows = append(rows, linkedIdentity{
			Email:    identity.Email,
			LinkedAt: identity.CreatedAt.Unix(),
			Provider: identity.Provider,
			Label:    socialProviderLabel(identity.Provider),
			// All current external-auth resolvers can discover an existing account by
			// verified email. Deleting only this row would therefore advertise a
			// revocation that the next provider callback silently undoes. Keep every
			// identity read-only until an explicit link flow and durable unlink
			// tombstone exist.
			Removable: false,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"has_password": hasPassword,
			"identities":   rows,
		},
	})
}

// DeleteSelfIdentity intentionally keeps external identities read-only. Google,
// GitHub, and generic OIDC all support verified-email account discovery, so
// deleting an identity row alone does not revoke provider access: the next
// callback would recreate it. A real unlink requires an authenticated link flow
// plus a durable tombstone that the resolver honors.
func DeleteSelfIdentity(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	if provider == "" {
		httperr.Invalid(c, "provider")
		return
	}
	httperr.Code(c, types.ErrorCodeIdentityUnlinkUnavailable)
}
