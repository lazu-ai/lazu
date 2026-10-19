package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type OpenAIModel struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	Permission []struct {
		ID                 string `json:"id"`
		Object             string `json:"object"`
		Created            int64  `json:"created"`
		AllowCreateEngine  bool   `json:"allow_create_engine"`
		AllowSampling      bool   `json:"allow_sampling"`
		AllowLogprobs      bool   `json:"allow_logprobs"`
		AllowSearchIndices bool   `json:"allow_search_indices"`
		AllowView          bool   `json:"allow_view"`
		AllowFineTuning    bool   `json:"allow_fine_tuning"`
		Organization       string `json:"organization"`
		Group              string `json:"group"`
		IsBlocking         bool   `json:"is_blocking"`
	} `json:"permission"`
	Root   string `json:"root"`
	Parent string `json:"parent"`
}

type OpenAIModelsResponse struct {
	Data    []OpenAIModel `json:"data"`
	Success bool          `json:"success"`
}

type upstreamModelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func parseUpstreamModelIDs(body []byte) ([]string, error) {
	var result upstreamModelListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	models := make([]string, 0, len(result.Data)+len(result.Models))
	appendModel := func(name string) {
		name = strings.TrimSpace(strings.TrimPrefix(name, "models/"))
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		models = append(models, name)
	}
	for _, item := range result.Data {
		appendModel(item.ID)
	}
	for _, item := range result.Models {
		appendModel(item.Name)
	}
	return models, nil
}

func upstreamModelListURLs(baseURL string, channelType int, key string) []string {
	baseURL = strings.TrimRight(baseURL, "/")
	if channelType != constant.ChannelTypeGemini {
		return []string{baseURL + "/v1/models"}
	}
	urls := []string{baseURL + "/v1/models"}
	if key != "" {
		urls = append(urls, baseURL+"/v1beta/models?key="+url.QueryEscape(key))
	}
	urls = append(urls, baseURL+"/v1beta/models", baseURL+"/v1beta/openai/models")
	return urls
}

func fetchUpstreamModelIDs(baseURL string, channelType int, key string, channel *model.Channel) ([]string, error) {
	key = strings.TrimSpace(strings.Split(key, "\n")[0])
	var lastErr error
	for _, endpoint := range upstreamModelListURLs(baseURL, channelType, key) {
		headers := GetAuthHeader(key)
		if channelType == constant.ChannelTypeAnthropic {
			headers = GetClaudeAuthHeader(key)
		}
		if channelType == constant.ChannelTypeGemini && strings.Contains(endpoint, "?key=") {
			headers = http.Header{}
		}
		var body []byte
		var err error
		if channel != nil {
			body, err = GetResponseBody(http.MethodGet, endpoint, channel, headers)
		} else {
			body, err = getResponseBodyWithoutChannel(http.MethodGet, endpoint, headers)
		}
		if err != nil {
			lastErr = err
			continue
		}
		models, err := parseUpstreamModelIDs(body)
		if err != nil {
			lastErr = err
			continue
		}
		if len(models) > 0 || channelType != constant.ChannelTypeGemini {
			return models, nil
		}
		lastErr = fmt.Errorf("no models found in upstream response")
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return []string{}, nil
}

func getResponseBodyWithoutChannel(method, endpoint string, headers http.Header) ([]byte, error) {
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for k := range headers {
		req.Header.Add(k, headers.Get(k))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &upstreamStatusError{status: resp.StatusCode}
	}
	return io.ReadAll(resp.Body)
}

func parseStatusFilter(statusParam string) int {
	switch strings.ToLower(statusParam) {
	case "enabled", "1":
		return common.ChannelStatusEnabled
	case "disabled", "0":
		return 0
	default:
		return -1
	}
}

func includeSearchBackends(c *gin.Context) bool {
	include, _ := strconv.ParseBool(c.Query("include_search_backends"))
	return include
}

// rejectSearchBackendChannelMutation answers a channel route that reached a
// search backend: those are created and edited on their own page.
func rejectSearchBackendChannelMutation(c *gin.Context, action string) {
	httperr.Code(c, types.ErrorCodeChannelSearchBackendManaged, types.D{"action": action})
}

// resolveChannelID turns a channel's public or numeric id into its row id.
// An id that names no channel is channel_not_found; a lookup failure stays
// internal.
func resolveChannelID(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "id"})
	}
	id, err := resolveIDParam("channels", raw)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, types.Err(types.ErrorCodeChannelNotFound, types.D{"channel_id": raw})
	}
	return id, err
}

