package controller

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// writeLookupError answers a failed single-row lookup: a missing row names
// the resource, anything else is logged and answered as internal_error.
func writeLookupError(c *gin.Context, err error, resource string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httperr.Code(c, types.ErrorCodeResourceNotFound, types.D{"resource": resource})
		return
	}
	httperr.Write(c, err)
}

// GetAllModelsMeta 获取模型列表（分页）
func GetAllModelsMeta(c *gin.Context) {

	pageInfo := common.GetPageQuery(c)
	sortBy := c.DefaultQuery("sort", model.DefaultModelSort)
	modelsMeta, err := model.GetAllModels(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), sortBy)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// 批量填充附加字段，提升列表接口性能
	enrichModels(modelsMeta)
	var total int64
	model.CoreDB().
		Model(&model.Model{}).Count(&total)

	// 统计供应商计数（全部数据，不受分页影响）
	vendorCounts, _ := model.GetVendorModelCounts()

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(modelsMeta)
	common.ApiSuccess(c, gin.H{
		"items":         modelsMeta,
		"total":         total,
		"page":          pageInfo.GetPage(),
		"page_size":     pageInfo.GetPageSize(),
		"vendor_counts": vendorCounts,
	})
}

// SearchModelsMeta 搜索模型列表
func SearchModelsMeta(c *gin.Context) {

	keyword := c.Query("keyword")
	vendor := c.Query("vendor")
	status := c.Query("status")
	tier := c.Query("tier")
	sortBy := c.DefaultQuery("sort", model.DefaultModelSort)
	pageInfo := common.GetPageQuery(c)

	modelsMeta, total, err := model.SearchModelsWithFilters(keyword, vendor, status, tier, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), sortBy)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// 批量填充附加字段，提升列表接口性能
	enrichModels(modelsMeta)
	vendorCounts, _ := model.GetVendorModelCounts()
	common.ApiSuccess(c, gin.H{
		"items":         modelsMeta,
		"total":         total,
		"page":          pageInfo.GetPage(),
		"page_size":     pageInfo.GetPageSize(),
		"vendor_counts": vendorCounts,
	})
}

// GetModelMeta 根据 ID 获取单条模型信息
func GetModelMeta(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httperr.Invalid(c, "id")
		return
	}
	var m model.Model
	if err := model.CoreDB().First(&m, id).Error; err != nil {
		writeLookupError(c, err, "model")
		return
	}
	enrichModels([]*model.Model{&m})
	common.ApiSuccess(c, &m)
}

// CreateModelMeta 新建模型
func CreateModelMeta(c *gin.Context) {
	raw, err := c.GetRawData()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var m model.Model
	if err := common.Unmarshal(raw, &m); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if m.ModelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model_name"})
		return
	}
	// 名称冲突检查
	if dup, err := model.IsModelNameDuplicated(0, m.ModelName); err != nil {
		httperr.Write(c, err)
		return
	} else if dup {
		httperr.Code(c, types.ErrorCodeModelNameTaken, types.D{"model": m.ModelName})
		return
	}

	if err := model.NormalizeModelInfo(&m); err != nil {
		httperr.Invalid(c, "model_info")
		return
	}

	// Every model fact typed into the registration form is the operator's;
	// the form marks the ones it prefilled from models.dev.
	prefilled, manual := parseInfoFieldsFromModels(raw)
	m.InfoSources = model.RegistrationInfoSources(m, prefilled, manual)
	if err := m.Insert(); err != nil {
		httperr.Write(c, err)
		return
	}
	// The release date is the model's own (or blank), never the day it was
	// listed here: listing day in that field is what scrambled the catalog order.
	_ = model.ResolvePendingModelInfoReviews(model.ModelInfoReviewNewRelease, m.ModelName, "")

	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	common.ApiSuccess(c, &m)
}

// UpdateModelMeta 更新模型
func UpdateModelMeta(c *gin.Context) {
	statusOnly := c.Query("status_only") == "true"

	raw, err := c.GetRawData()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var m model.Model
	if err := common.Unmarshal(raw, &m); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if m.Id == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "id"})
		return
	}

	if statusOnly {
		// 只更新状态，防止误清空其他字段
		if err := model.CoreDB().Model(&model.Model{}).Where("id = ?", m.Id).Update("status", m.Status).Error; err != nil {
			httperr.Write(c, err)
			return
		}
	} else {
		// 名称冲突检查
		if dup, err := model.IsModelNameDuplicated(m.Id, m.ModelName); err != nil {
			httperr.Write(c, err)
			return
		} else if dup {
			httperr.Code(c, types.ErrorCodeModelNameTaken, types.D{"model": m.ModelName})
			return
		}

		if err := model.NormalizeModelInfo(&m); err != nil {
			httperr.Invalid(c, "model_info")
			return
		}
		var fields map[string]json.RawMessage
		_ = common.Unmarshal(raw, &fields)
		if err := m.UpdateWithModelInfo(fields); err != nil {
			writeLookupError(c, err, "model")
			return
		}
		// An edited official price reprices every lane that follows it.
		model.RecomputeFollowingSellPricesLogged([]string{m.ModelName}, nil, "model edit")
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	common.ApiSuccess(c, &m)
}

// DeleteModelMeta 删除模型
func DeleteModelMeta(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httperr.Invalid(c, "id")
		return
	}
	if err := model.CoreDB().Delete(&model.Model{}, id).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	common.ApiSuccess(c, nil)
}

