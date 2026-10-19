package model

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"gorm.io/gorm"
)

type IntSlice []int

func (s IntSlice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "[]", nil
	}
	bytes, err := common.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func (s *IntSlice) Scan(value interface{}) error {
	if value == nil {
		*s = []int{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for IntSlice", value)
	}
	if len(bytes) == 0 {
		*s = []int{}
		return nil
	}
	return common.Unmarshal(bytes, s)
}

func (s *IntSlice) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*s = []int{}
		return nil
	}
	var raw []interface{}
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make([]int, 0, len(raw))
	for _, item := range raw {
		switch value := item.(type) {
		case float64:
			id := int(value)
			if id > 0 {
				result = append(result, id)
			}
		case string:
			id, err := ResolvePublicIntID("vendors", value)
			if err != nil {
				return fmt.Errorf("resolve vendor id %q: %w", value, err)
			}
			if id > 0 {
				result = append(result, id)
			}
		}
	}
	*s = result
	return nil
}

type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "[]", nil
	}
	bytes, err := common.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func (s *StringSlice) Scan(value interface{}) error {
	if value == nil {
		*s = []string{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for StringSlice", value)
	}
	if len(bytes) == 0 || strings.TrimSpace(string(bytes)) == "null" {
		*s = []string{}
		return nil
	}
	return common.Unmarshal(bytes, s)
}

func (s *StringSlice) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*s = []string{}
		return nil
	}
	var raw []string
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	*s = result
	return nil
}

type TokenVendorGroupMap map[string]string

func (m TokenVendorGroupMap) Value() (driver.Value, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	normalized := m.Normalize()
	bytes, err := common.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func (m *TokenVendorGroupMap) Scan(value interface{}) error {
	if value == nil {
		*m = TokenVendorGroupMap{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for TokenVendorGroupMap", value)
	}
	if len(bytes) == 0 || strings.TrimSpace(string(bytes)) == "null" {
		*m = TokenVendorGroupMap{}
		return nil
	}
	return m.UnmarshalJSON(bytes)
}

func (m *TokenVendorGroupMap) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*m = TokenVendorGroupMap{}
		return nil
	}
	var raw map[string]interface{}
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(TokenVendorGroupMap, len(raw))
	for vendorPublicID, value := range raw {
		vendorPublicID = strings.TrimSpace(vendorPublicID)
		if vendorPublicID == "" {
			continue
		}
		lane := ""
		switch typed := value.(type) {
		case string:
			lane = typed
		case map[string]interface{}:
			if primary, ok := typed["primary_group"].(string); ok {
				lane = primary
			} else if group, ok := typed["group"].(string); ok {
				lane = group
			}
		}
		lane = NormalizeChannelLane(lane)
		if !IsKnownChannelLane(lane) {
			return fmt.Errorf("invalid channel group %q for vendor %q", lane, vendorPublicID)
		}
		result[vendorPublicID] = lane
	}
	*m = result
	return nil
}

func (m TokenVendorGroupMap) Normalize() TokenVendorGroupMap {
	if len(m) == 0 {
		return TokenVendorGroupMap{}
	}
	result := make(TokenVendorGroupMap, len(m))
	for vendorPublicID, lane := range m {
		vendorPublicID = strings.TrimSpace(vendorPublicID)
		if vendorPublicID == "" {
			continue
		}
		lane = NormalizeChannelLane(lane)
		if !IsKnownChannelLane(lane) {
			continue
		}
		result[vendorPublicID] = lane
	}
	return result
}

func (m TokenVendorGroupMap) LaneForVendor(vendorPublicID string) string {
	vendorPublicID = strings.TrimSpace(vendorPublicID)
	if vendorPublicID == "" {
		return ChannelLaneDirect
	}
	if lane, ok := m[vendorPublicID]; ok && (NormalizeChannelLane(lane) == ChannelLaneAgent || IsValidChannelLane(lane)) {
		return NormalizeChannelLane(lane)
	}
	return ChannelLaneDirect
}

// TokenModelLaneMap pins single models to a lane, by model name. It is the
// finer half of the Key's lane policy: a model named here routes on its own
// lane whatever its vendor default says, and a model absent here falls back
// to that vendor default. The connect dialog writes it per model slot.
type TokenModelLaneMap map[string]string

func (m TokenModelLaneMap) Value() (driver.Value, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	bytes, err := common.Marshal(m.Normalize())
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func (m *TokenModelLaneMap) Scan(value interface{}) error {
	if value == nil {
		*m = TokenModelLaneMap{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for TokenModelLaneMap", value)
	}
	if len(bytes) == 0 || strings.TrimSpace(string(bytes)) == "null" {
		*m = TokenModelLaneMap{}
		return nil
	}
	return m.UnmarshalJSON(bytes)
}

func (m *TokenModelLaneMap) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*m = TokenModelLaneMap{}
		return nil
	}
	var raw map[string]string
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make(TokenModelLaneMap, len(raw))
	for modelName, lane := range raw {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		lane = NormalizeChannelLane(lane)
		if !IsKnownChannelLane(lane) {
			return fmt.Errorf("invalid channel group %q for model %q", lane, modelName)
		}
		result[modelName] = lane
	}
	*m = result
	return nil
}