// resolveChannelIDs resolves a batch of channel ids; the first unknown one
// fails the whole batch.
func resolveChannelIDs(raws []string) ([]int, error) {
	ids := make([]int, 0, len(raws))
	for _, raw := range raws {
		id, err := resolveChannelID(raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func tagContainsSearchBackends(tag string) (bool, error) {
	var count int64
	err := model.CoreDB().Model(&model.Channel{}).
		Where("tag = ? AND type IN ?", tag, constant.SearchChannelTypes()).
		Count(&count).Error
	return count > 0, err
}

func clearChannelInfo(channel *model.Channel) {
	channel.Key = ""
	if channel.ChannelInfo.IsMultiKey {
		channel.ChannelInfo.MultiKeyDisabledReason = nil
		channel.ChannelInfo.MultiKeyDisabledTime = nil
	}
}

func GetAllChannels(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelData := make([]*model.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	includeSearch := includeSearchBackends(c)
	statusParam := c.Query("status")
	// statusFilter: -1 all, 1 enabled, 0 disabled (include auto & manual)
	statusFilter := parseStatusFilter(statusParam)
	// type filter
	typeStr := c.Query("type")
	typeFilter := -1
	if typeStr != "" {
		if t, err := strconv.Atoi(typeStr); err == nil {
			typeFilter = t
		}
	}

	var total int64

	if enableTagMode {
		tags, err := model.GetPaginatedTags(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
		if err != nil {
			httperr.Write(c, err)
			return
		}
		for _, tag := range tags {
			if tag == nil || *tag == "" {
				continue
			}
			tagChannels, err := model.GetChannelsByTag(*tag, idSort)
			if err != nil {
				continue
			}
			filtered := make([]*model.Channel, 0)
			for _, ch := range tagChannels {
				if !includeSearch && constant.IsSearchChannelType(ch.Type) {
					continue
				}
				if statusFilter == common.ChannelStatusEnabled && ch.Status != common.ChannelStatusEnabled {
					continue
				}
				if statusFilter == 0 && ch.Status == common.ChannelStatusEnabled {
					continue
				}
				if typeFilter >= 0 && ch.Type != typeFilter {
					continue
				}
				filtered = append(filtered, ch)
			}
			channelData = append(channelData, filtered...)
		}
		total, _ = model.CountAllTags()
	} else {
		baseQuery := model.CoreDB().Model(&model.Channel{})
		if !includeSearch {
			baseQuery = baseQuery.Where("type NOT IN ?", constant.SearchChannelTypes())
		}
		if typeFilter >= 0 {
			baseQuery = baseQuery.Where("type = ?", typeFilter)
		}
		if statusFilter == common.ChannelStatusEnabled {
			baseQuery = baseQuery.Where("status = ?", common.ChannelStatusEnabled)
		} else if statusFilter == 0 {
			baseQuery = baseQuery.Where("status != ?", common.ChannelStatusEnabled)
		}

		baseQuery.Count(&total)

		order := "priority desc"
		if idSort {
			order = "id desc"
		}

		err := baseQuery.Order(order).Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Omit("key").Find(&channelData).Error
		if err != nil {
			httperr.Write(c, err)
			return
		}
	}

	for _, datum := range channelData {
		clearChannelInfo(datum)
	}

	countQuery := model.CoreDB().Model(&model.Channel{})
	if !includeSearch {
		countQuery = countQuery.Where("type NOT IN ?", constant.SearchChannelTypes())
	}
	if statusFilter == common.ChannelStatusEnabled {
		countQuery = countQuery.Where("status = ?", common.ChannelStatusEnabled)
	} else if statusFilter == 0 {
		countQuery = countQuery.Where("status != ?", common.ChannelStatusEnabled)
	}
	var results []struct {
		Type  int64
		Count int64
	}
	_ = countQuery.Select("type, count(*) as count").Group("type").Find(&results).Error
	typeCounts := make(map[int64]int64)
	for _, r := range results {
		typeCounts[r.Type] = r.Count
	}
	common.ApiSuccess(c, gin.H{
		"items":       channelData,
		"total":       total,
		"page":        pageInfo.GetPage(),
		"page_size":   pageInfo.GetPageSize(),
		"type_counts": typeCounts,
	})
	return
}

func FetchUpstreamModels(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	key := strings.Split(channel.Key, "\n")[0]
	ids, err := fetchUpstreamModelIDs(baseURL, channel.Type, key, channel)
	if err != nil {
		httperr.Write(c, upstreamFailure(err, types.ErrorCodeChannelUpstreamModelsFailed))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    ids,
	})
}

func FixChannelsAbilities(c *gin.Context) {
	success, fails, err := model.FixAbility()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"success": success,
			"fails":   fails,
		},
	})
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelKeyword := c.Query("model")
	statusParam := c.Query("status")
	statusFilter := parseStatusFilter(statusParam)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	includeSearch := includeSearchBackends(c)
	channelData := make([]*model.Channel, 0)
	if enableTagMode {
		tags, err := model.SearchTags(keyword, group, modelKeyword, idSort)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		for _, tag := range tags {
			if tag != nil && *tag != "" {
				tagChannel, err := model.GetChannelsByTag(*tag, idSort)
				if err == nil {
					channelData = append(channelData, tagChannel...)
				}
			}
		}
	} else {
		channels, err := model.SearchChannels(keyword, group, modelKeyword, idSort)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		channelData = channels
	}

	if !includeSearch {
		filtered := make([]*model.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if constant.IsSearchChannelType(ch.Type) {
				continue
			}
			filtered = append(filtered, ch)
		}
		channelData = filtered
	}

	if statusFilter == common.ChannelStatusEnabled || statusFilter == 0 {
		filtered := make([]*model.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if statusFilter == common.ChannelStatusEnabled && ch.Status != common.ChannelStatusEnabled {
				continue
			}
			if statusFilter == 0 && ch.Status == common.ChannelStatusEnabled {
				continue
			}
			filtered = append(filtered, ch)
		}
		channelData = filtered
	}

	// calculate type counts for search results
	typeCounts := make(map[int64]int64)
	for _, channel := range channelData {
		typeCounts[int64(channel.Type)]++
	}

	typeParam := c.Query("type")
	typeFilter := -1
	if typeParam != "" {
		if tp, err := strconv.Atoi(typeParam); err == nil {
			typeFilter = tp
		}
	}

	if typeFilter >= 0 {
		filtered := make([]*model.Channel, 0, len(channelData))
		for _, ch := range channelData {
			if ch.Type == typeFilter {
				filtered = append(filtered, ch)
			}
		}
		channelData = filtered
	}

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	total := len(channelData)
	startIdx := (page - 1) * pageSize
	if startIdx > total {
		startIdx = total
	}
	endIdx := startIdx + pageSize
	if endIdx > total {
		endIdx = total
	}

	pagedData := channelData[startIdx:endIdx]

	for _, datum := range pagedData {
		clearChannelInfo(datum)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":       pagedData,
			"total":       total,
			"type_counts": typeCounts,
		},
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if channel != nil {
		clearChannelInfo(channel)
		if costs, err := model.ListProviderCostPricesForChannel(channel.Id); err == nil {
			channel.ProviderCostPrices = costs
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// GetChannelKey returns a channel key only for authenticated admins.
// Channel list/detail endpoints intentionally omit keys; this explicit action is audited.
func GetChannelKey(c *gin.Context) {
	userId := c.GetInt("id")
	channelId, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}

	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	model.RecordLog(userId, model.LogTypeSystem, fmt.Sprintf("Viewed channel key (channel_id=%d)", channelId))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": map[string]interface{}{
			"key": channel.Key,
		},
	})
}

