package controller

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// 档位 (lanes) are managed from the 来源 page's 线路 dialog. They are stored
// as an option rather than a table: a handful of entries read on every request
// and cached in memory.

func laneView(lane setting.ChannelLane) gin.H {
	sources, _ := model.CountChannelsInLane(lane.Slug)
	priced, _ := model.CountPricedModelsInLane(lane.Slug)
	following, fixed, _ := model.CountSellPriceModesInLane(lane.Slug)
	return gin.H{
		"slug":         lane.Slug,
		"name":         lane.Name,
		"note":         lane.Note,
		"color":        lane.Color,
		"enabled":      lane.Enabled,
		"builtin":      lane.Builtin,
		"ever_enabled": lane.EverEnabled,
		"sort":         lane.Sort,
		"sources":      sources,
		"priced":       priced,
		"price_ratio":  setting.LanePriceRatio(lane.Slug),
		"following":    following,
		"fixed":        fixed,
	}
}

func persistChannelLanes() error {
	return model.UpdateOption("ChannelLanes", setting.ChannelLanes2JSONString())
}

func GetChannelLanes(c *gin.Context) {
	lanes := setting.AllChannelLanes()
	items := make([]gin.H, 0, len(lanes))
	for _, lane := range lanes {
		items = append(items, laneView(lane))
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

type channelLanePayload struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Note    string `json:"note"`
	Color   string `json:"color"`
	Sort    int    `json:"sort"`
	Enabled *bool  `json:"enabled"`
	// PriceRatio is the lane's default price, percent of the official price.
	PriceRatio float64 `json:"price_ratio"`
}

func SaveChannelLane(c *gin.Context) {
	var payload channelLanePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if slug := strings.TrimSpace(c.Param("slug")); slug != "" {
		payload.Slug = slug
	}
	before, _ := setting.LaneBySlug(payload.Slug)
	lane := setting.ChannelLane{
		Slug:       payload.Slug,
		Name:       payload.Name,
		Note:       payload.Note,
		Color:      payload.Color,
		Sort:       payload.Sort,
		PriceRatio: payload.PriceRatio,
	}
	if payload.Enabled != nil {
		lane.Enabled = *payload.Enabled
	} else {
		lane.Enabled = before.Enabled
	}
	saved, err := setting.UpsertChannelLane(lane)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := persistChannelLanes(); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.lane.save", "channel_lane", saved.Slug, saved.Name, before, saved)
	// Every model on this lane that follows the lane's ratio is repriced now.
	if setting.LanePriceRatio(saved.Slug) != before.PriceRatio {
		changes, err := model.RecomputeFollowingSellPrices(nil, []string{saved.Slug}, "lane ratio "+saved.Slug)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if len(changes) > 0 {
			model.WriteAdminAuditLog(c, "model_ops.lane.reprice", "channel_lane", saved.Slug, saved.Name, before.PriceRatio, len(changes))
			refreshModelMarketplaceCaches()
			InvalidateModelDiscoveryCache()
		}
	}
	common.ApiSuccess(c, laneView(saved))
}

type laneEnabledPayload struct {
	Enabled bool `json:"enabled"`
}

func SetChannelLaneEnabled(c *gin.Context) {
	var payload laneEnabledPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	slug := strings.TrimSpace(c.Param("slug"))
	before, ok := setting.LaneBySlug(slug)
	if !ok {
		httperr.Code(c, types.ErrorCodeLaneNotFound, types.D{"lane": slug})
		return
	}
	// Turning a lane on is what makes it routable, so refuse to do it while
	// nothing supplies it: an enabled lane with no sources silently drops every
	// request that asks for it.
	if payload.Enabled {
		sources, err := model.CountChannelsInLane(slug)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if sources == 0 {
			httperr.Code(c, types.ErrorCodeLaneHasNoSources, types.D{"lane": before.Slug})
			return
		}
	}
	lane, err := setting.SetChannelLaneEnabled(slug, payload.Enabled)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := persistChannelLanes(); err != nil {
		httperr.Write(c, err)
		return
	}
	action := "model_ops.lane.disable"
	if payload.Enabled {
		action = "model_ops.lane.enable"
	}
	model.WriteAdminAuditLog(c, action, "channel_lane", lane.Slug, lane.Name, before, lane)
	common.ApiSuccess(c, laneView(lane))
}

func DeleteChannelLane(c *gin.Context) {
	slug := strings.TrimSpace(c.Param("slug"))
	before, ok := setting.LaneBySlug(slug)
	if !ok {
		httperr.Code(c, types.ErrorCodeLaneNotFound, types.D{"lane": slug})
		return
	}
	referenced, err := model.ChannelLaneHasReferences(slug)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if referenced {
		httperr.Code(c, types.ErrorCodeLaneInUse, types.D{"lane": before.Slug})
		return
	}
	if err := setting.DeleteChannelLane(slug); err != nil {
		httperr.Write(c, err)
		return
	}
	if err := persistChannelLanes(); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.lane.delete", "channel_lane", slug, before.Name, before, nil)
	common.ApiSuccess(c, gin.H{"slug": slug})
}
