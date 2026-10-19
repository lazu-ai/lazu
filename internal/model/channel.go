package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
)

const (
	ChannelLaneDirect = setting.LaneSlugDirect
	ChannelLaneAgent  = setting.LaneSlugAgent
)

func NormalizeChannelLane(group string) string {
	return setting.NormalizeLaneSlug(group)
}

// IsValidChannelLane reports whether new traffic may use the lane. A lane that
// was stopped is still *known* (its prices and discounts resolve) but is no
// longer routable, so callers on the request path fall back to 稳定.
func IsValidChannelLane(group string) bool {
	return setting.IsLaneEnabled(group)
}

// IsKnownChannelLane reports whether the lane is registered at all, stopped or
// not. Pricing, billing and the console use this: a stopped lane must keep
// resolving the sell prices and discounts already written against it.
func IsKnownChannelLane(group string) bool {
	return setting.IsLaneKnown(group)
}

// Where the supply comes from. This is orthogonal to Channel.Type (which is the
// API-protocol adapter id): a "cloud" source and an "official" source can both
// speak the Anthropic protocol, but they carry very different trust, price and
// stability profiles, and the 模型运营 console groups the source ledger by this.
const (
	// ChannelSourceUnclassified is a real state, not a placeholder: nothing a
	// channel stores reveals whether it is first-party or a resale, so a row
	// nobody has classified must say so rather than assert "官方". The console
	// renders it as 未分类 and offers it as a filter, which is how an operator
	// finds the rows still waiting on a decision.
	ChannelSourceUnclassified = "unclassified"
	ChannelSourceOfficial     = "official"   // 官方 — the model vendor's own API
	ChannelSourceCloud        = "cloud"      // 云厂商 — Bedrock / Vertex / Azure style resale
	ChannelSourceAggregator   = "aggregator" // 聚合 — OpenRouter and friends
	ChannelSourceReverse      = "reverse"    // 非官方 — unofficial gateways; stored value predates the label
)

// NormalizeChannelSourceType maps stored/user input onto the known source
// types. Anything unset or unrecognised is 未分类 — never silently promoted to
// 官方, which would turn "we do not know" into a confident claim about a
// supplier's provenance.
func NormalizeChannelSourceType(sourceType string) string {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case ChannelSourceOfficial:
		return ChannelSourceOfficial
	case ChannelSourceCloud:
		return ChannelSourceCloud
	case ChannelSourceAggregator, "agg":
		return ChannelSourceAggregator
	case ChannelSourceReverse, "rev":
		return ChannelSourceReverse
	default:
		return ChannelSourceUnclassified
	}
}

// IsValidChannelSourceType reports whether the value names a decided type.
// 未分类 is a legal stored value but not a decision, so it is excluded.
func IsValidChannelSourceType(sourceType string) bool {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case ChannelSourceOfficial, ChannelSourceCloud, ChannelSourceAggregator, ChannelSourceReverse:
		return true
	default:
		return false
	}
}

// ChannelSourceTypes is the display/validation order used by the console.
func ChannelSourceTypes() []string {
	return []string{
		ChannelSourceUnclassified,
		ChannelSourceOfficial,
		ChannelSourceCloud,
		ChannelSourceAggregator,
		ChannelSourceReverse,
	}
}

func (channel *Channel) GetSourceType() string {
	if channel == nil {
		return ChannelSourceOfficial
	}
	return NormalizeChannelSourceType(channel.SourceType)
}

// BackfillChannelSourceTypes gives every pre-existing channel the explicit
// 未分类 value.
//
// It deliberately does NOT guess. The only signals a channel row carries are
// its protocol adapter id and its base URL, and neither distinguishes a
// first-party account from a cloud resale or a reverse gateway — an Anthropic
// key and a Bedrock deployment can both sit on the default endpoint. Writing
// "官方" for those rows would record a guess as a fact and leave an operator no
// way to tell which rows were guessed, so they are marked undecided and the
// 来源 tab surfaces them for review.
//
// Idempotent: it only touches rows that have never been classified.
func BackfillChannelSourceTypes(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Channel{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&Channel{}, "source_type") {
		return nil
	}
	return db.Model(&Channel{}).
		Where("source_type IS NULL OR source_type = ?", "").
		Update("source_type", ChannelSourceUnclassified).Error
}

