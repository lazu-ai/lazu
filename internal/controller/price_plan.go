package controller

import (
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"

	"github.com/gin-gonic/gin"
)

type pricePlanPayload struct {
	Name       string                `json:"name"`
	Note       string                `json:"note"`
	DefaultPct *float64              `json:"default_pct"`
	Lanes      string                `json:"lanes"`
	StartsAt   *time.Time            `json:"starts_at"`
	EndsAt     *time.Time            `json:"ends_at"`
	Active     *bool                 `json:"active"`
	Rules      []model.PricePlanRule `json:"rules"`
}

func (payload pricePlanPayload) toModel(id int) model.PricePlan {
	active := true
	if payload.Active != nil {
		active = *payload.Active
	}
	return model.PricePlan{
		ID:         id,
		Name:       payload.Name,
		Note:       payload.Note,
		DefaultPct: payload.DefaultPct,
		Lanes:      payload.Lanes,
		StartsAt:   payload.StartsAt,
		EndsAt:     payload.EndsAt,
		Active:     active,
	}
}

func pricePlanIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		httperr.Invalid(c, "id")
		return 0, false
	}
	return id, true
}

func ListPricePlans(c *gin.Context) {
	plans, err := model.ListPricePlans()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, plans)
}

func GetPricePlan(c *gin.Context) {
	id, ok := pricePlanIDParam(c)
	if !ok {
		return
	}
	detail, err := model.GetPricePlanDetail(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	members, err := model.ListPricePlanMembers(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"plan": detail, "members": members})
}

func CreatePricePlan(c *gin.Context) {
	var payload pricePlanPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	detail, err := model.SavePricePlan(payload.toModel(0), payload.Rules)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "price_plan.create", "price_plan", strconv.Itoa(detail.ID), detail.Name, nil, detail)
	common.ApiSuccess(c, detail)
}

func UpdatePricePlan(c *gin.Context) {
	id, ok := pricePlanIDParam(c)
	if !ok {
		return
	}
	var payload pricePlanPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	before, err := model.GetPricePlanDetail(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	detail, err := model.SavePricePlan(payload.toModel(id), payload.Rules)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "price_plan.update", "price_plan", strconv.Itoa(id), detail.Name, before, detail)
	common.ApiSuccess(c, detail)
}

func DeletePricePlan(c *gin.Context) {
	id, ok := pricePlanIDParam(c)
	if !ok {
		return
	}
	before, err := model.GetPricePlanDetail(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.DeletePricePlan(id); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "price_plan.delete", "price_plan", strconv.Itoa(id), before.Name, before, nil)
	common.ApiSuccess(c, nil)
}

type pricePlanMemberPayload struct {
	// User is a public id, internal id or username.
	User string `json:"user"`
}

func resolvePlanMember(identifier string) (int, error) {
	identifier = strings.TrimSpace(identifier)
	if id, err := model.ResolvePublicIntID("users", identifier); err == nil {
		return id, nil
	}
	var user model.User
	if err := model.CoreDB().Select("id").Where("username = ?", identifier).First(&user).Error; err != nil {
		return 0, err
	}
	return user.Id, nil
}

func AddPricePlanMember(c *gin.Context) {
	id, ok := pricePlanIDParam(c)
	if !ok {
		return
	}
	var payload pricePlanMemberPayload
	if err := c.ShouldBindJSON(&payload); err != nil || strings.TrimSpace(payload.User) == "" {
		httperr.Invalid(c, "user")
		return
	}
	userID, err := resolvePlanMember(payload.User)
	if err != nil {
		httperr.Invalid(c, "user")
		return
	}
	setUserPricePlanAudited(c, userID, id, 0)
}

func RemovePricePlanMember(c *gin.Context) {
	id, ok := pricePlanIDParam(c)
	if !ok {
		return
	}
	userID, err := resolvePlanMember(c.Param("user"))
	if err != nil {
		httperr.Invalid(c, "user")
		return
	}
	setUserPricePlanAudited(c, userID, 0, id)
}

func setUserPricePlanAudited(c *gin.Context, userID, planID, expectedPlanID int) {
	var before model.User
	if err := model.CoreDB().Select("id", "public_id", "username", "price_plan_id").First(&before, userID).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	var err error
	if expectedPlanID > 0 {
		err = model.RemoveUserPricePlan(userID, expectedPlanID)
	} else {
		err = model.SetUserPricePlan(userID, planID)
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "user.price_plan.update", "user", before.PublicID, before.Username,
		gin.H{"price_plan_id": before.PricePlanID}, gin.H{"price_plan_id": planID})
	common.ApiSuccess(c, nil)
}

// pricePlanView is what a customer sees of their own plan: enough to show
// the discount on the model page and receipts, nothing about other members.
type pricePlanView struct {
	ID         int                 `json:"id"`
	Name       string              `json:"name"`
	DefaultPct *float64            `json:"default_pct"`
	Lanes      string              `json:"lanes"`
	StartsAt   *time.Time          `json:"starts_at"`
	EndsAt     *time.Time          `json:"ends_at"`
	Live       bool                `json:"live"`
	Rules      []pricePlanRuleView `json:"rules"`
}

type pricePlanRuleView struct {
	Kind       string  `json:"kind"`
	VendorID   string  `json:"vendor_id,omitempty"`
	VendorName string  `json:"vendor_name,omitempty"`
	ModelName  string  `json:"model_name,omitempty"`
	Pct        float64 `json:"pct"`
}

func pricePlanViewFor(planID int) *pricePlanView {
	if planID <= 0 {
		return nil
	}
	detail, err := model.GetPricePlanDetail(planID)
	if err != nil {
		return nil
	}
	now := time.Now()
	live := detail.Active &&
		(detail.StartsAt == nil || !now.Before(*detail.StartsAt)) &&
		(detail.EndsAt == nil || now.Before(*detail.EndsAt))
	view := &pricePlanView{
		ID:         detail.ID,
		Name:       detail.Name,
		DefaultPct: detail.DefaultPct,
		Lanes:      detail.Lanes,
		StartsAt:   detail.StartsAt,
		EndsAt:     detail.EndsAt,
		Live:       live,
		Rules:      make([]pricePlanRuleView, 0, len(detail.Rules)),
	}
	for _, rule := range detail.Rules {
		view.Rules = append(view.Rules, pricePlanRuleView{
			Kind:       rule.Kind,
			VendorID:   rule.Vendor,
			VendorName: rule.VendorName,
			ModelName:  rule.ModelName,
			Pct:        rule.Pct,
		})
	}
	return view
}
