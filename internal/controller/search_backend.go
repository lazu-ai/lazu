package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ListSearchBackends(c *gin.Context) {
	var channels []*model.Channel
	if err := model.CoreDB().
		Where("type IN ?", constant.SearchChannelTypes()).
		Order("priority desc, id desc").
		Find(&channels).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.DecryptChannelKeysForUse(channels); err != nil {
		httperr.Write(c, err)
		return
	}
	records := make([]dto.SearchBackendRecord, 0, len(channels))
	for _, channel := range channels {
		records = append(records, searchBackendRecordFromChannel(channel, false))
	}
	common.ApiSuccess(c, records)
}

func CreateSearchBackend(c *gin.Context) {
	var req dto.SearchBackendRecord
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	channel, err := searchBackendChannelFromRecord(req, nil)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := channel.Insert(); err != nil {
		httperr.Write(c, err)
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, searchBackendRecordFromChannel(channel, false))
}

func UpdateSearchBackend(c *gin.Context) {
	channel, ok := loadSearchBackend(c)
	if !ok {
		return
	}
	var req dto.SearchBackendRecord
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	updated, err := searchBackendChannelFromRecord(req, channel)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := updated.Update(); err != nil {
		httperr.Write(c, err)
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, searchBackendRecordFromChannel(updated, false))
}

func DeleteSearchBackend(c *gin.Context) {
	channel, ok := loadSearchBackend(c)
	if !ok {
		return
	}
	if err := channel.Delete(); err != nil {
		httperr.Write(c, err)
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, nil)
}

// loadSearchBackend resolves the :id route parameter to a search channel. A
// channel of any other type is not a search backend, so it is not found here.
func loadSearchBackend(c *gin.Context) (*model.Channel, bool) {
	id, err := resolveIDParam("channels", c.Param("id"))
	if err != nil {
		writeSearchBackendLookupError(c, err)
		return nil, false
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		writeSearchBackendLookupError(c, err)
		return nil, false
	}
	if !constant.IsSearchChannelType(channel.Type) {
		httperr.Code(c, types.ErrorCodeSearchBackendNotFound)
		return nil, false
	}
	return channel, true
}

func writeSearchBackendLookupError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httperr.Code(c, types.ErrorCodeSearchBackendNotFound)
		return
	}
	httperr.Write(c, err)
}

func searchBackendChannelFromRecord(req dto.SearchBackendRecord, existing *model.Channel) (*model.Channel, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	channelType := req.Type
	if provider == "" && channelType != 0 {
		provider = constant.SearchProviderFromChannelType(channelType)
	}
	if channelType == 0 && provider != "" {
		channelType = constant.SearchChannelTypeFromProvider(provider)
	}
	if provider == "" || channelType == constant.ChannelTypeUnknown || !constant.IsSearchChannelType(channelType) {
		return nil, types.Err(types.ErrorCodeSearchBackendProviderInvalid, types.D{"field": "provider", "provider": provider})
	}
	if expected := constant.SearchProviderFromChannelType(channelType); expected != provider {
		return nil, types.Err(types.ErrorCodeSearchBackendProviderInvalid, types.D{"field": "provider", "provider": provider})
	}

	channel := &model.Channel{}
	if existing != nil {
		copy := *existing
		channel = &copy
	} else {
		channel.Status = common.ChannelStatusEnabled
		channel.CreatedTime = common.GetTimestamp()
		channel.Group = model.ChannelLaneDirect
	}
	channel.Type = channelType
	channel.Name = strings.TrimSpace(req.Name)
	if channel.Name == "" {
		channel.Name = constant.GetChannelTypeName(channelType)
	}
	channel.Models = ""
	channel.Group = model.ChannelLaneDirect
	if req.Status != common.ChannelStatusUnknown {
		channel.Status = req.Status
	} else if existing == nil {
		channel.Status = common.ChannelStatusEnabled
	}
	priority := req.Priority
	channel.Priority = &priority
	if strings.TrimSpace(req.BaseURL) != "" {
		baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
		channel.BaseURL = &baseURL
	} else {
		channel.BaseURL = nil
	}
	if strings.TrimSpace(req.APIKey) != "" {
		channel.Key = strings.TrimSpace(req.APIKey)
	} else if existing == nil {
		channel.Key = ""
	}

	settings := channel.GetOtherSettings()
	settings.SearchBackend = &dto.SearchBackendConfig{
		Provider:        provider,
		Capabilities:    normalizeSearchCapabilities(req.Capabilities, provider),
		MaxResults:      normalizeSearchMaxResults(req.MaxResults),
		TimeoutMs:       normalizeSearchTimeoutMs(req.TimeoutMs),
		SearchPrice:     nonNegativeFloat(req.SearchPrice),
		FetchPrice:      nonNegativeFloat(req.FetchPrice),
		UseProviderCost: req.UseProviderCost,
	}
	channel.SetOtherSettings(settings)
	return channel, nil
}