type Channel struct {
	Id                 int     `json:"-"`
	PublicID           string  `json:"id" gorm:"type:varchar(64);uniqueIndex"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	KeyDigest          string  `json:"-" gorm:"type:varchar(64);index"`
	KeyDigests         string  `json:"-" gorm:"column:key_digests;type:text"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	LastBusinessUsedAt int64   `json:"last_business_used_at" gorm:"bigint;default:0"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	// Other carries the Gemini API version for Gemini channels.
	Other  string `json:"other"`
	Models string `json:"models"`
	Group  string `json:"group" gorm:"type:varchar(64);default:'direct'"`
	// SourceType is 官方/云厂商/聚合/非官方. Column default is empty on purpose so the
	// one-time backfill (backfillChannelSourceTypes) can tell "never classified"
	// apart from "deliberately set to official"; reads go through GetSourceType.
	SourceType        string  `json:"source_type" gorm:"type:varchar(24);default:'';index"`
	ModelMapping      *string `json:"model_mapping" gorm:"type:text"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	Priority       *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan        *int    `json:"auto_ban" gorm:"default:1"`
	Tag            *string `json:"tag" gorm:"index"`
	Setting        *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride  *string `json:"param_override" gorm:"type:text"`
	HeaderOverride *string `json:"header_override" gorm:"type:text"`
	Remark         *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings
	// DeletedAt soft-deletes a channel so past logs, outcomes and costs keep
	// its name. A deleted channel is also disabled and loses its abilities, so
	// nothing routes to it.
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// cache info
	Keys               []string            `json:"-" gorm:"-"`
	ProviderCostPrices []ProviderCostPrice `json:"provider_cost_prices,omitempty" gorm:"-"`
}

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
}

