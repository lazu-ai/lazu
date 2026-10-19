package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
)

type accessAddressView struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	UserIDs   []int  `json:"user_ids"`
	UserCount int    `json:"user_count"`
}

func accessAddressViews(list []model.AccessAddress) []accessAddressView {
	out := make([]accessAddressView, 0, len(list))
	for _, item := range list {
		ids := item.UserIDs
		if ids == nil {
			ids = []int{}
		}
		out = append(out, accessAddressView{URL: item.URL, Kind: item.Kind, UserIDs: ids, UserCount: len(ids)})
	}
	return out
}

func writeAccessAddressError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAccessAddressInvalidURL), errors.Is(err, model.ErrAccessAddressDuplicate):
		httperr.Invalid(c, "url")
	case errors.Is(err, model.ErrAccessAddressInvalidKind):
		httperr.Invalid(c, "kind")
	case errors.Is(err, model.ErrAccessAddressNotFound):
		httperr.Invalid(c, "url")
	default:
		httperr.Write(c, err)
	}
}

// GetAccessAddresses lists the extra API addresses and who may use each.
func GetAccessAddresses(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": accessAddressViews(model.AccessAddresses())})
}

// UpdateAccessAddresses replaces the list of addresses and their kinds. The
// accounts already opened to an address that stays are kept.
func UpdateAccessAddresses(c *gin.Context) {
	var payload struct {
		Addresses []struct {
			URL  string `json:"url"`
			Kind string `json:"kind"`
		} `json:"addresses"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	before := model.AccessAddresses()
	next := make([]model.AccessAddress, 0, len(payload.Addresses))
	for _, item := range payload.Addresses {
		next = append(next, model.AccessAddress{URL: item.URL, Kind: item.Kind})
	}
	saved, err := model.ReplaceAccessAddresses(next)
	if err != nil {
		writeAccessAddressError(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "access_address.update", "option", model.AccessAddressesOptionKey, model.AccessAddressesOptionKey, before, saved)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": accessAddressViews(saved)})
}

// SetAccessAddressGrant opens or closes one address for one account.
func SetAccessAddressGrant(c *gin.Context) {
	var payload struct {
		URL     string `json:"url"`
		UserID  string `json:"user_id"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	userID, err := resolveUserID(payload.UserID)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	before := model.AccessAddresses()
	saved, err := model.SetAccessAddressGrant(payload.URL, userID, payload.Enabled)
	if err != nil {
		writeAccessAddressError(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "access_address.grant", "user", user.PublicID, user.Username, before, saved)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": accessAddressViews(saved)})
}

type selfAccessAddress struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// selfAccessAddresses lists the extra addresses an account may call, without
// the other accounts each one is opened to.
func selfAccessAddresses(userID int) []selfAccessAddress {
	list := model.AccessAddressesForUser(userID)
	out := make([]selfAccessAddress, 0, len(list))
	for _, item := range list {
		// Only api.* hosts are offered; a web domain registered just to be
		// gated would read as a second address to switch to.
		if !model.IsAPIAccessAddress(item.URL) {
			continue
		}
		out = append(out, selfAccessAddress{URL: item.URL, Kind: item.Kind})
	}
	return out
}
