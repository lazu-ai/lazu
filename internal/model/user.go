package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// User if you add sensitive fields, don't forget to clean them in setupLogin function.
// Otherwise, the sensitive information will be saved on local storage in plain text!
type User struct {
	CreditSeq     int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	VerifiedEmail string `json:"-" gorm:"size:254;not null;default:''"`
	Id            int    `json:"-"`
	PublicID      string `json:"id" gorm:"type:varchar(64);uniqueIndex"`
	Username      string `json:"username" gorm:"unique;index" validate:"max=20"`
	// UsernameChangedAt stamps the last self-service rename so the change
	// cooldown can be enforced. Zero means the handle has never been changed.
	UsernameChangedAt int64 `json:"-" gorm:"column:username_changed_at;not null;default:0"`
	// TermsVersion and TermsAgreedAt record which terms this account accepted
	// and when. A stored version and timestamp is the evidence a consent
	// checkbox was never able to produce; the sign-up form only shows the
	// notice. Empty on accounts created before consent was recorded.
	TermsVersion     string           `json:"-" gorm:"column:terms_version;size:64;not null;default:''"`
	TermsAgreedAt    int64            `json:"-" gorm:"column:terms_agreed_at;not null;default:0"`
	Password         string           `json:"password" gorm:"not null;" validate:"min=8,max=64"`
	SessionVersion   int              `json:"-" gorm:"column:session_version;not null;default:0"`
	NeedsPassword    bool             `json:"-" gorm:"column:needs_password;not null;default:false"`
	OriginalPassword string           `json:"original_password" gorm:"-:all"` // this field is only for Password change verification, don't save it to database!
	DisplayName      string           `json:"display_name" gorm:"index" validate:"max=20"`
	Role             int              `json:"role" gorm:"type:int;default:1"`   // admin, common
	Status           int              `json:"status" gorm:"type:int;default:1"` // enabled, disabled
	Email            string           `json:"email" gorm:"index" validate:"max=50"`
	VerificationCode string           `json:"verification_code" gorm:"-:all"`                                    // this field is only for Email verification, don't save it to database!
	AccessToken      *string          `json:"access_token" gorm:"type:char(32);column:access_token;uniqueIndex"` // this token is for system management
	Quota            billing.MicroUSD `json:"quota" gorm:"type:bigint;default:0"`
	UsedQuota        billing.MicroUSD `json:"used_quota" gorm:"type:bigint;default:0;column:used_quota"` // used quota
	StorageBytesUsed int64            `json:"storage_bytes_used" gorm:"type:bigint;default:0;column:storage_bytes_used"`
	HasToppedUp      bool             `json:"has_topped_up" gorm:"default:false;not null"`
	// PricePlanID is the 专属折扣 plan the account is billed under; 0 = standard prices.
	PricePlanID int `json:"price_plan_id" gorm:"not null;default:0;index"`
	// RPMLimit / TPMLimit override the site-wide rate limit for this account; 0 = default.
	RPMLimit  int            `json:"rpm_limit" gorm:"not null;default:0"`
	TPMLimit  int            `json:"tpm_limit" gorm:"not null;default:0"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
	// CreatedAt is when the account was registered (unix seconds). Accounts
	// created before schema version 15 were backfilled from the earliest
	// evidence available; CreatedAtSource says which, so growth metrics can
	// count only exact registrations. BeforeCreate stamps it: GORM leaves a
	// zero field that carries a default out of the INSERT, so autoCreateTime
	// alone never fired and every registration landed as 0.
	CreatedAt       int64  `json:"created_at" gorm:"column:created_at;not null;default:0;index"`
	CreatedAtSource string `json:"created_at_source" gorm:"column:created_at_source;size:24;not null;default:''"`
	// FirstRequestAt / LastRequestAt / FirstTopupAt are stamped when a request
	// settles or a top-up completes. They outlive log retention, which is
	// what activation, churn and first-purchase metrics need.
	FirstRequestAt int64  `json:"first_request_at" gorm:"column:first_request_at;not null;default:0"`
	LastRequestAt  int64  `json:"last_request_at" gorm:"column:last_request_at;not null;default:0;index"`
	FirstTopupAt   int64  `json:"first_topup_at" gorm:"column:first_topup_at;not null;default:0"`
	Setting        string `json:"setting" gorm:"type:text;column:setting"`
	Remark         string `json:"remark,omitempty" gorm:"type:varchar(255)" validate:"max=255"`
	// HomeSurface is the product the account signed up through: '' for the
	// console, or a surface an extension registers. Mail links and the
	// post-login landing follow it (see user_surface.go).
	HomeSurface string `json:"home_surface" gorm:"column:home_surface;size:16;not null;default:''"`
}

func (user *User) ToBaseUser() *UserBase {
	cache := &UserBase{
		Id:            user.Id,
		Quota:         user.Quota,
		Status:        user.Status,
		Username:      user.Username,
		Setting:       user.Setting,
		Email:         user.Email,
		NeedsPassword: user.NeedsPassword,
		HasToppedUp:   user.HasToppedUp,
		PricePlanID:   user.PricePlanID,
		RPMLimit:      user.RPMLimit,
		TPMLimit:      user.TPMLimit,
	}
	return cache
}

func (user *User) GetAccessToken() string {
	if user.AccessToken == nil {
		return ""
	}
	return *user.AccessToken
}

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}

func (user *User) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := json.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	// Credential state is authoritative in its dedicated column. Keeping it
	// out of the general-purpose JSON prevents stale preference writes from
	// turning a completed social account back into a first-password account.
	setting.NeedsPassword = user.NeedsPassword
	return setting
}

func (user *User) SetSetting(setting dto.UserSetting) {
	// Social/OIDC account construction sets this before the row has an ID. Once
	// persisted, only credential transactions may change the dedicated column.
	if user.Id == 0 && setting.NeedsPassword {
		user.NeedsPassword = true
	}
	setting.NeedsPassword = false
	settingBytes, err := json.Marshal(setting)
	if err != nil {
		common.SysLog("failed to marshal setting: " + err.Error())
		return
	}
	user.Setting = string(settingBytes)
}

// 根据用户角色生成默认的边栏配置
func generateDefaultSidebarConfigForRole(userRole int) string {
	defaultConfig := map[string]interface{}{}

	// 工作区 - 所有用户都可以访问
	defaultConfig["workspace"] = map[string]interface{}{
		"enabled":       true,
		"detail":        true,
		"model_market":  true,
		"token":         true,
		"search":        false,
		"usage":         true,
		"key_inspector": false,
	}

	// 我的账户 - 所有用户都可以访问
	defaultConfig["account"] = map[string]interface{}{
		"enabled":  true,
		"personal": true,
	}

	// 管理员区域 - 只有管理员可见，普通用户不包含 admin 区域
	if userRole >= common.RoleRootUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":        true,
			"channel":        true,
			"search_backend": true,
			"models":         true,
			"user":           true,
			"setting":        true,
		}
	}

	// 转换为JSON字符串
	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("Failed to generate default sidebar configuration: " + err.Error())
		return ""
	}

	return string(configBytes)
}

// UserListOptions carries the optional server-side filter/sort the admin
// console asks for. The zero value reproduces the historical behaviour
// (no status filter, newest first), so existing callers are unaffected.
type UserListOptions struct {
	// Status is "" (any), "enabled" or "disabled".
	Status string
	// Order is "" (id desc), "balance_asc", "spend_desc" or "credit_desc".
	Order string
	// Role is "" (any) or an exact role string ("1"/"100").
	Role string
	// PricePlan is "" (any), "any" (on some plan), "none", or a plan id.
	PricePlan string
	// Provider is "" (any), a supported user identity provider, or
	// "unbound" for accounts without an external identity row.
	Provider string
	// ExcludeDeleted keeps customer-ledger rows on the same population as
	// the scoped summary facts while preserving legacy admin-list behavior
	// for callers that omit this option.
	ExcludeDeleted bool
	// Filter is "" (none) or one of the console's account cohorts:
	// "active_30d", "topped_up", "low_balance_active",
	// "zero_topped_up", "never_called".
	Filter string
}

// LowBalanceMicrousd is the balance floor behind the console's 低余额
// cohort: an account is "low balance" once its wallet drops below $5.
const LowBalanceMicrousd = 5_000_000

// userActiveWindowDays mirrors the console's trailing activity window and
// is used by the model-side cohort filters that need "active in the last
// 30 days" without importing the controller's copy of the constant.
const userActiveWindowDays = 30

const activeConsumerBatchSize = 500

// forEachActiveConsumerIDBatch streams distinct active account ids out of the
// log database with keyset pagination. Keeping each primary-CoreDB() IN clause below
// the database parameter limit matters on large deployments where the active
// cohort can contain tens of thousands of accounts.
func forEachActiveConsumerIDBatch(since int64, visit func([]int) error) error {
	lastID := -1
	for {
		var ids []int
		if err := TelemetryDB().Model(&Log{}).
			Select("user_id").
			Where("type = ? AND created_at >= ? AND user_id > ?", LogTypeConsume, since, lastID).
			Group("user_id").
			Order("user_id ASC").
			Limit(activeConsumerBatchSize).
			Pluck("user_id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := visit(ids); err != nil {
			return err
		}
		lastID = ids[len(ids)-1]
		if len(ids) < activeConsumerBatchSize {
			return nil
		}
	}
}

func isActiveUserFilter(filter string) bool {
	return filter == "active_30d" || filter == "low_balance_active"
}

func applyUserRoleFilter(query *gorm.DB, role string) *gorm.DB {
	switch role {
	case "1", "100":
		return query.Where("role = ?", role)
	default:
		return query
	}
}

// userListScopes resolves predicates that live in the primary database. The
// cross-database active-account intersection is applied in bounded batches by
// getActiveUsersPage.
func userListScopes(opts UserListOptions) (func(*gorm.DB) *gorm.DB, error) {
	return func(query *gorm.DB) *gorm.DB {
		query = applyUserStatusFilter(query, opts.Status)
		query = applyUserRoleFilter(query, opts.Role)
		switch opts.PricePlan {
		case "":
		case "any":
			query = query.Where("price_plan_id > 0")
		case "none":
			query = query.Where("price_plan_id = 0")
		default:
			if planID, err := strconv.Atoi(opts.PricePlan); err == nil {
				query = query.Where("price_plan_id = ?", planID)
			}
		}
		switch opts.Provider {
		case "google", "github", "oidc":
			if common.AuthProviderEnabled(opts.Provider) {
				query = query.Where("id IN (?)", CoreDB().Model(&UserIdentity{}).
					Select("user_id").Where("provider = ?", opts.Provider))
			} else {
				query = query.Where("1 = 0")
			}
		case "email":
			if common.AuthProviderEnabled("email") {
				query = query.Where("TRIM(email) <> ''")
			} else {
				query = query.Where("1 = 0")
			}
		case "password":
			if common.AuthProviderEnabled("password") {
				query = query.Where("needs_password = ?", false)
			} else {
				query = query.Where("1 = 0")
			}
		case "unbound":
			// "Unbound" means no login method is usable now, not merely no
			// historical identity row. Keep this predicate aligned with the
			// capability-gated projection in controller.loginProvidersForUser.
			enabledIdentityProviders := make([]string, 0, 3)
			for _, provider := range []string{"google", "github", "oidc"} {
				if common.AuthProviderEnabled(provider) {
					enabledIdentityProviders = append(enabledIdentityProviders, provider)
				}
			}
			if len(enabledIdentityProviders) > 0 {
				query = query.Where("id NOT IN (?)", CoreDB().Model(&UserIdentity{}).
					Select("user_id").Where("provider IN ?", enabledIdentityProviders))
			}
			if common.AuthProviderEnabled("email") {
				query = query.Where("TRIM(email) = ''")
			}
			if common.AuthProviderEnabled("password") {
				query = query.Where("needs_password = ?", true)
			}
		}
		switch opts.Filter {
		case "active_30d":
			query = query.Where("role = ?", common.RoleCommonUser)
		case "topped_up":
			query = query.Where("role = ? AND has_topped_up = ?", common.RoleCommonUser, true)
		case "low_balance_active":
			// The console's cohort tiles count customers (role=1) only, so
			// the cohort filters carry the same role predicate to keep the
			// filtered list aligned with the tile counts.
			query = query.Where("role = ? AND quota < ? AND status = ?", common.RoleCommonUser, LowBalanceMicrousd, common.UserStatusEnabled)
		case "zero_topped_up":
			query = query.Where("role = ? AND quota <= ? AND has_topped_up = ?", common.RoleCommonUser, 0, true)
		case "never_called":
			query = query.Where("role = ? AND first_request_at = ?", common.RoleCommonUser, 0)
		case "rate_limit_override":
			query = query.Where("rpm_limit > 0 OR tpm_limit > 0")
		}
		return query
	}, nil
}

func sortUsersForList(users []*User, order string) {
	sort.SliceStable(users, func(i, j int) bool {
		switch order {
		case "credit_desc":
			// Everything an account was ever credited: balance plus spend.
			left := users[i].Quota + users[i].UsedQuota
			right := users[j].Quota + users[j].UsedQuota
			if left != right {
				return left > right
			}
		case "balance_asc":
			if users[i].Quota != users[j].Quota {
				return users[i].Quota < users[j].Quota
			}
		case "spend_desc":
			if users[i].UsedQuota != users[j].UsedQuota {
				return users[i].UsedQuota > users[j].UsedQuota
			}
		}
		return users[i].Id > users[j].Id
	})
}

// getActiveUsersPage intersects TelemetryDB() activity with primary-CoreDB() account
// predicates without ever binding the full active population in one query.
// Only the best start+pageSize candidates are retained in memory.
func getActiveUsersPage(startIdx, num int, opts UserListOptions, buildQuery func(*gorm.DB) *gorm.DB) ([]*User, int64, error) {
	scope, err := userListScopes(opts)
	if err != nil {
		return nil, 0, err
	}
	keep := startIdx + num
	if keep < 0 {
		keep = 0
	}
	var candidates []*User
	var total int64
	since := time.Now().AddDate(0, 0, -userActiveWindowDays).Unix()
	err = forEachActiveConsumerIDBatch(since, func(ids []int) error {
		base := scope(buildQuery(userListBaseQuery(CoreDB(), opts))).Where("id IN ?", ids)
		var count int64
		if err := base.Count(&count).Error; err != nil {
			return err
		}
		total += count
		if keep == 0 || count == 0 {
			return nil
		}
		var batch []*User
		if err := scope(buildQuery(userListBaseQuery(CoreDB(), opts))).
			Where("id IN ?", ids).
			Order(userListOrderClause(opts.Order)).
			Limit(keep).
			Omit("password").
			Find(&batch).Error; err != nil {
			return err
		}
		candidates = append(candidates, batch...)
		sortUsersForList(candidates, opts.Order)
		if len(candidates) > keep {
			candidates = candidates[:keep]
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if startIdx >= len(candidates) || num <= 0 {
		return []*User{}, total, nil
	}
	end := startIdx + num
	if end > len(candidates) {
		end = len(candidates)
	}
	return candidates[startIdx:end], total, nil
}

func userListBaseQuery(tx *gorm.DB, opts UserListOptions) *gorm.DB {
	if opts.ExcludeDeleted {
		return tx.Model(&User{})
	}
	return tx.Unscoped().Model(&User{})
}

// userListOrderClause maps the console's sort tokens onto real columns.
// Anything unrecognised falls back to newest-first so a bad query string
// can never turn into raw SQL.
func userListOrderClause(order string) string {
	switch order {
	case "balance_asc":
		return "quota asc, id desc"
	case "spend_desc":
		return "used_quota desc, id desc"
	case "credit_desc":
		return "(quota + used_quota) desc, id desc"
	default:
		return "id desc"
	}
}

func applyUserStatusFilter(query *gorm.DB, status string) *gorm.DB {
	switch status {
	case "enabled":
		return query.Where("status = ?", common.UserStatusEnabled)
	case "disabled":
		// Only 1/2 are ever written, but "not enabled" is the honest
		// predicate behind the console's 已冻结 badge.
		return query.Where("status <> ?", common.UserStatusEnabled)
	default:
		return query
	}
}

func GetAllUsersFiltered(pageInfo *common.PageInfo, opts UserListOptions) (users []*User, total int64, err error) {
	if isActiveUserFilter(opts.Filter) {
		return getActiveUsersPage(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), opts, func(query *gorm.DB) *gorm.DB { return query })
	}
	scope, err := userListScopes(opts)
	if err != nil {
		return nil, 0, err
	}

	// Start transaction
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Get total count within transaction
	err = scope(userListBaseQuery(tx, opts)).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated users within same transaction
	err = scope(userListBaseQuery(tx, opts)).
		Order(userListOrderClause(opts.Order)).
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Omit("password").
		Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

func SearchUsers(keyword string, startIdx int, num int) ([]*User, int64, error) {
	return SearchUsersFiltered(keyword, startIdx, num, UserListOptions{})
}

func applyUserSearch(query *gorm.DB, keyword string) *gorm.DB {
	likeCondition := "public_id = ? OR username LIKE ? OR email LIKE ? OR display_name LIKE ?"
	args := []interface{}{keyword, "%" + keyword + "%", "%" + keyword + "%", "%" + keyword + "%"}
	if keywordInt, err := strconv.Atoi(strings.TrimPrefix(keyword, "#")); err == nil {
		likeCondition = "id = ? OR " + likeCondition
		args = append([]interface{}{keywordInt}, args...)
	}
	return query.Where(likeCondition, args...)
}

func SearchUsersFiltered(keyword string, startIdx int, num int, opts UserListOptions) ([]*User, int64, error) {
	keyword = strings.TrimSpace(keyword)
	if isActiveUserFilter(opts.Filter) {
		return getActiveUsersPage(startIdx, num, opts, func(query *gorm.DB) *gorm.DB {
			return applyUserSearch(query, keyword)
		})
	}
	var users []*User
	var total int64
	var err error

	scope, err := userListScopes(opts)
	if err != nil {
		return nil, 0, err
	}

	// 开始事务
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 构建基础查询
	query := userListBaseQuery(tx, opts)

	query = applyUserSearch(query, keyword)

	query = scope(query)

	// 获取总数
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = query.Omit("password").Order(userListOrderClause(opts.Order)).Limit(num).Offset(startIdx).Find(&users).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// GetCustomerByIdentifier resolves a ledger deep link in one indexed lookup.
// It intentionally accepts only customer usernames/public ids, never fuzzy
// matches or privileged platform-member rows.
func GetCustomerByIdentifier(identifier string) (*User, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, ErrUserNotFound
	}
	var user User
	err := CoreDB().Omit("password").Where("role = ? AND (username = ? OR public_id = ?)", common.RoleCommonUser, identifier, identifier).
		First(&user).Error
	return &user, userLookupErr(err)
}

// UserUsageSummary carries the two per-account aggregates the console's
// organization ledger needs but the users table does not store: how many
// API keys the account holds, and what it actually spent inside a recent
// window — as opposed to UsedQuota, which is lifetime spend.
type UserUsageSummary struct {
	TokenCount         int64
	SpendMicrousd      billing.MicroUSD
	RequestCount       int64
	LastUsedAt         int64
	TopupTotalMicrousd billing.MicroUSD
	TopFramework       string
	TopCountry         string
}

// GetUserUsageSummaries aggregates only over the ids on the caller's
// current page, so the cost is bounded by page size rather than table
// size. Both queries filter on an indexed user_id column.
func GetUserUsageSummaries(userIDs []int, spendSince int64) (map[int]UserUsageSummary, error) {
	summaries := make(map[int]UserUsageSummary, len(userIDs))
	if len(userIDs) == 0 {
		return summaries, nil
	}

	type aggregateRow struct {
		UserId int
		Total  int64
	}

	var tokenRows []aggregateRow
	if err := excludeInternalTokens(CoreDB().Model(&Token{})).
		Select("user_id, count(*) as total").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&tokenRows).Error; err != nil {
		return nil, err
	}
	for _, row := range tokenRows {
		summary := summaries[row.UserId]
		summary.TokenCount = row.Total
		summaries[row.UserId] = summary
	}

	var spendRows []aggregateRow
	if err := TelemetryDB().Model(&Log{}).
		Select("user_id, coalesce(sum(amount_microusd), 0) as total").
		Where("user_id IN ? AND type = ? AND created_at >= ?", userIDs, LogTypeConsume, spendSince).
		Group("user_id").
		Scan(&spendRows).Error; err != nil {
		return nil, err
	}
	for _, row := range spendRows {
		summary := summaries[row.UserId]
		summary.SpendMicrousd = billing.MicroUSD(row.Total)
		summaries[row.UserId] = summary
	}

	var requestRows []aggregateRow
	if err := TelemetryDB().Model(&Log{}).
		Select("user_id, count(*) as total").
		Where("user_id IN ? AND type = ? AND created_at >= ?", userIDs, LogTypeConsume, spendSince).
		Group("user_id").
		Scan(&requestRows).Error; err != nil {
		return nil, err
	}
	for _, row := range requestRows {
		summary := summaries[row.UserId]
		summary.RequestCount = row.Total
		summaries[row.UserId] = summary
	}

	// Last activity is the newest consume-log timestamp, not the token's
	// accessed_time: accessed_time is seeded to the token's creation time,
	// so a brand-new never-called account would otherwise look active.
	// Left at 0 (never) when the user has no consume logs at all.
	var lastUsedRows []aggregateRow
	if err := TelemetryDB().Model(&Log{}).
		Select("user_id, max(created_at) as total").
		Where("user_id IN ? AND type = ?", userIDs, LogTypeConsume).
		Group("user_id").
		Scan(&lastUsedRows).Error; err != nil {
		return nil, err
	}
	for _, row := range lastUsedRows {
		summary := summaries[row.UserId]
		summary.LastUsedAt = row.Total
		summaries[row.UserId] = summary
	}

	var topupRows []aggregateRow
	if err := CoreDB().Model(&TopUp{}).
		Select("user_id, coalesce(sum("+topUpCreditedMicrousdSQL+"), 0) as total").
		Where("user_id IN ? AND status = ?", userIDs, common.TopUpStatusSuccess).
		Group("user_id").
		Scan(&topupRows).Error; err != nil {
		return nil, err
	}
	for _, row := range topupRows {
		summary := summaries[row.UserId]
		summary.TopupTotalMicrousd = billing.MicroUSD(row.Total)
		summaries[row.UserId] = summary
	}

	pairs, err := userTopClientPairs(userIDs, spendSince)
	if err != nil {
		return nil, err
	}
	for userID, pair := range pairs {
		summary := summaries[userID]
		summary.TopFramework = pair.Framework
		summary.TopCountry = pair.Country
		summaries[userID] = summary
	}

	return summaries, nil
}

// clientPair is the (framework, country) combination that actually
// co-occurred on a request, as opposed to pairing two independent modes.
type clientPair struct {
	Framework string
	Country   string
}

// userTopClientPairs returns, per user id, the (client_framework,
// client_country) pair that co-occurred most often across that user's
// consume logs in the window. Ranking by the pair — rather than picking a
// top framework and a top country independently — guarantees the surfaced
// pair is one the account genuinely produced together. Rows where both
// components are empty are skipped; a pair with one empty component is a
// legitimate top pair and is kept.
func userTopClientPairs(userIDs []int, since int64) (map[int]clientPair, error) {
	pairs := make(map[int]clientPair, len(userIDs))
	if len(userIDs) == 0 {
		return pairs, nil
	}

	type pairRow struct {
		UserId    int
		Framework string
		Country   string
		Total     int64
	}
	var rows []pairRow
	if err := TelemetryDB().Model(&Log{}).
		Select("user_id, client_framework as framework, client_country as country, count(*) as total").
		Where("user_id IN ? AND type = ? AND created_at >= ? AND (client_framework <> '' OR client_country <> '')", userIDs, LogTypeConsume, since).
		Group("user_id, client_framework, client_country").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	best := make(map[int]int64, len(userIDs))
	for _, row := range rows {
		if row.Framework == "" && row.Country == "" {
			continue
		}
		if current, ok := best[row.UserId]; !ok || row.Total > current {
			best[row.UserId] = row.Total
			pairs[row.UserId] = clientPair{Framework: row.Framework, Country: row.Country}
		}
	}
	return pairs, nil
}

// GetUserIdentityProviders collects the deduplicated external login
// providers each id has bound, so the console can badge accounts with how
// they sign in. The query is bounded by the caller's current page of ids.
func GetUserIdentityProviders(userIDs []int) (map[int][]string, error) {
	providers := make(map[int][]string, len(userIDs))
	if len(userIDs) == 0 {
		return providers, nil
	}
	type providerRow struct {
		UserID   int
		Provider string
	}
	var rows []providerRow
	if err := CoreDB().Model(&UserIdentity{}).
		Select("user_id, provider").
		Where("user_id IN ?", userIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[int]map[string]bool, len(userIDs))
	for _, row := range rows {
		provider := strings.TrimSpace(row.Provider)
		if provider == "" {
			continue
		}
		if seen[row.UserID] == nil {
			seen[row.UserID] = make(map[string]bool)
		}
		if seen[row.UserID][provider] {
			continue
		}
		seen[row.UserID][provider] = true
		providers[row.UserID] = append(providers[row.UserID], provider)
	}
	return providers, nil
}

// UserSummaryStats is the platform-wide account census behind the console's
// ledger header cards.
type UserSummaryStats struct {
	TotalCustomers   int64 `json:"total_customers"`
	PlatformMembers  int64 `json:"platform_members"`
	Active30d        int64 `json:"active_30d"`
	ToppedUp         int64 `json:"topped_up"`
	LowBalanceActive int64 `json:"low_balance_active"`
	ZeroToppedUp     int64 `json:"zero_topped_up"`
	Disabled         int64 `json:"disabled"`
	OnPricePlan      int64 `json:"on_price_plan"`
}

// GetUserSummaryStats computes the account census over the whole table. The
// two "active" figures reuse the distinct consume-user id set from TelemetryDB().
func GetUserSummaryStats(activeSince int64) (UserSummaryStats, error) {
	var stats UserSummaryStats

	if err := CoreDB().Model(&User{}).Where("role = ?", common.RoleCommonUser).Count(&stats.TotalCustomers).Error; err != nil {
		return stats, err
	}
	if err := CoreDB().Model(&User{}).Where("role >= ?", common.RoleRootUser).Count(&stats.PlatformMembers).Error; err != nil {
		return stats, err
	}
	if err := CoreDB().Model(&User{}).Where("has_topped_up = ? AND role = ?", true, common.RoleCommonUser).Count(&stats.ToppedUp).Error; err != nil {
		return stats, err
	}
	if err := CoreDB().Model(&User{}).Where("quota <= ? AND has_topped_up = ? AND role = ?", 0, true, common.RoleCommonUser).Count(&stats.ZeroToppedUp).Error; err != nil {
		return stats, err
	}
	if err := CoreDB().Model(&User{}).Where("status = ? AND role = ?", common.UserStatusDisabled, common.RoleCommonUser).Count(&stats.Disabled).Error; err != nil {
		return stats, err
	}
	if err := CoreDB().Model(&User{}).Where("price_plan_id > 0").Count(&stats.OnPricePlan).Error; err != nil {
		return stats, err
	}

	// The active-consumer id set from TelemetryDB() is unscoped and includes
	// admins/root, but every other tile counts customers (role=1) only.
	// Intersect with common-user rows so active_30d and low_balance_active
	// stay on the same population as the rest of the census.
	if err := forEachActiveConsumerIDBatch(activeSince, func(ids []int) error {
		var active, low int64
		if err := CoreDB().Model(&User{}).
			Where("role = ? AND id IN ?", common.RoleCommonUser, ids).
			Count(&active).Error; err != nil {
			return err
		}
		if err := CoreDB().Model(&User{}).
			Where("role = ? AND quota < ? AND status = ? AND id IN ?", common.RoleCommonUser, LowBalanceMicrousd, common.UserStatusEnabled, ids).
			Count(&low).Error; err != nil {
			return err
		}
		stats.Active30d += active
		stats.LowBalanceActive += low
		return nil
	}); err != nil {
		return stats, err
	}

	return stats, nil
}

// ClientBreakdownEntry / CountryBreakdownEntry carry the per-user request
// mix for the single-account drawer.
type ClientBreakdownEntry struct {
	Framework string `json:"framework"`
	Count     int64  `json:"count"`
}

type CountryBreakdownEntry struct {
	Country string `json:"country"`
	Count   int64  `json:"count"`
}

// UserAccountUsageAggregate is the per-account usage slice the single-user
// summary drawer needs from TelemetryDB() and the token table.
type UserAccountUsageAggregate struct {
	RequestCount    int64
	LastUsedAt      int64
	RecentSpend     billing.MicroUSD
	AgentSessions   int64
	ClientBreakdown []ClientBreakdownEntry
	Countries       []CountryBreakdownEntry
}

// GetUserAccountUsageAggregate gathers one account's windowed usage. Every
// query is keyed on the indexed user_id, so the cost is per-account.
func GetUserAccountUsageAggregate(userID int, since int64) (UserAccountUsageAggregate, error) {
	var agg UserAccountUsageAggregate

	if err := TelemetryDB().Model(&Log{}).
		Where("user_id = ? AND type = ? AND created_at >= ?", userID, LogTypeConsume, since).
		Count(&agg.RequestCount).Error; err != nil {
		return agg, err
	}

	var spend struct {
		Total int64
	}
	if err := TelemetryDB().Model(&Log{}).
		Select("coalesce(sum(amount_microusd), 0) as total").
		Where("user_id = ? AND type = ? AND created_at >= ?", userID, LogTypeConsume, since).
		Scan(&spend).Error; err != nil {
		return agg, err
	}
	agg.RecentSpend = billing.MicroUSD(spend.Total)

	// Last activity is the newest consume-log timestamp (all-time), not the
	// token accessed_time, which is seeded to token creation time and would
	// make a never-called account look active. 0 = never called.
	var lastUsed struct {
		Total int64
	}
	if err := TelemetryDB().Model(&Log{}).
		Select("coalesce(max(created_at), 0) as total").
		Where("user_id = ? AND type = ?", userID, LogTypeConsume).
		Scan(&lastUsed).Error; err != nil {
		return agg, err
	}
	agg.LastUsedAt = lastUsed.Total

	if err := TelemetryDB().Model(&Log{}).
		Where("user_id = ? AND type = ? AND created_at >= ? AND session_id <> '' AND NOT (token_id > 0 AND session_id = ('token-' || CAST(token_id AS TEXT)))", userID, LogTypeConsume, since).
		Distinct("session_id").
		Count(&agg.AgentSessions).Error; err != nil {
		return agg, err
	}

	type breakdownRow struct {
		Value string
		Total int64
	}
	var frameworkRows []breakdownRow
	if err := TelemetryDB().Model(&Log{}).
		Select("client_framework as value, count(*) as total").
		Where("user_id = ? AND type = ? AND created_at >= ? AND client_framework <> ''", userID, LogTypeConsume, since).
		Group("client_framework").
		Order("total desc").
		Limit(5).
		Scan(&frameworkRows).Error; err != nil {
		return agg, err
	}
	for _, row := range frameworkRows {
		agg.ClientBreakdown = append(agg.ClientBreakdown, ClientBreakdownEntry{Framework: row.Value, Count: row.Total})
	}

	var countryRows []breakdownRow
	if err := TelemetryDB().Model(&Log{}).
		Select("client_country as value, count(*) as total").
		Where("user_id = ? AND type = ? AND created_at >= ? AND client_country <> ''", userID, LogTypeConsume, since).
		Group("client_country").
		Order("total desc").
		Limit(5).
		Scan(&countryRows).Error; err != nil {
		return agg, err
	}
	for _, row := range countryRows {
		agg.Countries = append(agg.Countries, CountryBreakdownEntry{Country: row.Value, Count: row.Total})
	}

	return agg, nil
}

// ErrUserNotFound answers a lookup for an account that does not exist. It
// compares by code, so errors.Is(err, ErrUserNotFound) holds for any copy.
var ErrUserNotFound = types.Err(types.ErrorCodeUserNotFound)

// userLookupErr names a missing account; any other failure passes through.
func userLookupErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUserNotFound
	}
	return err
}

// errUserRowNotChanged reports a write that matched no row even though the
// row was locked a moment earlier. Callers answer it as an internal error.
func errUserRowNotChanged(operation string) error {
	return fmt.Errorf("user %s affected no row", operation)
}

func GetUserById(id int, selectAll bool) (*User, error) {
	if id == 0 {
		return nil, ErrUserNotFound
	}
	user := User{Id: id}
	var err error = nil
	if selectAll {
		err = CoreDB().First(&user, "id = ?", id).Error
	} else {
		err = CoreDB().Omit("password").First(&user, "id = ?", id).Error
	}
	return &user, userLookupErr(err)
}

func HardDeleteUserById(id int) error {
	_, err := hardDeleteUser(id, 0, 0, false)
	return err
}

// HardDeleteUserAuthorized rechecks the administrator's stale list snapshot
// against the locked target row before removing the account. Token and
// external-identity rows are deleted in the same transaction, and every token
// projection is fenced after commit so a concurrent cache fill cannot revive a
// credential whose database row has disappeared.
func HardDeleteUserAuthorized(userID int, actorRole int, expectedRole int) (*User, error) {
	return hardDeleteUser(userID, actorRole, expectedRole, true)
}

func hardDeleteUser(userID int, actorRole int, expectedRole int, enforceAuthorization bool) (*User, error) {
	if userID <= 0 {
		return nil, ErrUserNotFound
	}

	var deleted User
	var digests []string
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := checkUserProjectDeletionTx(tx, userID, true); err != nil {
			return err
		}
		if err := tx.Unscoped().
			Omit("password", "access_token").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&deleted, userID).Error; err != nil {
			return userLookupErr(err)
		}
		if enforceAuthorization {
			if deleted.Role != expectedRole {
				return types.Err(types.ErrorCodeVersionConflict)
			}
			if actorRole <= deleted.Role {
				return types.Err(types.ErrorCodeUserManageForbidden, types.D{"action": "delete"})
			}
		}

		var tokens []Token
		if err := tx.Unscoped().
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", commonKeyCol, "key_digest").
			Where("user_id = ?", userID).
			Find(&tokens).Error; err != nil {
			return err
		}
		digests = make([]string, 0, len(tokens))
		for index := range tokens {
			digests = append(digests, tokens[index].CacheDigest())
		}

		if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&Token{}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&UserIdentity{}) {
			if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&UserIdentity{}).Error; err != nil {
				return err
			}
		}
		result := tx.Unscoped().Delete(&deleted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("delete")
		}
		return finishUserProjectDeletionTx(tx, userID, true)
	})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(
		cacheInvalidateTokenSnapshotsWithFence(digests),
		invalidateUserCache(userID),
	); err != nil {
		return nil, fmt.Errorf("user deletion committed but credential cache invalidation failed: %w", err)
	}
	return &deleted, nil
}

func (user *User) Insert(_ ...int) error {
	return user.insertWithIdentity(nil)
}

// InsertWithIdentity commits a new user row and its first external identity in
// the same database transaction. OAuth callbacks must not leave a passwordless
// orphan account behind if the unique provider subject loses a race.
func (user *User) InsertWithIdentity(identity *UserIdentity) error {
	if identity == nil || strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.Subject) == "" {
		return errors.New("external identity is incomplete")
	}
	return user.insertWithIdentity(identity)
}

func (user *User) insertWithIdentity(identity *UserIdentity) error {
	var err error
	if user.Password != "" {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}
	signupQuota := common.QuotaForNewUser
	user.Quota = 0
	//user.SetAccessToken(common.GetUUID())

	// 初始化用户设置，包括默认的边栏配置
	// 这里暂时不设置SidebarModules，因为需要在用户创建后根据角色设置。
	// First-run onboarding is stamped at creation so accounts that predate the
	// feature are never shown it.
	initialSetting := user.GetSetting()
	initialSetting.OnboardingActive = true
	user.SetSetting(initialSetting)

	if err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		if identity != nil {
			identity.UserID = user.Id
			if err := tx.Create(identity).Error; err != nil {
				return err
			}
		}
		if signupQuota <= 0 {
			return nil
		}
		return adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
			UserID:         user.Id,
			DeltaQuota:     signupQuota,
			Kind:           WalletLedgerKindBonus,
			RefType:        "user_signup",
			RefID:          fmt.Sprintf("user:%d:signup", user.Id),
			PricingVersion: defaultPricingSeedVersion,
			Metadata: map[string]any{
				"quota": signupQuota,
			},
		})
	}); err != nil {
		return err
	}
	user.Quota = signupQuota

	// 用户创建成功后，根据角色初始化边栏配置
	// 需要重新获取用户以确保有正确的ID和Role
	var createdUser User
	if err := CoreDB().Where("username = ?", user.Username).First(&createdUser).Error; err == nil {
		// 生成基于角色的默认边栏配置
		defaultSidebarConfig := generateDefaultSidebarConfigForRole(createdUser.Role)
		if defaultSidebarConfig != "" {
			currentSetting := createdUser.GetSetting()
			currentSetting.SidebarModules = defaultSidebarConfig
			createdUser.SetSetting(currentSetting)
			createdUser.Update(false)
			common.SysLog(fmt.Sprintf("Initializing sidebar configuration for new user %s (role: %d)", createdUser.Username, createdUser.Role))
		}
	}

	if signupQuota > 0 {
		RecordLog(user.Id, LogTypeSystem, fmt.Sprintf("新用户注册赠送 %s", logger.LogQuota(signupQuota)))
	}
	return nil
}

func (user *User) Update(updatePassword bool) error {
	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}
	newUser := *user
	// Update is used by self-service profile and preference paths. Build an
	// explicit allowlist instead of handing GORM a possibly stale full User:
	// role, status, group, quota and other authorization/billing fields must only
	// be changed by their dedicated administrative or ledger APIs.
	updates := make(map[string]any, 4)
	if strings.TrimSpace(newUser.Setting) != "" {
		// Settings callers load a complete User before merging JSON. Treat a
		// non-empty Setting as the explicit intent and do not echo stale profile
		// fields that happened to be present on that object.
		updates["setting"] = newUser.Setting
	} else {
		if strings.TrimSpace(newUser.Username) != "" {
			updates["username"] = newUser.Username
		}
		if strings.TrimSpace(newUser.DisplayName) != "" {
			updates["display_name"] = newUser.DisplayName
		}
	}
	if updatePassword {
		updates["password"] = newUser.Password
		updates["needs_password"] = false
		updates["session_version"] = gorm.Expr("session_version + ?", 1)
	}
	var current User
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
			return err
		}
		if len(updates) > 0 {
			result := tx.Model(&User{}).Where("id = ?", user.Id).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errUserRowNotChanged("update")
			}
		}
		if err := tx.First(&current, user.Id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Do not write the post-commit snapshot back directly. A concurrent soft
	// delete may already have committed and advanced the user-cache generation;
	// invalidation keeps the next database fill behind that same fence.
	return invalidateUserCache(current.Id)
}

// UpdateWithPasswordAndRevokeSessions commits the credential change and the
// session-version increment together. SessionVersion is a dedicated column so
// unrelated JSON setting writes cannot roll a revocation back.
func (user *User) UpdateWithPasswordAndRevokeSessions(originalPassword string) (*User, error) {
	if user == nil || user.Id <= 0 || user.Password == "" {
		return nil, errors.New("password update needs a user and a new password")
	}
	hashedPassword, err := common.Password2Hash(user.Password)
	if err != nil {
		return nil, err
	}
	var updated User
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var current User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
			return err
		}
		// The first-password exception and the original-password check are made
		// from the same locked row that is updated below. Concurrent requests can
		// no longer both observe NeedsPassword=true and bypass the old secret.
		if !current.NeedsPassword && !common.ValidatePasswordAndHash(originalPassword, current.Password) {
			return types.Err(types.ErrorCodeOriginalPasswordIncorrect)
		}
		updates := map[string]any{
			"password":        hashedPassword,
			"needs_password":  false,
			"session_version": gorm.Expr("session_version + ?", 1),
		}
		if strings.TrimSpace(user.Username) != "" {
			updates["username"] = user.Username
		}
		if strings.TrimSpace(user.DisplayName) != "" {
			updates["display_name"] = user.DisplayName
		}
		result := tx.Model(&User{}).Where("id = ?", user.Id).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("password update")
		}
		return tx.Omit("password").First(&updated, user.Id).Error
	})
	if err != nil {
		return nil, err
	}
	if err := InvalidateUserCache(user.Id); err != nil {
		// Session validation reads the authoritative column, so a Redis failure
		// cannot keep an old cookie alive.
		common.SysLog(fmt.Sprintf("failed to invalidate user cache after password change for user %d: %v", user.Id, err))
	}
	return &updated, nil
}

func (user *User) Edit(updatePassword bool) error {
	return user.edit(updatePassword, false, 0, 0)
}

func (user *User) EditAuthorized(updatePassword bool, actorRole int, expectedRole int) error {
	return user.edit(updatePassword, true, actorRole, expectedRole)
}

func (user *User) edit(updatePassword bool, enforceAuthorization bool, actorRole int, expectedRole int) error {
	if !common.IsValidateRole(user.Role) {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "role"})
	}
	if user.Status != common.UserStatusEnabled && user.Status != common.UserStatusDisabled {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "status"})
	}
	if user.PricePlanID < 0 || user.RPMLimit < 0 || user.TPMLimit < 0 {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "price_plan_id"})
	}
	if user.PricePlanID > 0 {
		if _, ok := GetPricePlan(user.PricePlanID); !ok {
			return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "price_plan_id"})
		}
	}

	var err error
	if updatePassword {
		user.Password, err = common.Password2Hash(user.Password)
		if err != nil {
			return err
		}
	}

	newUser := *user
	updates := map[string]interface{}{
		"username":      newUser.Username,
		"display_name":  newUser.DisplayName,
		"role":          newUser.Role,
		"email":         newUser.Email,
		"price_plan_id": newUser.PricePlanID,
		"rpm_limit":     newUser.RPMLimit,
		"tpm_limit":     newUser.TPMLimit,
		"remark":        newUser.Remark,
		"status":        newUser.Status,
	}
	if updatePassword {
		updates["password"] = newUser.Password
	}

	var stored User
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stored, user.Id).Error; err != nil {
			return userLookupErr(err)
		}
		if enforceAuthorization {
			if stored.Role != expectedRole {
				return types.Err(types.ErrorCodeVersionConflict)
			}
			if err := checkRoleChangeAllowed(actorRole, stored.Role, newUser.Role); err != nil {
				return err
			}
		}
		if err := ensureAnotherActiveAdminTx(tx, stored, newUser.Role, newUser.Status); err != nil {
			return err
		}
		roleChanged := newUser.Role != stored.Role
		statusChanged := newUser.Status != stored.Status
		if updatePassword {
			updates["needs_password"] = false
		}
		if updatePassword || roleChanged || statusChanged {
			updates["session_version"] = gorm.Expr("session_version + ?", 1)
		}
		result := tx.Model(&User{}).Where("id = ?", user.Id).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("update")
		}
		return tx.Omit("password").First(&stored, user.Id).Error
	})
	if err != nil {
		return err
	}
	*user = stored

	if err := invalidateUserCache(user.Id); err != nil {
		return err
	}
	return InvalidateUserTokensCache(user.Id)
}

// CheckUserManageAllowed is the one platform-role rule every administrative
// read or write of another account shares. There are two roles: only an
// administrator (role 100) manages other accounts, and an administrator
// manages every account, including other administrators.
func CheckUserManageAllowed(actorRole int, action string) error {
	if actorRole >= common.RoleRootUser {
		return nil
	}
	return types.Err(types.ErrorCodeUserManageForbidden, types.D{"action": action})
}

// checkRoleChangeAllowed applies CheckUserManageAllowed to a write that may
// also change the target's role. The current and desired roles no longer
// matter: an administrator may assign either role, a normal user neither.
func checkRoleChangeAllowed(actorRole int, _ int, _ int) error {
	return CheckUserManageAllowed(actorRole, "update")
}

// ensureAnotherActiveAdminTx refuses a write that would take the last enabled
// administrator out of service, whether by demoting or by disabling it.
// Call it inside the transaction that holds the target row's lock.
func ensureAnotherActiveAdminTx(tx *gorm.DB, current User, desiredRole int, desiredStatus int) error {
	wasActiveAdmin := current.Role >= common.RoleRootUser && current.Status == common.UserStatusEnabled
	staysActiveAdmin := desiredRole >= common.RoleRootUser && desiredStatus == common.UserStatusEnabled
	if !wasActiveAdmin || staysActiveAdmin {
		return nil
	}
	var others int64
	if err := tx.Model(&User{}).
		Where("id <> ? AND role >= ? AND status = ?", current.Id, common.RoleRootUser, common.UserStatusEnabled).
		Count(&others).Error; err != nil {
		return err
	}
	if others == 0 {
		// Reuses root_user_protected (LZ-7005): its console copy is keyed by
		// code, and a new code would need copy in every locale first.
		action := "demoted while it is the last administrator"
		if desiredRole >= common.RoleRootUser {
			action = "disabled while it is the last administrator"
		}
		return types.Err(types.ErrorCodeRootUserProtected, types.D{"action": action})
	}
	return nil
}

// UpdateUserRoleStatus changes only the requested authorization fields. The
// row lock and session-version increment share a transaction, while unrelated
// profile data from a stale controller snapshot is never written back.
func UpdateUserRoleStatus(userID int, actorRole int, expectedRole int, role *int, status *int) (*User, error) {
	if userID <= 0 || (role == nil && status == nil) {
		return nil, errors.New("role/status update needs a user and a field to change")
	}
	if role != nil && !common.IsValidateRole(*role) {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "role"})
	}
	if status != nil && *status != common.UserStatusEnabled && *status != common.UserStatusDisabled {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "status"})
	}
	var updated User
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&updated, userID).Error; err != nil {
			return userLookupErr(err)
		}
		if updated.Role != expectedRole {
			return types.Err(types.ErrorCodeVersionConflict)
		}
		desiredRole := updated.Role
		if role != nil {
			desiredRole = *role
		}
		if err := checkRoleChangeAllowed(actorRole, updated.Role, desiredRole); err != nil {
			return err
		}
		desiredStatus := updated.Status
		if status != nil {
			desiredStatus = *status
		}
		if err := ensureAnotherActiveAdminTx(tx, updated, desiredRole, desiredStatus); err != nil {
			return err
		}
		updates := make(map[string]any, 3)
		if role != nil && *role != updated.Role {
			updates["role"] = *role
		}
		if status != nil && *status != updated.Status {
			updates["status"] = *status
		}
		if len(updates) == 0 {
			return nil
		}
		updates["session_version"] = gorm.Expr("session_version + ?", 1)
		result := tx.Model(&User{}).Where("id = ?", userID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("role/status update")
		}
		return tx.Omit("password").First(&updated, userID).Error
	})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(invalidateUserCache(userID), InvalidateUserTokensCache(userID)); err != nil {
		return nil, err
	}
	return &updated, nil
}

func UpdateUserAccessToken(userID int, accessToken string) error {
	if userID <= 0 || strings.TrimSpace(accessToken) == "" {
		return errors.New("access token update needs a user and a token")
	}
	if err := CoreDB().Model(&User{}).Where("id = ?", userID).Update("access_token", accessToken).Error; err != nil {
		return err
	}
	// ValidateAccessToken reads the database directly, so the previous access
	// token cannot survive this write. Clear the general user projection too so
	// callers never retain a pre-update snapshot.
	return invalidateUserCache(userID)
}

func UpdateUserEmail(userID int, email string) error {
	if userID <= 0 || strings.TrimSpace(email) == "" {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"})
	}
	if err := CoreDB().Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{"email": strings.TrimSpace(email), "verified_email": strings.ToLower(strings.TrimSpace(email))}).Error; err != nil {
		return err
	}
	return invalidateUserCache(userID)
}

func SetUserHasToppedUp(userID int, hasToppedUp bool) error {
	if userID <= 0 {
		return ErrUserNotFound
	}
	if err := CoreDB().Model(&User{}).Where("id = ?", userID).Update("has_topped_up", hasToppedUp).Error; err != nil {
		return err
	}
	if err := invalidateUserCache(userID); err != nil {
		return err
	}
	return InvalidateUserTokensCache(userID)
}

func SetUserQuotaWithLedger(userID int, newQuota billing.MicroUSD, kind, refType, refID string, metadata map[string]any) (delta billing.MicroUSD, err error) {
	if userID == 0 {
		return 0, ErrUserNotFound
	}
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var available billing.MicroUSD
		if GatewayLedgerEnabled() {
			// Take the payer fence before a SQL row lock: the cold financial
			// snapshot uses another connection and must never wait on our row.
			balance, err := fenceExternalLedgerAvailable(tx, userID)
			if err != nil {
				return err
			}
			available = billing.MicroUSD(balance)
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "quota").
			First(&user, userID).Error; err != nil {
			return err
		}
		if !GatewayLedgerEnabled() {
			available = user.Quota
		}
		delta = newQuota - available
		if delta == 0 {
			return nil
		}
		return adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
			UserID:         userID,
			DeltaQuota:     delta,
			Kind:           kind,
			RefType:        refType,
			RefID:          refID,
			PricingVersion: defaultPricingSeedVersion,
			Metadata:       metadata,
		})
	})
	if err != nil {
		return 0, err
	}
	if delta != 0 {
		updateUserQuotaCacheAfterCommit(userID, delta)
	}
	return delta, nil
}

func (user *User) Delete() error {
	_, err := DeleteSelfUser(user.Id)
	return err
}

// ErrCannotDeleteRootUser refuses to remove the root account.
var ErrCannotDeleteRootUser = types.Err(types.ErrorCodeRootUserProtected, types.D{"action": "deleted"})

// DeleteSelfUser makes the root-account guard and the soft delete one locked
// operation. If another administrator promotes the account to root before this
// transaction obtains the row lock, the delete is rejected instead of acting
// on the role captured by an earlier middleware/controller read.
func DeleteSelfUser(userID int) (*User, error) {
	if userID <= 0 {
		return nil, ErrUserNotFound
	}
	var deleted User
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := checkUserProjectDeletionTx(tx, userID, false); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&deleted, userID).Error; err != nil {
			return userLookupErr(err)
		}
		if deleted.Role == common.RoleRootUser {
			return ErrCannotDeleteRootUser
		}
		result := tx.Delete(&deleted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("delete")
		}
		return finishUserProjectDeletionTx(tx, userID, false)
	})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(invalidateUserCache(userID), InvalidateUserTokensCache(userID)); err != nil {
		return nil, fmt.Errorf("user deletion committed but credential cache invalidation failed: %w", err)
	}
	return &deleted, nil
}

// DeleteUserWithExpectedRole prevents an administrator's authorization check
// from going stale between the controller lookup and the delete. A concurrent
// promotion changes the locked role and makes the operation retry instead of
// deleting a newly privileged account.
func DeleteUserWithExpectedRole(userID int, actorRole int, expectedRole int) (*User, error) {
	if userID <= 0 {
		return nil, ErrUserNotFound
	}
	var deleted User
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := checkUserProjectDeletionTx(tx, userID, false); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&deleted, userID).Error; err != nil {
			return userLookupErr(err)
		}
		if deleted.Role != expectedRole {
			return types.Err(types.ErrorCodeVersionConflict)
		}
		if deleted.Role == common.RoleRootUser {
			return ErrCannotDeleteRootUser
		}
		if err := CheckUserManageAllowed(actorRole, "delete"); err != nil {
			return err
		}
		result := tx.Delete(&deleted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("delete")
		}
		return finishUserProjectDeletionTx(tx, userID, false)
	})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(invalidateUserCache(userID), InvalidateUserTokensCache(userID)); err != nil {
		return nil, err
	}
	return &deleted, nil
}

func (user *User) HardDelete() error { return HardDeleteUserById(user.Id) }

// ValidateAndFill check password & user status
func (user *User) ValidateAndFill() (err error) {
	// When querying with struct, GORM will only query with non-zero fields,
	// that means if your field's value is 0, '', false or other zero values,
	// it won't be used to build query conditions
	password := user.Password
	username := strings.TrimSpace(user.Username)
	if username == "" {
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "username"})
	}
	if password == "" {
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "password"})
	}
	// find buy username or email
	CoreDB().Where("username = ? OR email = ?", username, username).First(user)
	okay := common.ValidatePasswordAndHash(password, user.Password)
	if !okay || user.Status != common.UserStatusEnabled {
		return types.Err(types.ErrorCodeInvalidCredentials)
	}
	return nil
}

func (user *User) FillUserById() error {
	if user.Id == 0 {
		return ErrUserNotFound
	}
	CoreDB().Where(User{Id: user.Id}).First(user)
	return nil
}

func (user *User) FillUserByEmail() error {
	if user.Email == "" {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"})
	}
	CoreDB().Where(User{Email: user.Email}).First(user)
	return nil
}

func IsEmailAlreadyTaken(email string) bool {
	email = strings.TrimSpace(email)
	if CoreDB() == nil || email == "" {
		return false
	}
	var count int64
	return CoreDB().Unscoped().Model(&User{}).
		Where("LOWER(email) = LOWER(?)", email).
		Limit(1).
		Count(&count).Error == nil && count > 0
}

// GetUserByEmail returns the single account owning an address. An address
// shared by several rows resolves to nothing, because no caller can safely
// guess which of them the requester meant.
func GetUserByEmail(email string) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"})
	}
	var users []User
	if err := CoreDB().Where("LOWER(email) = LOWER(?)", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) != 1 {
		return nil, nil
	}
	return &users[0], nil
}

func PasswordResetVerificationKey(userID int, sessionVersion int) string {
	return fmt.Sprintf("user:%d:session:%d", userID, sessionVersion)
}

// ErrPasswordResetLinkInvalid covers every way a reset link can fail to match:
// a wrong or spent token, a changed address, or a newer session version.
var ErrPasswordResetLinkInvalid = types.Err(types.ErrorCodePasswordResetLinkInvalid)

// ResetUserPasswordWithToken first consumes the one-time token under its
// user/version key, before any account lookup or expensive password hash. The
// locked row then confirms those claims and the canonical email before writing.
// A token issued before a security-state change can never reset the new secret.
func ResetUserPasswordWithToken(email string, userID int, issuedSessionVersion int, token string, password string) (*User, error) {
	email = strings.TrimSpace(email)
	token = strings.TrimSpace(token)
	if email == "" || userID <= 0 || issuedSessionVersion < 0 || token == "" || password == "" {
		return nil, ErrPasswordResetLinkInvalid
	}
	verificationKey := PasswordResetVerificationKey(userID, issuedSessionVersion)
	if !common.VerifyCodeWithKey(verificationKey, token, common.PasswordResetPurpose) {
		return nil, ErrPasswordResetLinkInvalid
	}
	hashedPassword, err := common.Password2Hash(password)
	if err != nil {
		return nil, err
	}
	var updated User
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		var current User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPasswordResetLinkInvalid
			}
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(current.Email), email) || current.SessionVersion != issuedSessionVersion {
			return ErrPasswordResetLinkInvalid
		}
		result := tx.Model(&User{}).Where("id = ?", current.Id).Updates(map[string]any{
			"password":        hashedPassword,
			"needs_password":  false,
			"session_version": gorm.Expr("session_version + ?", 1),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errUserRowNotChanged("password reset")
		}
		return tx.Omit("password").First(&updated, current.Id).Error
	})
	if err != nil {
		return nil, err
	}
	if err := InvalidateUserCache(userID); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user cache after password reset for user %d: %v", userID, err))
	}
	return &updated, nil
}

// GetUserSessionVersion deliberately bypasses Redis. Password revocation is a
// security boundary and must fail closed even when cache invalidation fails.
func GetUserSessionVersion(id int) (int, error) {
	if id <= 0 {
		return 0, ErrUserNotFound
	}
	var user User
	if err := CoreDB().Select("session_version").First(&user, id).Error; err != nil {
		return 0, err
	}
	return user.SessionVersion, nil
}

// IsAdmin reports whether the user holds the administrator role (100).
func IsAdmin(userId int) bool {
	if userId == 0 {
		return false
	}
	var user User
	err := CoreDB().Where("id = ?", userId).Select("role").Find(&user).Error
	if err != nil {
		common.SysLog("no such user " + err.Error())
		return false
	}
	return user.Role >= common.RoleRootUser
}

func ValidateAccessToken(token string) (user *User) {
	if token == "" {
		return nil
	}
	token = strings.Replace(token, "Bearer ", "", 1)
	user = &User{}
	if CoreDB().Where("access_token = ?", token).First(user).RowsAffected == 1 {
		return user
	}
	return nil
}

// GetUserQuota gets quota from Redis first, falls back to CoreDB() if needed
func GetUserQuota(id int, fromDB bool) (quota billing.MicroUSD, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful CoreDB() read
		if shouldUpdateRedis(fromDB, err) {
			common.SafeGo("user-quota-cache-refresh", func() {
				if err := updateUserQuotaCache(id, quota); err != nil {
					common.SysLog("failed to update user quota cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		quota, err := getUserQuotaCache(id)
		if err == nil {
			return quota, nil
		}
		// Don't return error - fall through to CoreDB()
	}
	fromDB = true
	var user User
	err = CoreDB().Select("quota").Where("id = ?", id).First(&user).Error
	if err != nil {
		return 0, err
	}

	return user.Quota, nil
}

func GetUserUsedQuota(id int) (quota billing.MicroUSD, err error) {
	var user User
	err = CoreDB().Select("used_quota").Where("id = ?", id).First(&user).Error
	return user.UsedQuota, err
}

// GetUserSetting gets setting from Redis first, falls back to CoreDB() if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	var needsPassword bool
	defer func() {
		// Update Redis cache asynchronously on successful CoreDB() read
		if shouldUpdateRedis(fromDB, err) {
			common.SafeGo("user-setting-cache-refresh", func() {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
		// Don't return error - fall through to CoreDB()
	}
	fromDB = true
	var settingRow struct {
		Setting       string
		NeedsPassword bool
	}
	err = CoreDB().Model(&User{}).Where("id = ?", id).Select("setting", "needs_password").Find(&settingRow).Error
	if err != nil {
		return settingMap, err
	}
	setting = settingRow.Setting
	needsPassword = settingRow.NeedsPassword
	userBase := &UserBase{
		Setting:       setting,
		NeedsPassword: needsPassword,
	}
	return userBase.GetSetting(), nil
}

//func GetRootUserEmail() (email string) {
//	CoreDB().Model(&User{}).Where("role = ?", common.RoleRootUser).Select("email").Find(&email)
//	return email
//}

// GetRootUser returns the oldest administrator. Several accounts can hold the
// administrator role, so the lookup is ordered to stay deterministic.
func GetRootUser() (user *User) {
	CoreDB().Where("role = ? AND status = ?", common.RoleRootUser, common.UserStatusEnabled).Order("id").First(&user)
	return user
}

// UpdateUserUsedQuota adds settled spend to the payer's used_quota cache.
// The wallet ledger is the source of truth; this column only feeds the
// balance view.
func UpdateUserUsedQuota(id int, quota billing.MicroUSD) {
	if GatewayLedgerEnabled() {
		return
	}
	updateUserUsedQuota(id, quota)
}

func updateUserUsedQuota(id int, quota billing.MicroUSD) {
	err := CoreDB().Model(&User{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"used_quota": gorm.Expr("used_quota + ?", quota),
		},
	).Error
	if err != nil {
		common.SysLog("failed to update user used quota: " + err.Error())
	}
}

// GetUsernameById gets username from Redis first, falls back to CoreDB() if needed
func GetUsernameById(id int, fromDB bool) (username string, err error) {
	defer func() {
		// Update Redis cache asynchronously on successful CoreDB() read
		if shouldUpdateRedis(fromDB, err) {
			common.SafeGo("username-cache-refresh", func() {
				if err := updateUserNameCache(id, username); err != nil {
					common.SysLog("failed to update user name cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		username, err := getUserNameCache(id)
		if err == nil {
			return username, nil
		}
		// Don't return error - fall through to CoreDB()
	}
	fromDB = true
	err = CoreDB().Model(&User{}).Where("id = ?", id).Select("username").Find(&username).Error
	if err != nil {
		return "", err
	}

	return username, nil
}

func RootUserExists() bool {
	var user User
	err := CoreDB().Where("role = ?", common.RoleRootUser).First(&user).Error
	if err != nil {
		return false
	}
	return true
}

func SyncRootUserFromConfig() error {
	username := strings.TrimSpace(os.Getenv("ROOT_USERNAME"))
	if username == "" {
		username = strings.TrimSpace(common.AppConfig.Root.Username)
	}
	password := strings.TrimSpace(os.Getenv("ROOT_PASSWORD"))
	if password == "" {
		password = strings.TrimSpace(common.AppConfig.Root.Password)
	}
	if username == "" || password == "" {
		if RootUserExists() {
			common.SysLog("[root] ROOT_USERNAME/ROOT_PASSWORD not set; existing root user found, skipping root user sync")
			return nil
		}
		return errors.New("root bootstrap credentials are required when no root user exists; set ROOT_USERNAME and ROOT_PASSWORD or config root.username/root.password")
	}

	var synced User
	created := false
	changed := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		// Follow the project -> user lock order used by membership mutations.
		projectsReady := tx.Migrator().HasTable(&Project{}) && tx.Migrator().HasTable(&ProjectMember{})
		var owned []Project
		if projectsReady {
			userIDs := tx.Unscoped().Model(&User{}).Select("id").Where("username = ?", username)
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_user_id IN (?)", userIDs).Find(&owned).Error; err != nil {
				return err
			}
		}
		var current User
		err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("username = ?", username).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			hashedPassword, hashErr := common.Password2Hash(password)
			if hashErr != nil {
				return hashErr
			}
			synced = User{
				Username:    username,
				Password:    hashedPassword,
				Role:        common.RoleRootUser,
				Status:      common.UserStatusEnabled,
				DisplayName: "Root User",
				AccessToken: nil,
				Quota:       100000000,
			}
			if err := tx.Create(&synced).Error; err != nil {
				return err
			}
			created = true
			changed = true
			return nil
		}
		if err != nil {
			return err
		}

		passwordChanged := !common.ValidatePasswordAndHash(password, current.Password)
		securityChanged := passwordChanged || current.NeedsPassword ||
			current.Role != common.RoleRootUser ||
			current.Status != common.UserStatusEnabled || current.DeletedAt.Valid
		if securityChanged {
			updates := map[string]any{
				"role":            common.RoleRootUser,
				"status":          common.UserStatusEnabled,
				"deleted_at":      nil,
				"needs_password":  false,
				"session_version": gorm.Expr("session_version + ?", 1),
			}
			if passwordChanged {
				hashedPassword, hashErr := common.Password2Hash(password)
				if hashErr != nil {
					return hashErr
				}
				updates["password"] = hashedPassword
			}
			result := tx.Unscoped().Model(&User{}).Where("id = ?", current.Id).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("root user sync did not affect exactly one row")
			}
			changed = true
		}
		if err := tx.Unscoped().First(&synced, current.Id).Error; err != nil {
			return err
		}
		if projectsReady {
			// Bootstrap restores only the configured root's owned project.
			// Joined memberships, removal receipts, and credentials stay untouched.
			for _, project := range owned {
				if err := tx.Model(&ProjectMember{}).
					Where("project_id = ? AND user_id = ? AND role = ? AND status = ?", project.ID, synced.Id, ProjectRoleOwner, ProjectMemberRemoved).
					Updates(map[string]interface{}{"status": ProjectMemberActive, "removed_at": nil, "removal_operation_id": "", "version": gorm.Expr("version + 1")}).Error; err != nil {
					return err
				}
			}
			if _, err := ensureDefaultProjectTx(tx, &synced); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Retry cache invalidation on every bootstrap, even if a previous process
	// committed the database change and then failed while clearing Redis.
	if err := errors.Join(invalidateUserCache(synced.Id), InvalidateUserTokensCache(synced.Id)); err != nil {
		return fmt.Errorf("root user sync committed but cache invalidation failed: %w", err)
	}
	if created {
		common.SysLog(fmt.Sprintf("[root] created root user '%s' from env/yaml", username))
	} else if changed {
		common.SysLog(fmt.Sprintf("[root] synced root user '%s' from env/yaml", username))
	}
	return nil
}

// HasVerifiedEmail binds verification to the actual address, so an admin or
// profile update cannot carry verification forward to a different mailbox.
func (user User) HasVerifiedEmail() bool {
	return strings.TrimSpace(user.Email) != "" && strings.EqualFold(strings.TrimSpace(user.Email), strings.TrimSpace(user.VerifiedEmail))
}