// Value implements driver.Valuer interface
func (c ChannelInfo) Value() (driver.Value, error) {
	body, err := common.Marshal(&c)
	return string(body), err
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value interface{}) error {
	if value == nil {
		*c = ChannelInfo{}
		return nil
	}
	switch body := value.(type) {
	case []byte:
		return common.Unmarshal(body, c)
	case string:
		return common.Unmarshal([]byte(body), c)
	default:
		return fmt.Errorf("invalid ChannelInfo storage %T", value)
	}
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.LazuError) {
	// If not in multi-key mode, return the original key string directly.
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect indexes of enabled keys
	enabledIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) == common.ChannelStatusEnabled {
			enabledIdx = append(enabledIdx, i)
		}
	}
	// If no specific status list or none enabled, fall back to first key
	if len(enabledIdx) == 0 {
		return keys[0], 0, nil
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one enabled key
		selectedIdx := enabledIdx[rand.Intn(len(enabledIdx))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		//println("before polling index:", channel.ChannelInfo.MultiKeyPollingIndex)
		defer func() {
			if common.DebugEnabled {
				println(fmt.Sprintf("channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex))
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := 0; i < len(keys); i++ {
			idx := (start + i) % len(keys)
			if getStatus(idx) == common.ChannelStatusEnabled {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return first enabled key
		return keys[enabledIdx[0]], enabledIdx[0], nil
	default:
		// Unknown mode, default to first enabled key (or original key string)
		return keys[enabledIdx[0]], enabledIdx[0], nil
	}
}

// EnabledKeyCount reports how many distinct attempts a channel can make before
// a retry should move to another channel. It shares the per-channel lock used
// by key selection because key status can change concurrently after failures.
func (channel *Channel) EnabledKeyCount() int {
	if channel == nil || !channel.ChannelInfo.IsMultiKey {
		return 1
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return 1
	}
	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()
	enabled := 0
	for index := range keys {
		status, configured := channel.ChannelInfo.MultiKeyStatusList[index]
		if !configured || status == common.ChannelStatusEnabled {
			enabled++
		}
	}
	if enabled == 0 {
		return 1
	}
	return enabled
}

func (channel *Channel) SaveChannelInfo() error {
	return CoreDB().Model(channel).Update("channel_info", channel.ChannelInfo).Error
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{ChannelLaneDirect}
	}
	rawGroups := strings.Split(strings.Trim(channel.Group, ","), ",")
	groups := make([]string, 0, len(rawGroups))
	seen := make(map[string]struct{}, len(rawGroups))
	for _, group := range rawGroups {
		group = NormalizeChannelLane(group)
		// Known, not routable: a source provisioned into a stopped lane keeps
		// that assignment (it is how the lane earns the right to be enabled).
		// Routing still ignores it — abilities are only built for enabled lanes.
		if !IsKnownChannelLane(group) {
			continue
		}
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		return []string{ChannelLaneDirect}
	}
	return groups
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	clearKey := channel.Key
	storageChannel, err := channel.storageCopyWithEncryptedKey()
	if err != nil {
		return err
	}
	err = CoreDB().Save(storageChannel).Error
	if err != nil {
		return err
	}
	*channel = *storageChannel
	channel.Key = clearKey
	return nil
}

func (channel *Channel) SaveWithoutKey() error {
	return CoreDB().Omit("key").Save(channel).Error
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	if selectAll {
		err = CoreDB().Order(order).Find(&channels).Error
	} else {
		err = CoreDB().Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	if err == nil && selectAll {
		err = decryptChannelKeysForUse(channels)
	}
	return channels, err
}

func GetChannelsByTag(tag string, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := CoreDB().Where("tag = ?", tag).Order(order).Find(&channels).Error
	if err == nil {
		err = decryptChannelKeysForUse(channels)
	}
	return channels, err
}

func SearchChannels(keyword string, group string, model string, idSort bool) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := CoreDB().Model(&Channel{}).Omit("key")
	keyDigest := ""
	keyDigestPattern := ""
	if strings.TrimSpace(keyword) != "" {
		var digestErr error
		keyDigest, keyDigestPattern, digestErr = channelKeyDigestSearchValues(keyword)
		if digestErr != nil {
			return nil, digestErr
		}
	}

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		groupCondition := `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		whereClause = "(id = ? OR name LIKE ? OR key_digest = ? OR key_digests LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyDigest, keyDigestPattern, keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR key_digest = ? OR key_digests LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyDigest, keyDigestPattern, keyword, "%"+keyword+"%", "%"+model+"%")
	}

	// 执行查询
	err := baseQuery.Where(whereClause, args...).Order(order).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = CoreDB().First(channel, "id = ?", id).Error
	} else {
		err = CoreDB().Omit("key").First(channel, "id = ?", id).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, types.Err(types.ErrorCodeChannelNotFound, types.D{"channel_id": id})
	}
	if err != nil {
		return nil, err
	}
	if selectAll {
		if err := channel.decryptKeyForUse(); err != nil {
			return nil, err
		}
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	for _, chunk := range lo.Chunk(channels, 50) {
		storageChunk := make([]Channel, 0, len(chunk))
		for i := range chunk {
			if err := initializeMissingProtocolDialect(&chunk[i], nil); err != nil {
				tx.Rollback()
				return err
			}
			storageChannel, err := (&chunk[i]).storageCopyWithEncryptedKey()
			if err != nil {
				tx.Rollback()
				return err
			}
			storageChunk = append(storageChunk, *storageChannel)
		}
		if err := tx.Create(&storageChunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, channel_ := range storageChunk {
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit().Error
}

func BatchDeleteChannels(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return tx.Error
	}
	for _, chunk := range lo.Chunk(ids, 200) {
		if err := tx.Model(&Channel{}).Where("id in (?)", chunk).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Where("id in (?)", chunk).Delete(&Channel{}).Error; err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	return DeleteRouteHealthStatesForChannels(ids)
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL != nil && *channel.BaseURL != "" {
		return *channel.BaseURL
	}
	if channel.Type >= 0 && channel.Type < len(constant.ChannelBaseURLs) {
		return constant.ChannelBaseURLs[channel.Type]
	}
	return ""
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) Insert() error {
	channel.SourceType = NormalizeChannelSourceType(channel.SourceType)
	if err := initializeMissingProtocolDialect(channel, nil); err != nil {
		return err
	}
	clearKey := channel.Key
	storageChannel, err := channel.storageCopyWithEncryptedKey()
	if err != nil {
		return err
	}
	err = CoreDB().Create(storageChannel).Error
	if err != nil {
		return err
	}
	*channel = *storageChannel
	channel.Key = clearKey
	err = channel.AddAbilities(nil)
	return err
}

func (channel *Channel) Update() error {
	if strings.TrimSpace(channel.SourceType) != "" {
		channel.SourceType = NormalizeChannelSourceType(channel.SourceType)
	}
	var existingIdentity Channel
	if err := CoreDB().Select("type, base_url, settings").First(&existingIdentity, "id = ?", channel.Id).Error; err != nil {
		return err
	}
	if err := initializeMissingProtocolDialect(channel, &existingIdentity); err != nil {
		return err
	}
	// If this is a multi-key channel, recalculate MultiKeySize based on the current key list to avoid inconsistency after editing keys
	if channel.ChannelInfo.IsMultiKey {
		var keyStr string
		if channel.Key != "" {
			keyStr = channel.Key
		} else {
			// If key is not provided, read the existing key from the database
			if existing, err := GetChannelById(channel.Id, true); err == nil {
				keyStr = existing.Key
			}
		}
		// Parse the key list (supports newline separation or JSON array)
		keys := []string{}
		if keyStr != "" {
			trimmed := strings.TrimSpace(keyStr)
			if strings.HasPrefix(trimmed, "[") {
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
					keys = make([]string, len(arr))
					for i, v := range arr {
						keys[i] = string(v)
					}
				}
			}
			if len(keys) == 0 { // fallback to newline split
				keys = strings.Split(strings.Trim(keyStr, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		// Clean up status data that exceeds the new key count to prevent index out of range
		if channel.ChannelInfo.MultiKeyStatusList != nil {
			for idx := range channel.ChannelInfo.MultiKeyStatusList {
				if idx >= channel.ChannelInfo.MultiKeySize {
					delete(channel.ChannelInfo.MultiKeyStatusList, idx)
				}
			}
		}
	}
	var err error
	clearKey := channel.Key
	storageChannel, err := channel.storageCopyWithEncryptedKey()
	if err != nil {
		return err
	}
	err = CoreDB().Model(channel).Updates(storageChannel).Error
	if err != nil {
		return err
	}
	if err = CoreDB().Model(channel).First(channel, "id = ?", channel.Id).Error; err != nil {
		return err
	}
	if err = channel.decryptKeyForUse(); err != nil {
		return err
	}
	if clearKey != "" {
		channel.Key = clearKey
	}
	err = channel.UpdateAbilities(nil)
	return err
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := CoreDB().Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	var err error
	if err = CoreDB().Model(&Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
		return err
	}
	err = CoreDB().Delete(channel).Error
	if err != nil {
		return err
	}
	if err = channel.DeleteAbilities(); err != nil {
		return err
	}
	return DeleteRouteHealthStatesForChannels([]int{channel.Id})
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	CoreDB().Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value interface{}) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
	} else {
		var keyIndex int
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
		}
		if len(channel.ChannelInfo.MultiKeyStatusList) >= channel.ChannelInfo.MultiKeySize {
			channel.Status = common.ChannelStatusAutoDisabled
		}
	}
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()

		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			// Use per-channel lock to prevent concurrent map read/write with GetNextEnabledKey
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			// 如果是多Key模式，更新缓存中的状态
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			pollingLock.Unlock()
			//CacheUpdateChannel(channelCache)
			//return true
		} else {
			// 如果缓存渠道存在，且状态已是目标状态，直接返回
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled)
			if err != nil {
				common.SysLog(fmt.Sprintf("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	channel, err := GetChannelById(channelId, true)
	if err != nil {
		return false
	} else {
		if channel.Status == status {
			return false
		}

		if channel.ChannelInfo.IsMultiKey {
			beforeStatus := channel.Status
			// Protect map writes with the same per-channel lock used by readers
			pollingLock := GetChannelPollingLock(channelId)
			pollingLock.Lock()
			handlerMultiKeyUpdate(channel, usingKey, status, reason)
			pollingLock.Unlock()
			if beforeStatus != channel.Status {
				shouldUpdateAbilities = true
			}
		} else {
			channel.Status = status
			shouldUpdateAbilities = true
		}
		err = channel.SaveWithoutKey()
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to update channel status: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
			return false
		}
	}
	return true
}

// UpdateWholeChannelStatus changes the channel's routing state without
// interpreting an empty usingKey as the first key in a multi-key channel.
// This is intentionally distinct from UpdateChannelStatus, which is used by
// per-key automatic recovery and must keep its selected-key semantics.
func UpdateWholeChannelStatus(channelId int, status int, reason string) bool {
	channel, err := GetChannelById(channelId, true)
	if err != nil || channel.Status == status {
		return false
	}
	channel.Status = status
	if err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("key").Save(channel).Error; err != nil {
			return err
		}
		return tx.Model(&Ability{}).Where("channel_id = ?", channelId).
			Select("enabled").Update("enabled", status == common.ChannelStatusEnabled).Error
	}); err != nil {
		common.SysLog(fmt.Sprintf("failed to update whole channel status atomically: channel_id=%d, status=%d, error=%v", channel.Id, status, err))
		return false
	}
	// Rebuild instead of applying the per-key cache mutation used above: an
	// entire multi-key channel is now in or out of every pool it serves.
	InitChannelCache()
	return true
}

func EnableChannelByTag(tag string) error {
	err := CoreDB().Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusEnabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, true)
	return err
}

func DisableChannelByTag(tag string) error {
	err := CoreDB().Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusManuallyDisabled).Error
	if err != nil {
		return err
	}
	err = UpdateAbilityStatusByTag(tag, false)
	return err
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil && *modelMapping != "" {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		// A source belongs to exactly one lane, and only to a known one.
		if strings.Contains(*group, ",") {
			return types.Err(types.ErrorCodeChannelLaneInvalid, types.D{"lane": *group})
		}
		normalizedGroup := NormalizeChannelLane(*group)
		if !IsKnownChannelLane(normalizedGroup) {
			return types.Err(types.ErrorCodeChannelLaneInvalid, types.D{"lane": normalizedGroup})
		}
		shouldReCreateAbilities = true
		updateData.Group = normalizedGroup
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}

	err := CoreDB().Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error
	if err != nil {
		return err
	}
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false)
		if err == nil {
			for _, channel := range channels {
				err = channel.UpdateAbilities(nil)
				if err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
	} else {
		// Channels already carry the new tag at this point.
		err := UpdateAbilityByTag(updatedTag, priority, weight)
		if err != nil {
			return err
		}
	}
	return nil
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := CoreDB().Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := CoreDB().
		Where("(status = ? OR status = ?) AND type NOT IN ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled, constant.SearchChannelTypes()).
		Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	var tags []*string
	err := CoreDB().Model(&Channel{}).Select("DISTINCT tag").Where("tag != ''").Offset(offset).Limit(limit).Find(&tags).Error
	return tags, err
}

func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingPostgreSQL {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := CoreDB().Model(&Channel{}).Omit("key")
	keyDigest := ""
	keyDigestPattern := ""
	if strings.TrimSpace(keyword) != "" {
		var digestErr error
		keyDigest, keyDigestPattern, digestErr = channelKeyDigestSearchValues(keyword)
		if digestErr != nil {
			return nil, digestErr
		}
	}

	// 构造WHERE子句
	var whereClause string
	var args []interface{}
	if group != "" && group != "null" {
		groupCondition := `(',' || ` + commonGroupCol + ` || ',') LIKE ?`
		whereClause = "(id = ? OR name LIKE ? OR key_digest = ? OR key_digests LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + ` LIKE ? AND ` + groupCondition
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyDigest, keyDigestPattern, keyword, "%"+keyword+"%", "%"+model+"%", "%,"+group+",%")
	} else {
		whereClause = "(id = ? OR name LIKE ? OR key_digest = ? OR key_digests LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
		args = append(args, common.String2Int(keyword), "%"+keyword+"%", keyDigest, keyDigestPattern, keyword, "%"+keyword+"%", "%"+model+"%")
	}

	subQuery := baseQuery.Where(whereClause, args...).
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := CoreDB().Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	if channel.OtherSettings != "" {
		var other dto.ChannelOtherSettings
		if err := common.Unmarshal([]byte(channel.OtherSettings), &other); err != nil {
			return types.WrapErr(err, types.ErrorCodeChannelSettingsInvalid, types.D{"field": "settings"})
		}
		if err := other.ValidateClaudeCompatibility(); err != nil {
			field := "claude_omit_unused_system_beta_models"
			var configErr *dto.ClaudeCompatibilityConfigError
			if errors.As(err, &configErr) {
				field = configErr.Field
			}
			return types.WrapErr(err, types.ErrorCodeChannelSettingsInvalid, types.D{"field": field})
		}
	}
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return types.WrapErr(err, types.ErrorCodeChannelSettingsInvalid, types.D{"field": "setting"})
		}
	}
	if err := channelParams.ValidateDecisionSettings(); err != nil {
		return types.WrapErr(err, types.ErrorCodeChannelSettingsInvalid, types.D{"field": "setting"})
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			_ = channel.Save()    // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			_ = channel.Save()           // 保存修改
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

// GetCapabilities returns the channel's structured capability declaration,
// or nil if the operator hasn't filled one in. The composite router
// consumes the result to do capability-based candidate filtering. nil is
// the legacy default and intentionally means "assume fully capable" —
// every channel that existed before this field was added returns nil,
// preserving previous routing behaviour.
//
// Today this re-parses OtherSettings on every call; the cost is bounded
// by the size of the JSON blob (a few hundred bytes) and the call rate
// equals (# candidates × # routed requests). If router profiling later
// shows this is hot, the natural place to cache is the channel cache
// (internal/model/channel_cache.go) which already holds a *Channel
// reference per id — populate a cachedCapabilities field there.
func (channel *Channel) GetCapabilities() *dto.ChannelCapabilities {
	settings := channel.GetOtherSettings()
	return settings.GetCapabilities()
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := CoreDB().Where("id in (?)", ids).Find(&channels).Error
	if err == nil {
		err = decryptChannelKeysForUse(channels)
	}
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	// 开启事务
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// 更新标签
	err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	// update ability status
	channels, err := GetChannelsByIds(ids)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		err = channel.UpdateAbilities(tx)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// 提交事务
	return tx.Commit().Error
}

// CountAllChannels returns total channels in CoreDB()
func CountAllChannels() (int64, error) {
	var total int64
	err := CoreDB().Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	var total int64
	err := CoreDB().Model(&Channel{}).Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := CoreDB().Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := CoreDB().Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := CoreDB().Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
