package controller

import (
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// GetPrefillGroups 获取预填组列表，可通过 ?type=xxx 过滤
func GetPrefillGroups(c *gin.Context) {
	groupType := c.Query("type")
	groups, err := model.GetAllPrefillGroups(groupType)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, groups)
}

// CreatePrefillGroup 创建新的预填组
func CreatePrefillGroup(c *gin.Context) {
	var g model.PrefillGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if strings.TrimSpace(g.Name) == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "name"})
		return
	}
	if strings.TrimSpace(g.Type) == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "type"})
		return
	}
	// 创建前检查名称
	if dup, err := model.IsPrefillGroupNameDuplicated(0, g.Name); err != nil {
		httperr.Write(c, err)
		return
	} else if dup {
		httperr.Code(c, types.ErrorCodePrefillGroupNameTaken, types.D{"name": g.Name})
		return
	}

	if err := g.Insert(); err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, &g)
}

// UpdatePrefillGroup 更新预填组
func UpdatePrefillGroup(c *gin.Context) {
	var g model.PrefillGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if g.Id == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "id"})
		return
	}
	// 名称冲突检查
	if dup, err := model.IsPrefillGroupNameDuplicated(g.Id, g.Name); err != nil {
		httperr.Write(c, err)
		return
	} else if dup {
		httperr.Code(c, types.ErrorCodePrefillGroupNameTaken, types.D{"name": g.Name})
		return
	}

	if err := g.Update(); err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, &g)
}

// DeletePrefillGroup 删除预填组
func DeletePrefillGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httperr.Invalid(c, "id")
		return
	}
	if err := model.DeletePrefillGroupByID(id); err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