// validateChannel checks a channel before it is added or updated. Every
// failure is a catalog error the operator can act on.
func validateChannel(channel *model.Channel, isAdd bool) error {
	if channel == nil {
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "channel"})
	}
	if err := channel.ValidateSettings(); err != nil {
		return err
	}
	// A source belongs to exactly one lane.
	if strings.Contains(channel.Group, ",") {
		return types.Err(types.ErrorCodeChannelLaneInvalid, types.D{"lane": channel.Group})
	}
	channel.Group = model.NormalizeChannelLane(channel.Group)
	// Known, not routable: a new lane starts stopped and can only be enabled
	// once sources supply it, so assignment has to be possible while it is off.
	if !model.IsKnownChannelLane(channel.Group) {
		return types.Err(types.ErrorCodeChannelLaneInvalid, types.D{"lane": channel.Group})
	}

	if isAdd {
		if channel.Key == "" {
			return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "key"})
		}
		for _, m := range channel.GetModels() {
			if len(m) > 255 {
				return types.Err(types.ErrorCodeChannelModelNameTooLong, types.D{"model": m, "max": 255})
			}
		}
	}

	return nil
}

type AddChannelRequest struct {
	Mode                      string                `json:"mode"`
	MultiKeyMode              constant.MultiKeyMode `json:"multi_key_mode"`
	BatchAddSetKeyPrefix2Name bool                  `json:"batch_add_set_key_prefix_2_name"`
	Channel                   *model.Channel        `json:"channel"`
}