func (m TokenModelLaneMap) Normalize() TokenModelLaneMap {
	if len(m) == 0 {
		return TokenModelLaneMap{}
	}
	result := make(TokenModelLaneMap, len(m))
	for modelName, lane := range m {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		lane = NormalizeChannelLane(lane)
		if !IsKnownChannelLane(lane) {
			continue
		}
		result[modelName] = lane
	}
	return result
}

// LaneForModel reports the pinned lane and whether this map pins one at all.
// The model name is matched as written first, then through the pricing
// normaliser, so a dated alias follows the lane picked for its base model.
func (m TokenModelLaneMap) LaneForModel(modelName string) (string, bool) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || len(m) == 0 {
		return ChannelLaneDirect, false
	}
	if lane, ok := m[modelName]; ok {
		lane = NormalizeChannelLane(lane)
		if IsKnownChannelLane(lane) {
			return lane, true
		}
	}
	if normalized := NormalizePricingModelName(modelName); normalized != "" && normalized != modelName {
		if lane, ok := m[normalized]; ok {
			lane = NormalizeChannelLane(lane)
			if IsKnownChannelLane(lane) {
				return lane, true
			}
		}
	}
	return ChannelLaneDirect, false
}

// LaneCandidatesForModel is the shared request/catalog lane policy. Cheap is
// a strict cost preference: it never falls back to another lane, including
// when its price is missing. Other custom lanes retain their direct fallback.
//
// A model pinned in modelLanes wins over its vendor default: the two are one
// policy read at every call site, so the gateway, the catalog and the connect
// dialog can never disagree about which lane a model takes.
func LaneCandidatesForModel(modelName string, modelLanes TokenModelLaneMap, vendorGroups TokenVendorGroupMap) ([]string, string, bool) {
	_, vendorPublicID, _ := GetModelVendorPublicMeta(modelName)
	desiredLane := NormalizeChannelLane(vendorGroups.LaneForVendor(vendorPublicID))
	_, vendorPinned := vendorGroups[vendorPublicID]
	pinned, modelPinned := modelLanes.LaneForModel(modelName)
	if modelPinned {
		desiredLane = pinned
	}
	if desiredLane == ChannelLaneAgent {
		if !IsValidChannelLane(desiredLane) {
			return nil, desiredLane, false
		}
		if _, hasPrice := GetActiveModelSellPrice(modelName, desiredLane); !hasPrice {
			return nil, desiredLane, false
		}
		return []string{desiredLane}, desiredLane, false
	}
	if desiredLane == ChannelLaneDirect || !IsValidChannelLane(desiredLane) {
		// A model offered only on cheap has no stable price to bill against.
		// Use its priced lane for an unpinned Key; an explicit direct choice
		// remains strict and will report that the chosen lane is unavailable.
		if !modelPinned && !vendorPinned {
			if _, hasDirect := GetActiveModelSellPrice(modelName, ChannelLaneDirect); !hasDirect {
				if _, hasCheap := GetActiveModelSellPrice(modelName, ChannelLaneAgent); hasCheap {
					return []string{ChannelLaneAgent}, ChannelLaneAgent, false
				}
			}
		}
		return []string{ChannelLaneDirect}, ChannelLaneDirect, false
	}
	if _, hasPrice := GetActiveModelSellPrice(modelName, desiredLane); !hasPrice {
		return []string{ChannelLaneDirect}, desiredLane, true
	}
	return []string{desiredLane, ChannelLaneDirect}, desiredLane, false
}

