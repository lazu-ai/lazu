package controller

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// GetAllVendors 获取供应商列表（分页）
func GetAllVendors(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	vendors, err := model.GetAllVendors(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var total int64
	model.CoreDB().
		Model(&model.Vendor{}).Count(&total)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(vendors)
	common.ApiSuccess(c, pageInfo)
}

// SearchVendors 搜索供应商
func SearchVendors(c *gin.Context) {
	keyword := c.Query("keyword")
	pageInfo := common.GetPageQuery(c)
	vendors, total, err := model.SearchVendors(keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(vendors)
	common.ApiSuccess(c, pageInfo)
}

// GetVendorMeta 根据 ID 获取供应商
func GetVendorMeta(c *gin.Context) {
	id, err := resolveIDParam("vendors", c.Param("id"))
	if err != nil {
		writeLookupError(c, err, "vendor")
		return
	}
	v, err := model.GetVendorByID(id)
	if err != nil {
		writeLookupError(c, err, "vendor")
		return
	}
	common.ApiSuccess(c, v)
}

// CreateVendorMeta 新建供应商
func CreateVendorMeta(c *gin.Context) {
	var v model.Vendor
	if err := c.ShouldBindJSON(&v); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if strings.TrimSpace(v.Name) == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "name"})
		return
	}
	// 创建前先检查名称
	if dup, err := model.IsVendorNameDuplicated(0, v.Name); err != nil {
		httperr.Write(c, err)
		return
	} else if dup {
		httperr.Code(c, types.ErrorCodeVendorNameTaken, types.D{"vendor": v.Name})
		return
	}

	if err := v.Insert(); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	common.ApiSuccess(c, &v)
}

// UpdateVendorMeta 更新供应商
func UpdateVendorMeta(c *gin.Context) {
	var v model.Vendor
	if err := c.ShouldBindJSON(&v); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if v.Id == 0 && v.PublicID != "" {
		var err error
		v.Id, err = resolveIDParam("vendors", v.PublicID)
		if err != nil {
			writeLookupError(c, err, "vendor")
			return
		}
	}
	if v.Id == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "id"})
		return
	}
	// 名称冲突检查
	if dup, err := model.IsVendorNameDuplicated(v.Id, v.Name); err != nil {
		httperr.Write(c, err)
		return
	} else if dup {
		httperr.Code(c, types.ErrorCodeVendorNameTaken, types.D{"vendor": v.Name})
		return
	}

	if err := v.Update(); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	common.ApiSuccess(c, &v)
}

// DeleteVendorMeta 删除供应商
func DeleteVendorMeta(c *gin.Context) {
	id, err := resolveIDParam("vendors", c.Param("id"))
	if err != nil {
		writeLookupError(c, err, "vendor")
		return
	}
	if err := model.CoreDB().Delete(&model.Vendor{}, id).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	common.ApiSuccess(c, nil)
}