// enrichModels 批量填充附加信息：端点、渠道、分组、计费类型，避免 N+1 查询
func enrichModels(models []*model.Model) {
	if len(models) == 0 {
		return
	}
	model.EnrichModelSellPrices(models)
	model.ResolveModelMakers(models)

	// 1) 拆分精确与规则匹配
	exactNames := make([]string, 0)
	exactIdx := make(map[string][]int) // modelName -> indices in models
	ruleIndices := make([]int, 0)
	for i, m := range models {
		if m == nil {
			continue
		}
		if m.NameRule == model.NameRuleExact {
			exactNames = append(exactNames, m.ModelName)
			exactIdx[m.ModelName] = append(exactIdx[m.ModelName], i)
		} else {
			ruleIndices = append(ruleIndices, i)
		}
	}

	// 2) 批量查询精确模型的绑定渠道
	channelsByModel, _ := model.GetBoundChannelsByModelsMap(exactNames)

	// 3) 精确模型：端点从缓存、渠道批量映射、分组/计费类型从缓存
	for name, indices := range exactIdx {
		chs := channelsByModel[name]
		for _, idx := range indices {
			mm := models[idx]
			if mm.Endpoints == "" {
				eps := model.GetModelSupportEndpointTypes(mm.ModelName)
				if b, err := json.Marshal(eps); err == nil {
					mm.Endpoints = string(b)
				}
			}
			mm.BoundChannels = chs
			mm.EnableGroups = model.GetModelEnableGroups(mm.ModelName)
			mm.QuotaTypes = model.GetModelQuotaTypes(mm.ModelName)
		}
	}

	if len(ruleIndices) == 0 {
		return
	}

	// 4) 一次性读取定价缓存，内存匹配所有规则模型
	pricings := model.GetPricing()

	// 为全部规则模型收集匹配名集合、端点并集、分组并集、配额集合
	matchedNamesByIdx := make(map[int][]string)
	endpointSetByIdx := make(map[int]map[constant.EndpointType]struct{})
	groupSetByIdx := make(map[int]map[string]struct{})
	quotaSetByIdx := make(map[int]map[int]struct{})

	for _, p := range pricings {
		for _, idx := range ruleIndices {
			mm := models[idx]
			var matched bool
			switch mm.NameRule {
			case model.NameRulePrefix:
				matched = strings.HasPrefix(p.ModelName, mm.ModelName)
			case model.NameRuleSuffix:
				matched = strings.HasSuffix(p.ModelName, mm.ModelName)
			case model.NameRuleContains:
				matched = strings.Contains(p.ModelName, mm.ModelName)
			}
			if !matched {
				continue
			}
			matchedNamesByIdx[idx] = append(matchedNamesByIdx[idx], p.ModelName)

			es := endpointSetByIdx[idx]
			if es == nil {
				es = make(map[constant.EndpointType]struct{})
				endpointSetByIdx[idx] = es
			}
			for _, et := range p.SupportedEndpointTypes {
				es[et] = struct{}{}
			}

			gs := groupSetByIdx[idx]
			if gs == nil {
				gs = make(map[string]struct{})
				groupSetByIdx[idx] = gs
			}
			for _, g := range p.EnableGroup {
				gs[g] = struct{}{}
			}

			qs := quotaSetByIdx[idx]
			if qs == nil {
				qs = make(map[int]struct{})
				quotaSetByIdx[idx] = qs
			}
			qs[p.QuotaType] = struct{}{}
		}
	}

	// 5) 汇总所有匹配到的模型名称，批量查询一次渠道
	allMatchedSet := make(map[string]struct{})
	for _, names := range matchedNamesByIdx {
		for _, n := range names {
			allMatchedSet[n] = struct{}{}
		}
	}
	allMatched := make([]string, 0, len(allMatchedSet))
	for n := range allMatchedSet {
		allMatched = append(allMatched, n)
	}
	matchedChannelsByModel, _ := model.GetBoundChannelsByModelsMap(allMatched)

	// 6) 回填每个规则模型的并集信息
	for _, idx := range ruleIndices {
		mm := models[idx]

		// 端点并集 -> 序列化
		if es, ok := endpointSetByIdx[idx]; ok && mm.Endpoints == "" {
			eps := make([]constant.EndpointType, 0, len(es))
			for et := range es {
				eps = append(eps, et)
			}
			if b, err := json.Marshal(eps); err == nil {
				mm.Endpoints = string(b)
			}
		}

		// 分组并集
		if gs, ok := groupSetByIdx[idx]; ok {
			groups := make([]string, 0, len(gs))
			for g := range gs {
				groups = append(groups, g)
			}
			mm.EnableGroups = groups
		}

		// 配额类型集合（保持去重并排序）
		if qs, ok := quotaSetByIdx[idx]; ok {
			arr := make([]int, 0, len(qs))
			for k := range qs {
				arr = append(arr, k)
			}
			sort.Ints(arr)
			mm.QuotaTypes = arr
		}

		// 渠道并集
		names := matchedNamesByIdx[idx]
		channelSet := make(map[string]model.BoundChannel)
		for _, n := range names {
			for _, ch := range matchedChannelsByModel[n] {
				key := ch.Name + "_" + strconv.Itoa(ch.Type)
				channelSet[key] = ch
			}
		}
		if len(channelSet) > 0 {
			chs := make([]model.BoundChannel, 0, len(channelSet))
			for _, ch := range channelSet {
				chs = append(chs, ch)
			}
			mm.BoundChannels = chs
		}

		// 匹配信息
		mm.MatchedModels = names
		mm.MatchedCount = len(names)
	}
}

// parseInfoFieldsFromModels reads the prefill marker the registration form
// sends: {"prefilled_fields": ["release_date", ...]}.
func parseInfoFieldsFromModels(raw []byte) ([]string, []string) {
	var body struct {
		PrefilledFields []string `json:"prefilled_fields"`
		ManualFields    []string `json:"manual_fields"`
	}
	_ = common.Unmarshal(raw, &body)
	return body.PrefilledFields, body.ManualFields
}