func searchBackendRecordFromChannel(channel *model.Channel, includeKey bool) dto.SearchBackendRecord {
	if channel == nil {
		return dto.SearchBackendRecord{}
	}
	settings := channel.GetOtherSettings().SearchBackend
	provider := constant.SearchProviderFromChannelType(channel.Type)
	capabilities := normalizeSearchCapabilities(nil, provider)
	maxResults := normalizeSearchMaxResults(0)
	timeoutMs := normalizeSearchTimeoutMs(0)
	searchPrice := 0.0
	fetchPrice := 0.0
	useProviderCost := false
	if settings != nil {
		if strings.TrimSpace(settings.Provider) != "" {
			provider = strings.ToLower(strings.TrimSpace(settings.Provider))
		}
		capabilities = normalizeSearchCapabilities(settings.Capabilities, provider)
		maxResults = normalizeSearchMaxResults(settings.MaxResults)
		timeoutMs = normalizeSearchTimeoutMs(settings.TimeoutMs)
		searchPrice = nonNegativeFloat(settings.SearchPrice)
		fetchPrice = nonNegativeFloat(settings.FetchPrice)
		useProviderCost = settings.UseProviderCost
	}
	recordID := channel.PublicID
	if recordID == "" {
		recordID = fmt.Sprint(channel.Id)
	}
	record := dto.SearchBackendRecord{
		ID:              recordID,
		MaskedKey:       maskSearchCredential(channel.Key),
		LastUsedAt:      channel.LastBusinessUsedAt,
		Name:            channel.Name,
		Provider:        provider,
		Type:            channel.Type,
		BaseURL:         channel.GetBaseURL(),
		Capabilities:    capabilities,
		MaxResults:      maxResults,
		TimeoutMs:       timeoutMs,
		Priority:        channel.GetPriority(),
		Status:          channel.Status,
		SearchPrice:     searchPrice,
		FetchPrice:      fetchPrice,
		UseProviderCost: useProviderCost,
	}
	if includeKey {
		record.APIKey = channel.Key
	}
	return record
}

func normalizeSearchCapabilities(capabilities []string, provider string) []string {
	if len(capabilities) == 0 {
		switch provider {
		case "tavily":
			return []string{dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch, dto.SearchCapabilityAnswer}
		case "jina":
			return []string{dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch}
		default:
			return []string{dto.SearchCapabilityWebSearch}
		}
	}
	result := make([]string, 0, len(capabilities))
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.ToLower(strings.TrimSpace(capability))
		switch capability {
		case dto.SearchCapabilityWebSearch, dto.SearchCapabilityWebFetch, dto.SearchCapabilityAnswer:
		default:
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	if len(result) == 0 {
		return []string{dto.SearchCapabilityWebSearch}
	}
	return result
}

func normalizeSearchMaxResults(value int) int {
	if value <= 0 {
		return 10
	}
	if value > 50 {
		return 50
	}
	return value
}

func normalizeSearchTimeoutMs(value int) int {
	if value <= 0 {
		return 8000
	}
	if value < 1000 {
		return 1000
	}
	if value > 60000 {
		return 60000
	}
	return value
}

func nonNegativeFloat(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func maskSearchCredential(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 12 {
		return "••••••••"
	}
	return key[:8] + "••••••••" + key[len(key)-4:]
}

func RevealSearchBackendKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	channel, ok := loadSearchBackend(c)
	if !ok {
		return
	}
	model.RecordLog(c.GetInt("id"), model.LogTypeSystem, fmt.Sprintf("Viewed search service key (channel_id=%d)", channel.Id))
	common.ApiSuccess(c, gin.H{"key": channel.Key})
}