func AddChannel(c *gin.Context) {
	addChannelRequest := AddChannelRequest{}
	err := c.ShouldBindJSON(&addChannelRequest)
	if err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}

	if addChannelRequest.Channel != nil && constant.IsSearchChannelType(addChannelRequest.Channel.Type) {
		rejectSearchBackendChannelMutation(c, "create")
		return
	}

	if err := validateChannel(addChannelRequest.Channel, true); err != nil {
		httperr.Write(c, err)
		return
	}

	addChannelRequest.Channel.CreatedTime = common.GetTimestamp()
	keys := make([]string, 0)
	switch addChannelRequest.Mode {
	case "multi_to_single":
		addChannelRequest.Channel.ChannelInfo.IsMultiKey = true
		addChannelRequest.Channel.ChannelInfo.MultiKeyMode = addChannelRequest.MultiKeyMode
		cleanKeys := make([]string, 0)
		for _, key := range strings.Split(addChannelRequest.Channel.Key, "\n") {
			if key == "" {
				continue
			}
			key = strings.TrimSpace(key)
			cleanKeys = append(cleanKeys, key)
		}
		addChannelRequest.Channel.ChannelInfo.MultiKeySize = len(cleanKeys)
		addChannelRequest.Channel.Key = strings.Join(cleanKeys, "\n")
		keys = []string{addChannelRequest.Channel.Key}
	case "batch":
		keys = strings.Split(addChannelRequest.Channel.Key, "\n")
	case "single":
		keys = []string{addChannelRequest.Channel.Key}
	default:
		httperr.Invalid(c, "mode")
		return
	}

	channels := make([]model.Channel, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		localChannel := addChannelRequest.Channel
		localChannel.Key = key
		if addChannelRequest.BatchAddSetKeyPrefix2Name && len(keys) > 1 {
			keyPrefix := localChannel.Key
			if len(localChannel.Key) > 8 {
				keyPrefix = localChannel.Key[:8]
			}
			localChannel.Name = fmt.Sprintf("%s %s", localChannel.Name, keyPrefix)
		}
		channels = append(channels, *localChannel)
	}
	err = model.BatchInsertChannels(channels)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.create", "channel", "batch", addChannelRequest.Channel.Name, nil, gin.H{
		"mode":     addChannelRequest.Mode,
		"count":    len(channels),
		"channels": channels,
	})
	service.ResetProxyClientCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	err = channel.Delete()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.delete", "channel", channel.PublicID, channel.Name, channel, nil)
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

type ChannelTag struct {
	Tag          string  `json:"tag"`
	NewTag       *string `json:"new_tag"`
	Priority     *int64  `json:"priority"`
	Weight       *uint   `json:"weight"`
	ModelMapping *string `json:"model_mapping"`
	Models       *string `json:"models"`
	Groups       *string `json:"groups"`
}

type channelStatusRequest struct {
	Status *int   `json:"status"`
	Reason string `json:"reason"`
}

// UpdateChannelStatusById flips one upstream between enabled and manually
// disabled. It exists so an operator can take a failing source out of every
// pool it serves from wherever they spotted it — the provider ledger on the
// channel-health page is the case this was written for — without round-tripping
// the whole channel object through the edit form, which would risk overwriting
// unrelated fields with whatever the page happened to be holding.
//
// Weight cannot do this job: the picker gives every live route a base weight,
// so even weight 0 keeps a share of the traffic. Disabling is the only lever
// that actually stops it.
func UpdateChannelStatusById(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var req channelStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	// Only enable and manual disable: auto-disabled is the router's verdict.
	if req.Status == nil ||
		(*req.Status != common.ChannelStatusEnabled &&
			*req.Status != common.ChannelStatusManuallyDisabled) {
		httperr.Invalid(c, "status")
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if channel.Status == *req.Status {
		common.ApiSuccess(c, gin.H{"id": channel.PublicID, "status": channel.Status, "changed": false})
		return
	}
	previousStatus := channel.Status

	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "manually updated from the console"
	}
	if !model.UpdateWholeChannelStatus(id, *req.Status, reason) {
		// The model logs the cause; the status did not change.
		httperr.Write(c, fmt.Errorf("update whole channel status: channel %d to %d failed", id, *req.Status))
		return
	}
	model.WriteAdminAuditLog(c, "channel.status.update", "channel", channel.PublicID, channel.Name,
		gin.H{"status": previousStatus}, gin.H{"status": *req.Status, "reason": reason})
	refreshChannelHealthInBackground("status update")
	common.ApiSuccess(c, gin.H{"id": channel.PublicID, "status": *req.Status, "changed": true})
}

func DisableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if channelTag.Tag == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "tag"})
		return
	}
	if contains, err := tagContainsSearchBackends(channelTag.Tag); err != nil {
		httperr.Write(c, err)
		return
	} else if contains {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if err := model.DisableChannelByTag(channelTag.Tag); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.tag.disable", "channel_tag", channelTag.Tag, channelTag.Tag, nil, channelTag)
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EnableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if channelTag.Tag == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "tag"})
		return
	}
	if contains, err := tagContainsSearchBackends(channelTag.Tag); err != nil {
		httperr.Write(c, err)
		return
	} else if contains {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if err := model.EnableChannelByTag(channelTag.Tag); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.tag.enable", "channel_tag", channelTag.Tag, channelTag.Tag, nil, channelTag)
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EditTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	if err := c.ShouldBindJSON(&channelTag); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if channelTag.Tag == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "tag"})
		return
	}
	if contains, err := tagContainsSearchBackends(channelTag.Tag); err != nil {
		httperr.Write(c, err)
		return
	} else if contains {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if err := model.EditChannelByTag(channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.tag.update", "channel_tag", channelTag.Tag, channelTag.Tag, gin.H{"tag": channelTag.Tag}, channelTag)
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type ChannelBatch struct {
	Ids []string `json:"ids"`
	Tag *string  `json:"tag"`
}

func DeleteChannelBatch(c *gin.Context) {
	channelBatch := ChannelBatch{}
	if err := c.ShouldBindJSON(&channelBatch); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if len(channelBatch.Ids) == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "ids"})
		return
	}
	ids, err := resolveChannelIDs(channelBatch.Ids)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var searchCount int64
	if err := model.CoreDB().Model(&model.Channel{}).
		Where("id IN ? AND type IN ?", ids, constant.SearchChannelTypes()).
		Count(&searchCount).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if searchCount > 0 {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if err := model.BatchDeleteChannels(ids); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.batch_delete", "channel", "batch", "", gin.H{"ids": ids}, nil)
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    len(ids),
	})
	return
}

type PatchChannel struct {
	model.Channel
	MultiKeyMode *string `json:"multi_key_mode"`
	KeyMode      *string `json:"key_mode"` // 多key模式下密钥覆盖或者追加
}

