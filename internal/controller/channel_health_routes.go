package controller

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

type channelHealthRoleRequest struct {
	ChannelID string `json:"channel_id"`
	Lane      string `json:"lane"`
	Model     string `json:"model"`
	Role      string `json:"role"`
}

// UpdateChannelHealthRouteRole moves one upstream between carrying a route,
// standing by for it, and sitting in inventory.
//
// This is the one control that decides what a source is FOR, as opposed to how
// much of the load it takes, which stays with weight. Keeping them apart is
// what lets an operator hold spare capacity that is measured but not used:
// weight zero still serves requests, because the picker adds a floor to every
// candidate, so "weight zero" has never meant "off" and cannot be made to.
func UpdateChannelHealthRouteRole(c *gin.Context) {
	var req channelHealthRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if !model.IsValidAbilityRole(role) {
		httperr.Invalid(c, "role")
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	lane := model.NormalizeChannelLane(req.Lane)
	if !model.IsValidChannelLane(lane) {
		httperr.Code(c, types.ErrorCodeChannelLaneInvalid, types.D{"lane": req.Lane})
		return
	}
	channelID, err := resolveChannelID(req.ChannelID)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// The health page folds aliased variants of one model on one upstream into
	// a single source, so the role belongs to every routed name that resolves
	// to the same model there. Updating only the name the page happened to
	// carry would leave the other variants routable after the operator parked
	// the source.
	var candidates []model.Ability
	if err := model.CoreDB().
		Where("channel_id = ?", channelID).
		Where(modelOpsAbilityGroupColumn()+" = ?", lane).
		Find(&candidates).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	canonical := model.NormalizePricingModelName(modelName)
	var routed []string
	before := ""
	for _, ability := range candidates {
		if ability.Model != modelName && model.NormalizePricingModelName(ability.Model) != canonical {
			continue
		}
		routed = append(routed, ability.Model)
		if before == "" || ability.Model == modelName {
			before = model.NormalizeAbilityRole(ability.Role)
		}
	}
	routeNotFound := types.D{"channel_id": req.ChannelID, "model": modelName, "lane": lane}
	if len(routed) == 0 {
		httperr.Code(c, types.ErrorCodeChannelRouteNotFound, routeNotFound)
		return
	}
	// A route under observation goes live only once it has earned it; that
	// is the point of watching it. Parking one again is always allowed.
	if role != model.AbilityRoleInventory {
		if err := requireObservationPassed(channelID, req.ChannelID, lane, modelName, routed); err != nil {
			httperr.Write(c, err)
			return
		}
	}
	result := model.CoreDB().Model(&model.Ability{}).
		Where("model IN ? AND channel_id = ?", routed, channelID).
		Where(modelOpsAbilityGroupColumn()+" = ?", lane).
		Update("role", role)
	if result.Error != nil {
		httperr.Write(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		httperr.Code(c, types.ErrorCodeChannelRouteNotFound, routeNotFound)
		return
	}
	if role != model.AbilityRoleInventory {
		if err := model.EndRouteObservations(nil, channelID, lane, routed); err != nil {
			common.SysLog("route observation: failed to end observation: " + err.Error())
		}
	}
	// Moving a source between roles changes what we will serve and from where,
	// which is exactly the kind of decision an incident review has to be able
	// to reconstruct.
	model.WriteAdminAuditLog(c, "channel_health.route_role.update", "ability",
		fmt.Sprintf("%s|%s|%d", lane, modelName, channelID), modelName,
		gin.H{"role": before}, gin.H{"role": role, "models": routed})
	// Roles are baked into the cached routing priorities, so the change is not
	// live until the cache is rebuilt.
	model.InitChannelCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"model": modelName, "models": routed, "lane": lane, "role": role})
}

// ClearChannelHealthRouteRetreat puts a route the breaker pulled back into
// rotation immediately.
//
// A retreat expires on its own, so this exists for the case where the operator
// knows something the gateway cannot: they have just fixed the key, or topped
// up the balance, and would rather not wait out a timer that is measuring a
// problem which no longer exists.
func ClearChannelHealthRouteRetreat(c *gin.Context) {
	var req channelHealthRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	lane := model.NormalizeChannelLane(req.Lane)
	if !model.IsValidChannelLane(lane) {
		httperr.Code(c, types.ErrorCodeChannelLaneInvalid, types.D{"lane": req.Lane})
		return
	}
	channelID, err := resolveChannelID(req.ChannelID)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.ClearRouteHealthRetreat(modelName, lane, channelID); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel_health.route_retreat.clear", "ability",
		fmt.Sprintf("%s|%s|%d", lane, modelName, channelID), modelName, nil, nil)
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"model": modelName, "lane": lane, "channel_id": channelID})
}