type Token struct {
	ProjectID          int    `json:"-" gorm:"index;not null;default:0"`
	ProjectVersion     int64  `json:"-" gorm:"not null;default:1"`
	RemovalOperationID string `json:"-" gorm:"size:64"`

	Id       int    `json:"-"`
	UserId   int    `json:"-" gorm:"index;uniqueIndex:idx_token_user_internal_scope,priority:1"`
	PublicID string `json:"id" gorm:"type:varchar(64);uniqueIndex"`
	Key      string `json:"key" gorm:"type:varchar(64);uniqueIndex"`
	// Use a column-level unique constraint here. Older PostgreSQL installations
	// may expose the original digest uniqueness as a column constraint rather
	// than an index; GORM's index tag attempts to drop a generated constraint
	// name that does not exist during AutoMigrate.
	KeyDigest     string    `json:"-" gorm:"type:varchar(64);unique;default:null"`
	KeyPrefix     string    `json:"key_prefix" gorm:"type:varchar(24);index;default:null"`
	KeySuffix     string    `json:"key_suffix" gorm:"type:varchar(8);default:null"`
	Status        int       `json:"status" gorm:"default:1"`
	Name          string    `json:"name" gorm:"index" `
	Product       string    `json:"product" gorm:"type:varchar(128);index;default:''"`
	InternalScope *string   `json:"-" gorm:"type:varchar(32);uniqueIndex:idx_token_user_internal_scope,priority:2"`
	Metadata      JSONValue `json:"metadata" gorm:"type:json"`
	CreatedTime   int64     `json:"created_time" gorm:"bigint"`
	// LastUsedAt is stamped when a request settles a charge on this Key.
	LastUsedAt                 int64                              `json:"last_used_at" gorm:"column:last_used_at;not null;default:0;index"`
	ExpiredTime                int64                              `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota                billing.MicroUSD                   `json:"remain_quota" gorm:"type:bigint;default:0"`
	UnlimitedQuota             bool                               `json:"unlimited_quota"`
	RateLimitsEnabled          bool                               `json:"rate_limits_enabled" gorm:"column:rate_limits_enabled;default:false"`
	RPMLimit                   int                                `json:"rpm_limit" gorm:"column:rpm_limit;default:0"`
	TPMLimit                   int                                `json:"tpm_limit" gorm:"column:tpm_limit;default:0"`
	ModelLimitsEnabled         bool                               `json:"model_limits_enabled"`
	ModelLimits                string                             `json:"model_limits" gorm:"type:varchar(1024);default:''"`
	AllowIps                   *string                            `json:"allow_ips" gorm:"default:''"`
	IPWhitelistEnabled         *bool                              `json:"ip_whitelist_enabled" gorm:"column:ip_whitelist_enabled"`
	IPBlacklist                *string                            `json:"ip_blacklist" gorm:"column:ip_blacklist;default:''"`
	IPBlacklistEnabled         bool                               `json:"ip_blacklist_enabled" gorm:"column:ip_blacklist_enabled;default:false"`
	UsedQuota                  billing.MicroUSD                   `json:"used_quota" gorm:"type:bigint;default:0"` // used quota
	Group                      string                             `json:"group" gorm:"default:'direct'"`
	VendorLimitsEnabled        bool                               `json:"vendor_limits_enabled"`
	VendorLimits               IntSlice                           `json:"vendor_limits" gorm:"type:json"`
	VendorGroups               TokenVendorGroupMap                `json:"vendor_groups" gorm:"type:json"`
	ModelLanes                 TokenModelLaneMap                  `json:"model_lanes" gorm:"type:json"`
	DecisionQuestionGeneration DecisionQuestionGenerationSettings `json:"decision_question_generation" gorm:"type:json"`
	// ReasoningEffortCap is the highest reasoning effort this Key may use; ""
	// means the Key adds no cap of its own and follows its member's.
	ReasoningEffortCap string `json:"reasoning_effort_cap" gorm:"type:varchar(16);default:''"`
	// ModelScopeVendors holds vendors this Key is scoped to as a whole, as
	// opposed to ModelLimits, which names individual models. The two compose as
	// a union: a model is allowed when its vendor is in scope OR its name is
	// listed. Naming the vendor is a standing decision — a model that vendor
	// ships next month is in scope without anyone editing the Key — which is
	// exactly what a list of names cannot express.
	ModelScopeVendors IntSlice `json:"model_scope_vendors" gorm:"type:json"`
	// UsageAlertPercent mails the payer once when the Key has spent this share
	// of its quota. 0 disables it.
	UsageAlertPercent int `json:"usage_alert_percent" gorm:"default:0"`
	// Note is the sentence a human reads to remember what this Key is for.
	// Tags are for machines reconciling spend; this is not.
	Note                  string         `json:"note" gorm:"type:varchar(256);default:''"`
	SearchLimitsEnabled   bool           `json:"search_limits_enabled" gorm:"column:search_limits_enabled;default:false"`
	SearchProviders       StringSlice    `json:"search_providers" gorm:"type:json"`
	SearchDomainAllowlist string         `json:"search_domain_allowlist" gorm:"type:varchar(2048);default:''"`
	DeletedAt             gorm.DeletedAt `gorm:"index"`
}

const tokenKeyStoredDigestPrefix = "digest:"

func (token *Token) Clean() {
	token.Key = ""
}

func (token *Token) IsInternal() bool {
	if token == nil {
		return false
	}
	if token.InternalScope != nil && strings.TrimSpace(*token.InternalScope) != "" {
		return true
	}
	return isHiddenTokenKind(token.Product, token.Name)
}

func excludeInternalTokens(query *gorm.DB) *gorm.DB {
	query = query.Where("internal_scope IS NULL")
	for _, kind := range extensions.hiddenTokens {
		query = query.Where("(product IS NULL OR name IS NULL OR product <> ? OR name <> ?)", kind.Product, kind.Name)
	}
	return query
}

// hiddenTokenIDs returns credentials that are intentionally hidden from
// the customer-facing API-key inventory. They still produce consume logs for
// billing, so dashboard "active key" counts must filter them too.
func hiddenTokenIDs() ([]int, error) {
	ids := []int{}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&Token{}) {
		return ids, nil
	}
	condition, args := hiddenTokenCondition()
	err := CoreDB().Unscoped().Model(&Token{}).
		Where(condition, args...).
		Pluck("id", &ids).Error
	return ids, err
}

func publicUserTokenQuery(userId int) *gorm.DB {
	return excludeInternalTokens(CoreDB().Model(&Token{})).Where("user_id = ?", userId)
}

func (token *Token) NormalizeRateLimits() {
	if token == nil {
		return
	}
	if token.RPMLimit < 0 {
		token.RPMLimit = 0
	}
	if token.TPMLimit < 0 {
		token.TPMLimit = 0
	}
	if !token.RateLimitsEnabled || (token.RPMLimit == 0 && token.TPMLimit == 0) {
		token.RateLimitsEnabled = false
		token.RPMLimit = 0
		token.TPMLimit = 0
	}
}

func (token *Token) NormalizeProductMetadata() error {
	if token == nil {
		return nil
	}
	token.Product = strings.TrimSpace(token.Product)
	if len(token.Product) > 128 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "product", "reason": "product is longer than 128 characters"})
	}
	raw := strings.TrimSpace(string(token.Metadata))
	if raw == "" || raw == "null" {
		token.Metadata = JSONValue([]byte("{}"))
		return nil
	}
	var metadata map[string]any
	if err := common.Unmarshal([]byte(raw), &metadata); err != nil {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "metadata", "reason": "metadata must be a JSON object"})
	}
	normalized, err := common.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(normalized) > 4096 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "metadata", "reason": "metadata is larger than 4096 bytes"})
	}
	token.Metadata = JSONValue(normalized)
	return nil
}

func TokenKeyDigest(raw string) string {
	key := common.NormalizeAPIKey(raw)
	if key == "" {
		return ""
	}
	return common.GenerateHMAC(key)
}

func tokenKeyLookupDigests(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	digests := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		digest := TokenKeyDigest(candidate)
		if digest == "" {
			continue
		}
		if _, ok := seen[digest]; ok {
			continue
		}
		seen[digest] = struct{}{}
		digests = append(digests, digest)
	}
	return digests
}

func tokenKeyPrefix(raw string) string {
	key := common.NormalizeAPIKey(raw)
	if key == "" {
		return ""
	}
	if len(key) <= len(common.APITokenPrefix)+4 {
		return key
	}
	return key[:len(common.APITokenPrefix)+4]
}

func tokenKeySuffix(raw string) string {
	key := common.NormalizeAPIKey(raw)
	if len(key) <= 4 {
		return key
	}
	return key[len(key)-4:]
}

func (token *Token) prepareKeyForStorage(clearKey string) error {
	clearKey = common.NormalizeAPIKey(clearKey)
	if clearKey == "" {
		return nil
	}
	digest := TokenKeyDigest(clearKey)
	if digest == "" {
		return errors.New("invalid token key")
	}
	token.KeyDigest = digest
	token.KeyPrefix = tokenKeyPrefix(clearKey)
	token.KeySuffix = tokenKeySuffix(clearKey)
	token.Key = clearKey
	return nil
}

func (token *Token) CacheDigest() string {
	if token == nil {
		return ""
	}
	if token.KeyDigest != "" {
		return token.KeyDigest
	}
	return TokenKeyDigest(token.Key)
}

// CanRevealKey excludes legacy digest-only rows and sanitized projections.
func (token *Token) CanRevealKey() bool {
	return token != nil && token.Key != "" && !strings.HasPrefix(token.Key, tokenKeyStoredDigestPrefix) && !strings.Contains(token.Key, "...") && !strings.Contains(token.Key, "*")
}

func (token *Token) DisplayKey() string {
	if token == nil {
		return ""
	}
	if token.KeyPrefix != "" || token.KeySuffix != "" {
		if token.KeyPrefix == "" {
			return "************" + token.KeySuffix
		}
		if token.KeySuffix == "" {
			return token.KeyPrefix + "************"
		}
		return token.KeyPrefix + "..." + token.KeySuffix
	}
	if strings.HasPrefix(token.Key, tokenKeyStoredDigestPrefix) {
		return "************"
	}
	return token.Key
}

func (token *Token) GetIpLimitsMap() map[string]any {
	ipLimitsMap := make(map[string]any)
	if !token.ShouldUseIPWhitelist() {
		return ipLimitsMap
	}
	return buildIPMap(token.AllowIps)
}

func (token *Token) GetIPBlacklistMap() map[string]any {
	// delete empty spaces
	//split with \n
	blacklistMap := make(map[string]any)
	if !token.ShouldUseIPBlacklist() {
		return blacklistMap
	}
	return buildIPMap(token.IPBlacklist)
}

func (token *Token) ShouldUseIPWhitelist() bool {
	if token.AllowIps == nil {
		return false
	}
	clean := strings.TrimSpace(*token.AllowIps)
	if clean == "" {
		return false
	}
	if token.IPWhitelistEnabled == nil {
		return true
	}
	return *token.IPWhitelistEnabled
}

func (token *Token) ShouldUseIPBlacklist() bool {
	if !token.IPBlacklistEnabled || token.IPBlacklist == nil {
		return false
	}
	return strings.TrimSpace(*token.IPBlacklist) != ""
}

func buildIPMap(raw *string) map[string]any {
	result := make(map[string]any)
	if raw == nil {
		return result
	}
	cleanIps := strings.ReplaceAll(*raw, " ", "")
	if cleanIps == "" {
		return result
	}
	ips := strings.Split(cleanIps, "\n")
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if common.IsIP(ip) {
			result[ip] = true
		}
	}
	return result
}

func (token *Token) GetVendorLimitSet() map[int]struct{} {
	set := make(map[int]struct{})
	if !token.VendorLimitsEnabled || len(token.VendorLimits) == 0 {
		return set
	}
	for _, id := range token.VendorLimits {
		if id <= 0 {
			continue
		}
		set[id] = struct{}{}
	}
	return set
}

func (token *Token) IsVendorLimited(vendorID int) bool {
	if vendorID == 0 || !token.VendorLimitsEnabled {
		return false
	}
	for _, id := range token.VendorLimits {
		if id == vendorID {
			return true
		}
	}
	return false
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	return GetAllUserTokensByStatus(userId, 0, startIdx, num)
}

func publicUserTokenStatusQuery(userId int, status int) *gorm.DB {
	query := publicUserTokenQuery(userId)
	if status > 0 {
		query = query.Where("status = ?", status)
	}
	return query
}

func GetAllUserTokensByStatus(userId int, status int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = publicUserTokenStatusQuery(userId, status).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// GetUserTokensAdmin returns a page of a single account's tokens for the
// admin console, newest first, with total. Internal extension tokens are
// excluded exactly as the self-service listing excludes them. Callers must
// project only the safe fields — never the key or its digest.
func GetUserTokensAdmin(userId int, startIdx int, num int) (tokens []*Token, total int64, err error) {
	query := publicUserTokenQuery(userId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = publicUserTokenQuery(userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, total, err
}

func SearchUserTokens(userId int, keyword string, token string) (tokens []*Token, err error) {
	return SearchUserTokensByStatus(userId, keyword, token, 0)
}

func SearchUserTokensByStatus(userId int, keyword string, token string, status int) (tokens []*Token, err error) {
	err = FilterTokenSearch(publicUserTokenStatusQuery(userId, status), keyword, token).Find(&tokens).Error
	return tokens, err
}

// FilterTokenSearch preserves single-box name and credential hint matching on
// an already authorized query; it must never replace its ownership predicates.
func FilterTokenSearch(query *gorm.DB, keyword, token string) *gorm.DB {
	// A stray leading or trailing space is never part of a name or key, but it
	// turns "name LIKE '%abc %'" into a search that matches nothing.
	keyword = strings.TrimSpace(keyword)
	token = strings.TrimSpace(token)
	if token == "" && keyword != "" {
		// The console searches keys from a single box, so a bare keyword has to
		// mean "name or key" rather than "name only". A key is never shown in
		// full after creation, so what a reader can actually recall and type is
		// its prefix or its tail — both are matched here alongside the name.
		candidates := common.APIKeyLookupCandidates(keyword)
		digests := tokenKeyLookupDigests(candidates)
		normalized := common.NormalizeAPIKey(keyword)
		query = query.Where(
			"name LIKE ? OR "+commonKeyCol+" LIKE ? OR "+commonKeyCol+" IN ? OR key_digest IN ? OR key_prefix LIKE ? OR key_suffix LIKE ?",
			"%"+keyword+"%",
			"%"+normalized+"%",
			candidates,
			digests,
			"%"+normalized+"%",
			"%"+normalized+"%",
		)
		return query
	}
	query = query.Where("name LIKE ?", "%"+keyword+"%")
	if token != "" {
		candidates := common.APIKeyLookupCandidates(token)
		digests := tokenKeyLookupDigests(candidates)
		token = common.NormalizeAPIKey(token)
		query = query.Where(
			commonKeyCol+" LIKE ? OR "+commonKeyCol+" IN ? OR key_digest IN ? OR key_prefix LIKE ? OR key_suffix LIKE ?",
			"%"+token+"%",
			candidates,
			digests,
			"%"+token+"%",
			"%"+token+"%",
		)
	}
	return query
}

// ErrInvalidAPIKey is an unknown, missing or internal-only credential. The
// gateway answers invalid_api_key for it and never exposes a project.
var ErrInvalidAPIKey = types.Err(types.ErrorCodeInvalidAPIKey)

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrInvalidAPIKey
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if err := ValidateTokenState(token, key); err != nil {
			return token, err
		}
		return token, nil
	}
	return nil, ErrInvalidAPIKey
}

func ValidateTokenState(token *Token, keyLabel string) error {
	if token == nil {
		return ErrInvalidAPIKey
	}
	if token.IsInternal() {
		return types.WrapErr(errors.New("internal token cannot be used as an API key"), types.ErrorCodeInvalidAPIKey)
	}
	return validateTokenOperationalState(token, keyLabel)
}

func ValidateSessionTokenState(token *Token, keyLabel string) error {
	if token == nil {
		return ErrInvalidAPIKey
	}
	return validateTokenOperationalState(token, keyLabel)
}

// Token state sentinels. They are catalog errors, so a console route writes
// them with httperr.Write, and they compare by code with errors.Is.
//
// ErrTokenAccessInactive has its own internal code (clients see
// invalid_api_key), so an ordinary invalid_api_key never matches it: the
// gateway relies on that to tell a verified-but-inactive Key from an unknown
// credential.
var (
	ErrTokenBudgetExhausted = types.Err(types.ErrorCodeKeyBudgetExceeded)
	ErrTokenAccessInactive  = types.Err(types.ErrorCodeKeyInactive)
)

// ErrTokenExpired and ErrTokenDisabled say why a Key is inactive. Both carry
// ErrTokenAccessInactive as their cause, so every
// errors.Is(err, ErrTokenAccessInactive) check keeps matching; the gateway
// uses them to tell the caller whether to extend the Key or enable it.
var (
	ErrTokenExpired  = &types.APIError{Code: types.ErrorCodeTokenExpired, Cause: ErrTokenAccessInactive}
	ErrTokenDisabled = &types.APIError{Code: types.ErrorCodeTokenDisabled, Cause: ErrTokenAccessInactive}
)

func validateTokenOperationalState(token *Token, keyLabel string) error {
	if token.Status == common.TokenStatusExhausted {
		return ErrTokenBudgetExhausted
	} else if token.Status == common.TokenStatusExpired {
		return ErrTokenExpired
	}
	if token.Status != common.TokenStatusEnabled {
		return ErrTokenDisabled
	}
	if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
		if !common.RedisEnabled {
			token.Status = common.TokenStatusExpired
			err := token.SelectUpdate()
			if err != nil {
				common.SysLog("failed to update token status" + err.Error())
			}
		}
		return ErrTokenExpired
	}
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		if !common.RedisEnabled {
			// in this case, we can make sure the token is exhausted
			token.Status = common.TokenStatusExhausted
			err := token.SelectUpdate()
			if err != nil {
				common.SysLog("failed to update token status" + err.Error())
			}
		}
		return ErrTokenBudgetExhausted
	}
	return nil
}

// GetTokenByIds returns the Key `userId` owns.
func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
	}
	var token Token
	err := excludeInternalTokens(CoreDB()).First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetSessionTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
	}
	token := Token{Id: id, UserId: userId}
	err := CoreDB().First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
	}
	token := Token{Id: id}
	// ID lookups are control-plane reads, not authentication hot-path reads.
	// Do not warm the key-addressed token cache here: the digest is only known
	// after the database read, which makes a deletion-safe generation capture
	// impossible without adding another query.
	err := CoreDB().First(&token, "id = ?", id).Error
	return &token, err
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	candidates := common.APIKeyLookupCandidates(key)
	if len(candidates) == 0 {
		return nil, ErrInvalidAPIKey
	}
	key = candidates[0]
	digests := tokenKeyLookupDigests(candidates)
	var tokenFillGuard *tokenCacheFillGuard
	defer func() {
		// Quota settlement snapshots this cache later in the same request. Warm
		// only a missing entry before returning so concurrent requests cannot
		// overwrite an already-adjusted quota snapshot with stale CoreDB() values.
		if shouldUpdateRedis(fromDB, err) && token != nil {
			if cacheErr := cacheSetTokenIfMissing(*token, tokenFillGuard); cacheErr != nil {
				common.SysLog("failed to update user status cache: " + cacheErr.Error())
			}
		}
	}()
	if !fromDB && common.RedisEnabled {
		// Try Redis first
		cacheTimer := common.StartHotPathStage(common.HotPathStageAuthTokenCache)
		token, err := cacheGetTokenByKey(key)
		cacheTimer.Finish(err)
		if err == nil {
			common.IncrementHotPathCounter(common.HotPathCounterAuthTokenCacheHit)
			return token, nil
		}
		common.IncrementHotPathCounter(common.HotPathCounterAuthTokenCacheMiss)
		// Don't return error - fall through to CoreDB()
	}
	fromDB = true
	if common.RedisEnabled {
		tokenFillGuard, err = captureTokenCacheFillGuard(TokenKeyDigest(key))
		if err != nil {
			common.SysLog("failed to capture token cache fill guard: " + err.Error())
			tokenFillGuard = nil
			err = nil
		}
	}
	common.IncrementHotPathCounter(common.HotPathCounterAuthTokenDBFallback)
	dbTimer := common.StartHotPathStage(common.HotPathStageAuthTokenDBFallback)
	err = CoreDB().Where("key_digest IN ? OR "+commonKeyCol+" IN ?", digests, candidates).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && CoreDB().Migrator().HasTable(&TokenCredential{}) {
		var credential TokenCredential
		aliasErr := CoreDB().Where("digest IN ? AND expires_at > ?", digests, time.Now().Unix()).First(&credential).Error
		if aliasErr == nil {
			token, err = ResolveTokenCredential(credential.TokenID, key, time.Now())
		} else if !errors.Is(aliasErr, gorm.ErrRecordNotFound) {
			err = aliasErr
		}
	}
	dbTimer.Finish(err)
	if err == nil && token != nil {
		if token.KeyDigest == "" {
			backfillTokenKeyDigest(token.Id, key)
		}
		token.Key = key
	}
	return token, err
}

func (token *Token) Insert() error {
	token.NormalizeRateLimits()
	if err := token.NormalizeProductMetadata(); err != nil {
		return err
	}
	clearKey := token.Key
	if err := token.prepareKeyForStorage(clearKey); err != nil {
		return err
	}
	err := CoreDB().Create(token).Error
	if clearKey != "" {
		token.Key = common.NormalizeAPIKey(clearKey)
	}
	return err
}

// TokenMutationCommittedError means PostgreSQL is authoritative and already
// contains the mutation, but the synchronous cache fence did not complete.
// Callers can still record the audit event while returning an operational
// error instead of falsely reporting that every replica has observed it.
type TokenMutationCommittedError struct {
	Operation string
	Cause     error
}

func (err *TokenMutationCommittedError) Error() string {
	return fmt.Sprintf("token %s committed but cache fence failed: %v", err.Operation, err.Cause)
}

func (err *TokenMutationCommittedError) Unwrap() error {
	return err.Cause
}

func IsTokenMutationCommittedError(err error) bool {
	var committedErr *TokenMutationCommittedError
	return errors.As(err, &committedErr)
}

func synchronizeTokenCachesAfterUpdate(token *Token) error {
	if !common.RedisEnabled || token == nil {
		return nil
	}

	// The database update is already committed. Invalidate the projection on
	// both sides of deleting the mutable token snapshot. The second generation
	// bump rejects a projection fill that captured the first generation while
	// it was still able to read the old token hash.
	if invalidationErr := invalidateMutableTokenSnapshots(token); invalidationErr != nil {
		return invalidationErr
	}

	// Warming is safe only after both old snapshots are gone. If warming fails,
	// delete the possibly partial hash again so requests fall back to the
	// authoritative database rather than trusting an incomplete cache entry.
	guard, err := captureTokenCacheFillGuard(token.CacheDigest())
	if err == nil {
		err = cacheSetTokenIfMissing(*token, guard)
	}
	if err != nil {
		cleanupErr := cacheDeleteTokenForToken(token)
		if cleanupErr == nil {
			common.SysLog("failed to warm token cache after synchronous invalidation; requests will fall back to database: " + err.Error())
			return nil
		}
		return errors.Join(fmt.Errorf("token snapshot refresh failed: %w", err), wrapTokenCacheFenceError("token snapshot cleanup", cleanupErr))
	}
	return nil
}

func synchronizeTokenCachesAfterDelete(token *Token) error {
	if !common.RedisEnabled || token == nil {
		return nil
	}
	return invalidateMutableTokenSnapshots(token)
}

func invalidateMutableTokenSnapshots(token *Token) error {
	if token == nil {
		return nil
	}
	return cacheInvalidateTokenSnapshotsWithFence([]string{token.CacheDigest()})
}

func wrapTokenCacheFenceError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() error {
	token.NormalizeRateLimits()
	if err := token.NormalizeProductMetadata(); err != nil {
		return err
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error { return updateTokenFieldsTx(tx, token) })
	if err != nil {
		return err
	}
	if err := synchronizeTokenCachesAfterUpdate(token); err != nil {
		return &TokenMutationCommittedError{Operation: "update", Cause: err}
	}
	return nil
}

func updateTokenFieldsTx(tx *gorm.DB, token *Token) error {
	if err := tx.Model(token).Select("name", "product", "metadata", "status", "expired_time", "remain_quota", "unlimited_quota",
		"rate_limits_enabled", "rpm_limit", "tpm_limit",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "ip_whitelist_enabled",
		"ip_blacklist", "ip_blacklist_enabled", "vendor_limits_enabled", "vendor_limits", "vendor_groups", "model_lanes",
		"search_limits_enabled", "search_providers", "search_domain_allowlist",
		"model_scope_vendors", "usage_alert_percent", "note", "reasoning_effort_cap", "decision_question_generation").Updates(token).Error; err != nil {
		return err
	}
	return tx.Model(token).UpdateColumn("project_version", gorm.Expr("project_version + 1")).Error
}

func (token *Token) SelectUpdate() error {
	// This can update zero values
	if err := CoreDB().Model(token).Select("status").Updates(token).Error; err != nil {
		return err
	}
	if err := synchronizeTokenCachesAfterUpdate(token); err != nil {
		return &TokenMutationCommittedError{Operation: "select update", Cause: err}
	}
	return nil
}

func (token *Token) Delete() error {
	if err := CoreDB().Delete(token).Error; err != nil {
		return err
	}
	if err := synchronizeTokenCachesAfterDelete(token); err != nil {
		return &TokenMutationCommittedError{Operation: "deletion", Cause: err}
	}
	return nil
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func TokenModelLimitAllows(limits map[string]bool, modelName string) bool {
	if len(limits) == 0 {
		return false
	}
	if limits[modelName] {
		return true
	}
	normalizedModelName := NormalizePricingModelName(modelName)
	if limits[normalizedModelName] {
		return true
	}
	return false
}

// GetModelScopeVendorSet is the vendor half of a Key's model scope, as a set.
func (token *Token) GetModelScopeVendorSet() map[int]struct{} {
	if len(token.ModelScopeVendors) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(token.ModelScopeVendors))
	for _, id := range token.ModelScopeVendors {
		set[id] = struct{}{}
	}
	return set
}

// TokenModelScopeAllows reports whether a Key with its model scope switched on
// may call this model. The two halves are a union, not an intersection: naming
// a vendor admits everything it ships, now and later, and naming a model admits
// that one whatever its vendor. A Key that has neither admits nothing, which is
// the same deny-all TokenModelLimitAllows has always meant.
func TokenModelScopeAllows(limits map[string]bool, vendors map[int]struct{}, modelName string) bool {
	if TokenModelLimitAllows(limits, modelName) {
		return true
	}
	if len(vendors) == 0 {
		return false
	}
	vendorID, _ := GetModelVendorMeta(modelName)
	if vendorID <= 0 {
		return false
	}
	_, ok := vendors[vendorID]
	return ok
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
	}
	token := Token{Id: id, UserId: userId}
	err = excludeInternalTokens(CoreDB()).Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	return CountUserTokensByStatus(userId, 0)
}

func CountUserTokensByStatus(userId int, status int) (int64, error) {
	var total int64
	err := publicUserTokenStatusQuery(userId, status).Count(&total).Error
	return total, err
}

// CountUsableUserTokens excludes hidden internal credentials as well as keys
// that cannot authenticate a new public API request.
func CountUsableUserTokens(userId int) (int64, error) {
	var total int64
	err := publicUserTokenQuery(userId).
		Where("status = ?", common.TokenStatusEnabled).
		Where("expired_time = ? OR expired_time > ?", -1, common.GetTimestamp()).
		Where("unlimited_quota = ? OR remain_quota > 0", true).
		Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "ids"})
	}

	tx := CoreDB().Begin()

	var tokens []Token
	if err := excludeInternalTokens(tx).Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := excludeInternalTokens(tx).Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	digests := make([]string, 0, len(tokens))
	for index := range tokens {
		digests = append(digests, tokens[index].CacheDigest())
	}
	cacheErr := cacheInvalidateTokenSnapshotsWithFence(digests)
	if cacheErr != nil {
		return len(tokens), &TokenMutationCommittedError{Operation: "batch deletion", Cause: cacheErr}
	}

	return len(tokens), nil
}

func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}

	var tokens []Token
	if err := CoreDB().Unscoped().
		Select("id", commonKeyCol, "key_digest").
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}

	digests := make([]string, 0, len(tokens))
	for index := range tokens {
		token := &tokens[index]
		if token.Key == "" && token.KeyDigest == "" {
			continue
		}
		digests = append(digests, token.CacheDigest())
	}
	return cacheInvalidateTokenSnapshotsWithFence(digests)
}

func InvalidateTokenCache(key string) error {
	if !common.RedisEnabled || key == "" {
		return nil
	}
	return cacheInvalidateTokenSnapshotsWithFence([]string{TokenKeyDigest(key)})
}