func UpdateChannel(c *gin.Context) {
	channel := PatchChannel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}

	if err := validateChannel(&channel.Channel, false); err != nil {
		httperr.Write(c, err)
		return
	}
	if channel.Id == 0 && channel.PublicID != "" {
		channel.Id, err = resolveChannelID(channel.PublicID)
		if err != nil {
			httperr.Write(c, err)
			return
		}
	}
	// Preserve existing ChannelInfo to ensure multi-key channels keep correct state even if the client does not send ChannelInfo in the request.
	originChannel, err := model.GetChannelById(channel.Id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(originChannel.Type) || constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	// Always copy the original ChannelInfo so that fields like IsMultiKey and MultiKeySize are retained.
	channel.ChannelInfo = originChannel.ChannelInfo

	// If the request explicitly specifies a new MultiKeyMode, apply it on top of the original info.
	if channel.MultiKeyMode != nil && *channel.MultiKeyMode != "" {
		channel.ChannelInfo.MultiKeyMode = constant.MultiKeyMode(*channel.MultiKeyMode)
	}

	// 处理多key模式下的密钥追加/覆盖逻辑
	if channel.KeyMode != nil && channel.ChannelInfo.IsMultiKey {
		switch *channel.KeyMode {
		case "append":
			// 追加模式：将新密钥添加到现有密钥列表
			if originChannel.Key != "" {
				var newKeys []string
				var existingKeys []string

				// 解析现有密钥
				if strings.HasPrefix(strings.TrimSpace(originChannel.Key), "[") {
					// JSON数组格式
					var arr []json.RawMessage
					if err := json.Unmarshal([]byte(strings.TrimSpace(originChannel.Key)), &arr); err == nil {
						existingKeys = make([]string, len(arr))
						for i, v := range arr {
							existingKeys[i] = string(v)
						}
					}
				} else {
					// 换行分隔格式
					existingKeys = strings.Split(strings.Trim(originChannel.Key, "\n"), "\n")
				}

				inputKeys := strings.Split(channel.Key, "\n")
				for _, key := range inputKeys {
					key = strings.TrimSpace(key)
					if key != "" {
						newKeys = append(newKeys, key)
					}
				}
				allKeys := append(existingKeys, newKeys...)
				channel.Key = strings.Join(allKeys, "\n")
			}
		case "replace":
			// 覆盖模式：直接使用新密钥（默认行为，不需要特殊处理）
		}
	}
	err = channel.Update()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.ReplaceProviderCostPricesForChannel(channel.Id, channel.ProviderCostPrices); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.update", "channel", originChannel.PublicID, originChannel.Name, originChannel, channel.Channel)
	model.InitChannelCache()
	service.ResetProxyClientCache()
	channel.Key = ""
	clearChannelInfo(&channel.Channel)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

func FetchModels(c *gin.Context) {
	var req struct {
		BaseURL string `json:"base_url"`
		Type    int    `json:"type"`
		Key     string `json:"key"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if constant.IsSearchChannelType(req.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		baseURL = constant.ChannelBaseURLs[req.Type]
	}

	// remove line breaks and extra spaces.
	key := strings.TrimSpace(req.Key)
	// If the key contains a line break, only take the first part.
	key = strings.Split(key, "\n")[0]
	models, err := fetchUpstreamModelIDs(baseURL, req.Type, key, nil)
	if err != nil {
		httperr.Write(c, upstreamFailure(err, types.ErrorCodeChannelUpstreamModelsFailed))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    models,
	})
}

func BatchSetChannelTag(c *gin.Context) {
	channelBatch := ChannelBatch{}
	if err := c.ShouldBindJSON(&channelBatch); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	if len(channelBatch.Ids) == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "ids"})
		return
	}
	ids, err := resolveChannelIDs(channelBatch.Ids)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var searchCount int64
	if err := model.CoreDB().Model(&model.Channel{}).
		Where("id IN ? AND type IN ?", ids, constant.SearchChannelTypes()).
		Count(&searchCount).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if searchCount > 0 {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}
	if err := model.BatchSetChannelTag(ids, channelBatch.Tag); err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "channel.batch_tag", "channel", "batch", "", gin.H{"ids": ids}, gin.H{"ids": ids, "tag": channelBatch.Tag})
	model.InitChannelCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    len(ids),
	})
	return
}

func GetTagModels(c *gin.Context) {
	tag := c.Query("tag")
	if tag == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "tag"})
		return
	}

	channels, err := model.GetChannelsByTag(tag, false) // Assuming false for idSort is fine here
	if err != nil {
		httperr.Write(c, err)
		return
	}

	var longestModels string
	maxLength := 0

	// Find the longest models string among all channels with the given tag
	for _, channel := range channels {
		if channel.Models != "" {
			currentModels := strings.Split(channel.Models, ",")
			if len(currentModels) > maxLength {
				maxLength = len(currentModels)
				longestModels = channel.Models
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    longestModels,
	})
	return
}

// CopyChannel handles cloning an existing channel with its key.
// POST /api/channel/copy/:id
// Optional query params:
//
//	suffix         - string appended to the original name (default "_复制")
func CopyChannel(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}

	suffix := c.DefaultQuery("suffix", "_复制")

	// fetch original channel with key
	origin, err := model.GetChannelById(id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(origin.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	// clone channel
	clone := *origin // shallow copy is sufficient as we will overwrite primitives
	clone.Id = 0     // let DB auto-generate
	clone.PublicID = common.GeneratePublicID(common.PublicChannelIDPrefix)
	clone.CreatedTime = common.GetTimestamp()
	clone.Name = origin.Name + suffix
	clone.TestTime = 0
	clone.ResponseTime = 0

	// insert
	if err := model.BatchInsertChannels([]model.Channel{clone}); err != nil {
		httperr.Write(c, err)
		return
	}
	model.InitChannelCache()
	// success
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"id": clone.PublicID}})
}

// MultiKeyManageRequest represents the request for multi-key management operations
type MultiKeyManageRequest struct {
	ChannelId string `json:"channel_id"`
	Action    string `json:"action"`              // "disable_key", "enable_key", "delete_key", "delete_disabled_keys", "get_key_status"
	KeyIndex  *int   `json:"key_index,omitempty"` // for disable_key, enable_key, and delete_key actions
	Page      int    `json:"page,omitempty"`      // for get_key_status pagination
	PageSize  int    `json:"page_size,omitempty"` // for get_key_status pagination
	Status    *int   `json:"status,omitempty"`    // for get_key_status filtering: 1=enabled, 2=manual_disabled, 3=auto_disabled, nil=all
}

// MultiKeyStatusResponse represents the response for key status query
type MultiKeyStatusResponse struct {
	Keys       []KeyStatus `json:"keys"`
	Total      int         `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
	// Statistics
	EnabledCount        int `json:"enabled_count"`
	ManualDisabledCount int `json:"manual_disabled_count"`
	AutoDisabledCount   int `json:"auto_disabled_count"`
}

type KeyStatus struct {
	Index        int    `json:"index"`
	Status       int    `json:"status"` // 1: enabled, 2: disabled
	DisabledTime int64  `json:"disabled_time,omitempty"`
	Reason       string `json:"reason,omitempty"`
	KeyPreview   string `json:"key_preview"` // first 10 chars of key for identification
}

// ManageMultiKeys handles multi-key management operations
func ManageMultiKeys(c *gin.Context) {
	request := MultiKeyManageRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}

	channelID, err := resolveChannelID(request.ChannelId)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if constant.IsSearchChannelType(channel.Type) {
		rejectSearchBackendChannelMutation(c, "edit")
		return
	}

	if !channel.ChannelInfo.IsMultiKey {
		httperr.Code(c, types.ErrorCodeChannelNotMultiKey, types.D{"channel_id": request.ChannelId})
		return
	}

	lock := model.GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	switch request.Action {
	case "get_key_status":
		keys := channel.GetKeys()

		// Default pagination parameters
		page := request.Page
		pageSize := request.PageSize
		if page <= 0 {
			page = 1
		}
		if pageSize <= 0 {
			pageSize = 50 // Default page size
		}

		// Statistics for all keys (unchanged by filtering)
		var enabledCount, manualDisabledCount, autoDisabledCount int

		// Build all key status data first
		var allKeyStatusList []KeyStatus
		for i, key := range keys {
			status := 1 // default enabled
			var disabledTime int64
			var reason string

			if channel.ChannelInfo.MultiKeyStatusList != nil {
				if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
					status = s
				}
			}

			// Count for statistics (all keys)
			switch status {
			case 1:
				enabledCount++
			case 2:
				manualDisabledCount++
			case 3:
				autoDisabledCount++
			}

			if status != 1 {
				if channel.ChannelInfo.MultiKeyDisabledTime != nil {
					disabledTime = channel.ChannelInfo.MultiKeyDisabledTime[i]
				}
				if channel.ChannelInfo.MultiKeyDisabledReason != nil {
					reason = channel.ChannelInfo.MultiKeyDisabledReason[i]
				}
			}

			// Create key preview (first 10 chars)
			keyPreview := key
			if len(key) > 10 {
				keyPreview = key[:10] + "..."
			}

			allKeyStatusList = append(allKeyStatusList, KeyStatus{
				Index:        i,
				Status:       status,
				DisabledTime: disabledTime,
				Reason:       reason,
				KeyPreview:   keyPreview,
			})
		}

		// Apply status filter if specified
		var filteredKeyStatusList []KeyStatus
		if request.Status != nil {
			for _, keyStatus := range allKeyStatusList {
				if keyStatus.Status == *request.Status {
					filteredKeyStatusList = append(filteredKeyStatusList, keyStatus)
				}
			}
		} else {
			filteredKeyStatusList = allKeyStatusList
		}

		// Calculate pagination based on filtered results
		filteredTotal := len(filteredKeyStatusList)
		totalPages := (filteredTotal + pageSize - 1) / pageSize
		if totalPages == 0 {
			totalPages = 1
		}
		if page > totalPages {
			page = totalPages
		}

		// Calculate range for current page
		start := (page - 1) * pageSize
		end := start + pageSize
		if end > filteredTotal {
			end = filteredTotal
		}

		// Get the page data
		var pageKeyStatusList []KeyStatus
		if start < filteredTotal {
			pageKeyStatusList = filteredKeyStatusList[start:end]
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": MultiKeyStatusResponse{
				Keys:                pageKeyStatusList,
				Total:               filteredTotal, // Total of filtered results
				Page:                page,
				PageSize:            pageSize,
				TotalPages:          totalPages,
				EnabledCount:        enabledCount,        // Overall statistics
				ManualDisabledCount: manualDisabledCount, // Overall statistics
				AutoDisabledCount:   autoDisabledCount,   // Overall statistics
			},
		})
		return

	case "disable_key":
		keyIndex, ok := multiKeyIndex(c, channel, request, "disable_key")
		if !ok {
			return
		}

		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}

		channel.ChannelInfo.MultiKeyStatusList[keyIndex] = 2 // disabled

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, gin.H{"key_index": keyIndex})
		return

	case "enable_key":
		keyIndex, ok := multiKeyIndex(c, channel, request, "enable_key")
		if !ok {
			return
		}

		// 从状态列表中删除该密钥的记录，使其回到默认启用状态
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime != nil {
			delete(channel.ChannelInfo.MultiKeyDisabledTime, keyIndex)
		}
		if channel.ChannelInfo.MultiKeyDisabledReason != nil {
			delete(channel.ChannelInfo.MultiKeyDisabledReason, keyIndex)
		}

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, gin.H{"key_index": keyIndex})
		return

	case "enable_all_keys":
		// 清空所有禁用状态，使所有密钥回到默认启用状态
		var enabledCount int
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			enabledCount = len(channel.ChannelInfo.MultiKeyStatusList)
		}

		channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, gin.H{"count": enabledCount})
		return

	case "disable_all_keys":
		// 禁用所有启用的密钥
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if channel.ChannelInfo.MultiKeyDisabledTime == nil {
			channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
		}
		if channel.ChannelInfo.MultiKeyDisabledReason == nil {
			channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
		}

		var disabledCount int
		for i := 0; i < channel.ChannelInfo.MultiKeySize; i++ {
			status := 1 // default enabled
			if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
				status = s
			}

			// 只禁用当前启用的密钥
			if status == 1 {
				channel.ChannelInfo.MultiKeyStatusList[i] = 2 // disabled
				disabledCount++
			}
		}

		if disabledCount == 0 {
			httperr.Code(c, types.ErrorCodeChannelKeysNoneEligible, types.D{"action": request.Action})
			return
		}

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, gin.H{"count": disabledCount})
		return

	case "delete_key":
		keyIndex, ok := multiKeyIndex(c, channel, request, "delete_key")
		if !ok {
			return
		}

		keys := channel.GetKeys()
		var remainingKeys []string
		var newStatusList = make(map[int]int)
		var newDisabledTime = make(map[int]int64)
		var newDisabledReason = make(map[int]string)

		newIndex := 0
		for i, key := range keys {
			// 跳过要删除的密钥
			if i == keyIndex {
				continue
			}

			remainingKeys = append(remainingKeys, key)

			// 保留其他密钥的状态信息，重新索引
			if channel.ChannelInfo.MultiKeyStatusList != nil {
				if status, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists && status != 1 {
					newStatusList[newIndex] = status
				}
			}
			if channel.ChannelInfo.MultiKeyDisabledTime != nil {
				if t, exists := channel.ChannelInfo.MultiKeyDisabledTime[i]; exists {
					newDisabledTime[newIndex] = t
				}
			}
			if channel.ChannelInfo.MultiKeyDisabledReason != nil {
				if r, exists := channel.ChannelInfo.MultiKeyDisabledReason[i]; exists {
					newDisabledReason[newIndex] = r
				}
			}
			newIndex++
		}

		if len(remainingKeys) == 0 {
			httperr.Code(c, types.ErrorCodeChannelLastKey)
			return
		}

		// Update channel with remaining keys
		channel.Key = strings.Join(remainingKeys, "\n")
		channel.ChannelInfo.MultiKeySize = len(remainingKeys)
		channel.ChannelInfo.MultiKeyStatusList = newStatusList
		channel.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
		channel.ChannelInfo.MultiKeyDisabledReason = newDisabledReason

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, gin.H{"key_index": keyIndex})
		return

	case "delete_disabled_keys":
		keys := channel.GetKeys()
		var remainingKeys []string
		var deletedCount int
		var newStatusList = make(map[int]int)
		var newDisabledTime = make(map[int]int64)
		var newDisabledReason = make(map[int]string)

		newIndex := 0
		for i, key := range keys {
			status := 1 // default enabled
			if channel.ChannelInfo.MultiKeyStatusList != nil {
				if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
					status = s
				}
			}

			// 只删除自动禁用（status == 3）的密钥，保留启用（status == 1）和手动禁用（status == 2）的密钥
			if status == 3 {
				deletedCount++
			} else {
				remainingKeys = append(remainingKeys, key)
				// 保留非自动禁用密钥的状态信息，重新索引
				if status != 1 {
					newStatusList[newIndex] = status
					if channel.ChannelInfo.MultiKeyDisabledTime != nil {
						if t, exists := channel.ChannelInfo.MultiKeyDisabledTime[i]; exists {
							newDisabledTime[newIndex] = t
						}
					}
					if channel.ChannelInfo.MultiKeyDisabledReason != nil {
						if r, exists := channel.ChannelInfo.MultiKeyDisabledReason[i]; exists {
							newDisabledReason[newIndex] = r
						}
					}
				}
				newIndex++
			}
		}

		if deletedCount == 0 {
			httperr.Code(c, types.ErrorCodeChannelKeysNoneEligible, types.D{"action": request.Action})
			return
		}

		// Update channel with remaining keys
		channel.Key = strings.Join(remainingKeys, "\n")
		channel.ChannelInfo.MultiKeySize = len(remainingKeys)
		channel.ChannelInfo.MultiKeyStatusList = newStatusList
		channel.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
		channel.ChannelInfo.MultiKeyDisabledReason = newDisabledReason

		if err := channel.Update(); err != nil {
			httperr.Write(c, err)
			return
		}

		model.InitChannelCache()
		common.ApiSuccess(c, deletedCount)
		return

	default:
		httperr.Invalid(c, "action")
		return
	}
}

// multiKeyIndex reads the key index a single-key action names and checks it
// against the channel. It writes the failure and reports false when the
// index is missing or out of range.
func multiKeyIndex(c *gin.Context, channel *model.Channel, request MultiKeyManageRequest, action string) (int, bool) {
	if request.KeyIndex == nil {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "key_index", "action": action})
		return 0, false
	}
	keyIndex := *request.KeyIndex
	if keyIndex < 0 || keyIndex >= channel.ChannelInfo.MultiKeySize {
		httperr.Code(c, types.ErrorCodeChannelKeyIndexInvalid, types.D{
			"key_index": keyIndex,
			"key_count": channel.ChannelInfo.MultiKeySize,
		})
		return 0, false
	}
	return keyIndex, true
}
