package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/lazu-ai/lazu/internal/constant"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func Login(c *gin.Context) {
	passwordLoginEnabled := common.PasswordLoginEnabled
	if !passwordLoginEnabled && !common.OIDCBreakGlassPasswordEnabled {
		httperr.Code(c, types.ErrorCodePasswordLoginDisabled)
		return
	}
	var loginRequest LoginRequest
	err := json.NewDecoder(c.Request.Body).Decode(&loginRequest)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	username := strings.TrimSpace(loginRequest.Username)
	password := loginRequest.Password
	if username == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "username"})
		return
	}
	if password == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "password"})
		return
	}
	if loginLocked(c, username) {
		return
	}
	user := model.User{
		Username: username,
		Password: password,
	}
	err = user.ValidateAndFill()
	if err != nil {
		if types.IsErrCode(err, types.ErrorCodeInvalidCredentials) {
			recordLoginFailure(username)
		}
		httperr.Write(c, err)
		return
	}
	if !passwordLoginEnabled && user.Role != common.RoleRootUser {
		recordLoginFailure(username)
		httperr.Code(c, types.ErrorCodeInvalidCredentials)
		return
	}
	clearLoginFailures(username)

	setupLogin(&user, c)
}

// setup session & cookies and then return user info
func setupLogin(user *model.User, c *gin.Context) {
	cleanUser, err := setupSession(user, c)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data":    cleanUser,
	})
}

func setupSession(user *model.User, c *gin.Context) (*model.User, error) {
	session := sessions.Default(c)
	session.Set("id", user.Id)
	session.Set("username", user.Username)
	session.Set("role", user.Role)
	session.Set("status", user.Status)
	session.Set("session_version", user.SessionVersion)
	err := session.Save()
	if err == nil {
		recordSignupAttributionIfNew(c, user)
		rememberRequestLanguage(c, user)
	}
	if err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}
	cleanUser := cleanUserForLogin(user)
	return cleanUser, nil
}

func cleanUserForLogin(user *model.User) *model.User {
	if user == nil {
		return nil
	}
	return &model.User{
		Id:          user.Id,
		PublicID:    user.PublicID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
		HomeSurface: user.HomeSurface,
	}
}

func Logout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	err := session.Save()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}

func Register(c *gin.Context) {
	if !common.RegisterEnabled {
		httperr.Code(c, types.ErrorCodeRegistrationClosed)
		return
	}
	if !common.PasswordRegisterEnabled {
		httperr.Code(c, types.ErrorCodePasswordRegistrationDisabled)
		return
	}
	var user model.User
	err := json.NewDecoder(c.Request.Body).Decode(&user)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		httperr.Write(c, userFieldInvalid(err))
		return
	}
	if common.EmailVerificationEnabled {
		if user.Email == "" || user.VerificationCode == "" {
			httperr.Code(c, types.ErrorCodeEmailVerificationRequired)
			return
		}
		if !common.VerifyCodeWithKey(user.Email, user.VerificationCode, common.EmailVerificationPurpose) {
			httperr.Code(c, types.ErrorCodeVerificationCodeInvalid)
			return
		}
	}
	normalizedEmail := ""
	if common.EmailVerificationEnabled {
		normalizedEmail = strings.ToLower(strings.TrimSpace(user.Email))
	}
	cleanUser := model.User{}
	exists := false
	err = withPasswordRegistrationLock(model.CoreDB(), normalizedEmail, user.Username, func(tx *gorm.DB) error {
		query := tx.Unscoped().Model(&model.User{}).Where("username = ?", user.Username)
		if normalizedEmail != "" {
			query = query.Or("LOWER(email) = ?", normalizedEmail)
		}
		var ownerCount int64
		if err := query.Count(&ownerCount).Error; err != nil {
			return err
		}
		if ownerCount > 0 {
			exists = true
			return nil
		}

		cleanUser = model.User{
			Username:      user.Username,
			Password:      user.Password,
			DisplayName:   user.Username,
			Email:         normalizedEmail,
			VerifiedEmail: normalizedEmail,
			Role:          common.RoleCommonUser,
			TermsVersion:  common.TermsVersion,
			TermsAgreedAt: time.Now().Unix(),
		}
		return cleanUser.InsertTx(tx)
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if exists {
		httperr.Code(c, types.ErrorCodeUserExists)
		return
	}
	if cleanUser.Id <= 0 {
		httperr.Write(c, errors.New("registration committed no user id"))
		return
	}
	model.RecordNewUserSignupEffects(&cleanUser)

	if err := initializeNewUserFeatures(&cleanUser); err != nil {
		httperr.Write(c, err)
		return
	}

	SendWelcomeEmail(&cleanUser, "", requestMailLang(c), requestHomeSurface(c))

	// Registration establishes the session directly. Bouncing a user who just
	// proved their email back to the sign-in form is the largest avoidable
	// drop-off in the funnel. The row is re-read rather than reusing cleanUser:
	// Insert replaces its password with the hash and leaves column defaults
	// like status and group unset in the struct.
	createdUser, err := model.GetUserById(cleanUser.Id, false)
	if err != nil {
		// The account exists; only the auto-login failed, so the client falls
		// back to the sign-in form instead of being told registration failed.
		common.SysLog(fmt.Sprintf("registration auto-login lookup failed for user %d: %v", cleanUser.Id, err))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    nil,
		})
		return
	}
	setupLogin(createdUser, c)
	return
}

// initializeNewUserFeatures applies the post-insert features shared by every
// registration method. Keeping this in one path prevents password, social,
// and enterprise OIDC sign-ups from receiving different initial-token
// behaviour.
var generateDefaultTokenKey = common.GenerateKey

// The sign-up Key goes through the project-aware creation path: a Key that is
// inserted straight into `tokens` carries no project, and a Key without one is
// billable to nobody and is refused by the gateway's project admission.
var insertDefaultToken = func(userID int, token *model.Token) error {
	return model.CreateDefaultUserToken(userID, token)
}

func initializeNewUserFeatures(user *model.User) error {
	if user == nil || user.Id <= 0 {
		return errors.New("new user has no id")
	}
	if !constant.GenerateDefaultToken {
		return nil
	}
	key, err := generateDefaultTokenKey()
	if err != nil {
		// The account row is already committed by every caller. Reporting the
		// registration as failed here strands a valid account and makes a retry skip
		// initialization because the external identity now exists. A default token
		// is a convenience; users can create one from the console.
		common.SysLog(fmt.Sprintf("failed to generate best-effort default token for user %d: %v", user.Id, err))
		return nil
	}
	token := model.Token{
		UserId:             user.Id,
		Name:               user.Username + "的初始令牌",
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		ExpiredTime:        -1,
		RemainQuota:        billing.USD(1),
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		Group:              model.ChannelLaneDirect,
	}
	if err := insertDefaultToken(user.Id, &token); err != nil {
		common.SysLog(fmt.Sprintf("failed to create best-effort default token for user %d: %v", user.Id, err))
		return nil
	}
	return nil
}

// resolveUserID reads a user reference (public or internal id) from a path or
// body value and names the failure: an empty value is an invalid request, an
// unknown one a missing user.
func resolveUserID(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
	}
	id, err := resolveIDParam("users", raw)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, model.ErrUserNotFound
	}
	return id, err
}

// userFieldInvalid turns a struct validation failure into invalid_request on
// the first rejected field, named as the client sent it.
func userFieldInvalid(err error) error {
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) && len(validationErrors) > 0 {
		first := validationErrors[0]
		return types.Err(types.ErrorCodeInvalidRequest, types.D{
			"field":  snakeFieldName(first.Field()),
			"reason": fmt.Sprintf("%s fails the %s rule", snakeFieldName(first.Field()), first.Tag()),
		})
	}
	return types.WrapErr(err, types.ErrorCodeInvalidRequest)
}

// snakeFieldName maps a Go field name (DisplayName) to its JSON form
// (display_name) for error details.
func snakeFieldName(name string) string {
	var out strings.Builder
	for index, r := range name {
		if r >= 'A' && r <= 'Z' {
			if index > 0 {
				out.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		out.WriteRune(r)
	}
	return out.String()
}

// userUsageWindowDays is the trailing window behind the console's
// "30 日消费" column. It is a display window only: UsedQuota stays the
// lifetime figure.
const userUsageWindowDays = 30

// userListOptions reads the optional filter/sort/aggregate query string
// the admin console sends. Absent params keep the historical behaviour.
func userListOptions(c *gin.Context) (model.UserListOptions, bool) {
	opts := model.UserListOptions{
		Status:         c.Query("status"),
		Order:          c.Query("order"),
		Role:           c.Query("role"),
		Provider:       c.Query("provider"),
		PricePlan:      c.Query("price_plan"),
		Filter:         c.Query("filter"),
		ExcludeDeleted: c.Query("exclude_deleted") == "true",
	}
	return opts, c.Query("with_usage") == "true"
}

func GetAllUsers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	opts, withUsage := userListOptions(c)
	users, total, err := model.GetAllUsersFiltered(pageInfo, opts)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	items, err := decorateUserList(users, withUsage)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)

	common.ApiSuccess(c, pageInfo)
	return
}

func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	pageInfo := common.GetPageQuery(c)
	opts, withUsage := userListOptions(c)
	users, total, err := model.SearchUsersFiltered(keyword, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), opts)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	items, err := decorateUserList(users, withUsage)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
	return
}

// clientTop is the single most-used framework/country pair for a listed
// account, surfaced so the ledger table can show "who drives this account".
type clientTop struct {
	Framework string `json:"framework"`
	Country   string `json:"country"`
}

type userWithInternalID struct {
	// Keep this projection explicit. Admin list and exact-lookup endpoints must
	// never serialize model.User because it also carries bearer credentials and
	// private integration/configuration fields.
	ID                string           `json:"id"`
	InternalID        int              `json:"internal_id"`
	Username          string           `json:"username"`
	DisplayName       string           `json:"display_name"`
	Role              int              `json:"role"`
	Status            int              `json:"status"`
	Email             string           `json:"email"`
	Quota             billing.MicroUSD `json:"quota"`
	UsedQuota         billing.MicroUSD `json:"used_quota"`
	StorageQuotaBytes int64            `json:"storage_quota_bytes"`
	StorageBytesUsed  int64            `json:"storage_bytes_used"`
	HasToppedUp       bool             `json:"has_topped_up"`
	PricePlanID       int              `json:"price_plan_id"`
	PricePlanName     string           `json:"price_plan_name,omitempty"`
	RPMLimit          int              `json:"rpm_limit"`
	TPMLimit          int              `json:"tpm_limit"`
	CreatedAt         int64            `json:"created_at"`
	CreatedAtSource   string           `json:"created_at_source"`
	FirstRequestAt    int64            `json:"first_request_at"`
	LastRequestAt     int64            `json:"last_request_at"`
	FirstTopupAt      int64            `json:"first_topup_at"`
	DeletedAt         gorm.DeletedAt   `json:"DeletedAt"`
	Remark            string           `json:"remark,omitempty"`
	needsPassword     bool
	// TokenCount / RecentSpend are only populated when the caller asks
	// for them with ?with_usage=true; the pointers keep them absent
	// otherwise so a client can tell "no data requested" from "zero".
	TokenCount      *int64            `json:"token_count,omitempty"`
	RecentSpend     *billing.MicroUSD `json:"recent_spend_microusd,omitempty"`
	RecentSpendDays int               `json:"recent_spend_days,omitempty"`
	// The following are attached alongside the usage aggregates when
	// with_usage=true. pricing_factor is a pure in-memory config lookup;
	// the rest come from the page-bounded aggregate queries.
	// RequestCountRecent is a pointer so a genuine zero still serializes
	// (the frontend renders "—" for a missing value, "0" for a real zero).
	RequestCountRecent *int64     `json:"request_count_recent,omitempty"`
	LastUsedAt         int64      `json:"last_used_at,omitempty"`
	TopupTotalMicrousd int64      `json:"topup_total_microusd,omitempty"`
	LoginProviders     []string   `json:"login_providers,omitempty"`
	ClientTop          *clientTop `json:"client_top,omitempty"`
}

func withInternalUserIDs(users []*model.User) []userWithInternalID {
	items := make([]userWithInternalID, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		items = append(items, userWithInternalID{
			ID:                user.PublicID,
			InternalID:        user.Id,
			Username:          user.Username,
			DisplayName:       user.DisplayName,
			Role:              user.Role,
			Status:            user.Status,
			Email:             user.Email,
			Quota:             user.Quota,
			UsedQuota:         user.UsedQuota,
			StorageQuotaBytes: model.FileStorageQuotaBytes(),
			StorageBytesUsed:  user.StorageBytesUsed,
			HasToppedUp:       user.HasToppedUp,
			PricePlanID:       user.PricePlanID,
			RPMLimit:          user.RPMLimit,
			TPMLimit:          user.TPMLimit,
			CreatedAt:         user.CreatedAt,
			CreatedAtSource:   user.CreatedAtSource,
			FirstRequestAt:    user.FirstRequestAt,
			LastRequestAt:     user.LastRequestAt,
			FirstTopupAt:      user.FirstTopupAt,
			DeletedAt:         user.DeletedAt,
			Remark:            user.Remark,
			needsPassword:     user.NeedsPassword,
		})
	}
	return items
}

func loginProvidersForUser(identityProviders []string, email string, needsPassword bool) []string {
	providers := make([]string, 0, len(identityProviders)+2)
	appendProvider := func(provider string) {
		for _, existing := range providers {
			if existing == provider {
				return
			}
		}
		providers = append(providers, provider)
	}
	for _, provider := range identityProviders {
		if common.AuthProviderEnabled(provider) {
			appendProvider(provider)
		}
	}
	if strings.TrimSpace(email) != "" && common.AuthProviderEnabled("email") {
		appendProvider("email")
	}
	if common.AuthProviderEnabled("password") && !needsPassword {
		appendProvider("password")
	}
	return providers
}

// LookupCustomerAccount resolves a ledger deep link without walking fuzzy
// search pages. The dedicated route is intentionally restricted to ordinary
// customer accounts and returns the same safe decorated shape as the list.
func LookupCustomerAccount(c *gin.Context) {
	user, err := model.GetCustomerByIdentifier(c.Query("account"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	items, err := decorateUserList([]*model.User{user}, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if len(items) != 1 {
		httperr.Code(c, types.ErrorCodeUserNotFound)
		return
	}
	common.ApiSuccess(c, items[0])
}

// decorateUserList attaches the per-account key count and trailing-window
// spend when requested. The aggregate runs over this page's ids only.
func decorateUserList(users []*model.User, withUsage bool) ([]userWithInternalID, error) {
	items := withInternalUserIDs(users)
	committed := make(map[int]billing.MicroUSD, len(items))
	for _, item := range items {
		committed[item.InternalID] = item.Quota
	}
	balances := model.ReadConsoleWalletBalances(context.Background(), committed)
	for i := range items {
		items[i].Quota = balances[items[i].InternalID]
	}
	if !withUsage || len(items) == 0 {
		return items, nil
	}

	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.InternalID)
	}
	since := time.Now().AddDate(0, 0, -userUsageWindowDays).Unix()
	summaries, err := model.GetUserUsageSummaries(ids, since)
	if err != nil {
		return nil, err
	}
	providers, err := model.GetUserIdentityProviders(ids)
	if err != nil {
		return nil, err
	}
	for index := range items {
		summary := summaries[items[index].InternalID]
		tokenCount := summary.TokenCount
		spend := summary.SpendMicrousd
		requestCount := summary.RequestCount
		items[index].TokenCount = &tokenCount
		items[index].RecentSpend = &spend
		items[index].RecentSpendDays = userUsageWindowDays
		items[index].RequestCountRecent = &requestCount
		items[index].LastUsedAt = summary.LastUsedAt
		items[index].TopupTotalMicrousd = summary.TopupTotalMicrousd.Int64()
		if plan, ok := model.GetPricePlan(items[index].PricePlanID); ok {
			items[index].PricePlanName = plan.Name
		}
		items[index].LoginProviders = loginProvidersForUser(
			providers[items[index].InternalID],
			items[index].Email,
			items[index].needsPassword,
		)
		if summary.TopFramework != "" || summary.TopCountry != "" {
			items[index].ClientTop = &clientTop{
				Framework: summary.TopFramework,
				Country:   summary.TopCountry,
			}
		}
	}
	return items, nil
}

func GetUser(c *gin.Context) {
	id, err := resolveUserID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.CheckUserManageAllowed(c.GetInt("role"), "view"); err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user,
	})
	return
}

// resolveAdminTargetUser resolves the :id path segment to a user row and
// enforces the same guard as GetUser: an actor may only inspect strictly
// lower-privileged accounts, unless the actor is root. On any failure it
// writes the response and returns ok=false.
func resolveAdminTargetUser(c *gin.Context) (*model.User, bool) {
	id, err := resolveUserID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return nil, false
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return nil, false
	}
	if err := model.CheckUserManageAllowed(c.GetInt("role"), "view"); err != nil {
		httperr.Write(c, err)
		return nil, false
	}
	return user, true
}

// GetUserSummary returns the platform-wide account census for the console's
// customer-ledger header cards.
func GetUserSummary(c *gin.Context) {
	activeSince := time.Now().AddDate(0, 0, -userUsageWindowDays).Unix()
	stats, err := model.GetUserSummaryStats(activeSince)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, stats)
}

// GetUserAccountSummary returns the single-account drawer: identity, wallet,
// tier context, usage window aggregates and operational health for one user.
func GetUserAccountSummary(c *gin.Context) {
	user, ok := resolveAdminTargetUser(c)
	if !ok {
		return
	}

	since := time.Now().AddDate(0, 0, -userUsageWindowDays).Unix()
	agg, err := model.GetUserAccountUsageAggregate(user.Id, since)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	topupTotal, err := model.GetUserTopupTotalMicrousd(user.Id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	mediaTotal, mediaFailed, err := model.GetUserMediaJobCounts(user.Id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	openSettlements, err := model.GetUserUnresolvedSettlementFailureCount(user.Id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var waivedSettlements int64
	if err := model.CoreDB().WithContext(c.Request.Context()).Model(&model.LedgerLegacySettlementWaiver{}).Where("user_id = ?", user.Id).Count(&waivedSettlements).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	providers, err := model.GetUserIdentityProviders([]int{user.Id})
	if err != nil {
		httperr.Write(c, err)
		return
	}

	spend30d := agg.RecentSpend.Int64()
	rpm, tpm := setting.AccountRateLimit(user.HasToppedUp, user.RPMLimit, user.TPMLimit)
	rateLimit := gin.H{
		"enabled":      setting.ModelRequestRateLimitEnabled,
		"rpm":          rpm,
		"tpm":          tpm,
		"rpm_override": user.RPMLimit,
		"tpm_override": user.TPMLimit,
	}

	clientBreakdown := agg.ClientBreakdown
	if clientBreakdown == nil {
		clientBreakdown = []model.ClientBreakdownEntry{}
	}
	countries := agg.Countries
	if countries == nil {
		countries = []model.CountryBreakdownEntry{}
	}
	loginProviders := loginProvidersForUser(providers[user.Id], user.Email, user.NeedsPassword)
	attribution, err := model.GetSignupAttribution(user.Id)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"signup": gin.H{
			"created_at":        user.CreatedAt,
			"created_at_source": user.CreatedAtSource,
			"first_request_at":  user.FirstRequestAt,
			"last_request_at":   user.LastRequestAt,
			"first_topup_at":    user.FirstTopupAt,
			"attribution":       attribution,
		},
		"id":                    user.PublicID,
		"internal_id":           user.Id,
		"username":              user.Username,
		"display_name":          user.DisplayName,
		"email":                 user.Email,
		"role":                  user.Role,
		"status":                user.Status,
		"remark":                user.Remark,
		"quota":                 model.ReadConsoleWalletBalance(c.Request.Context(), user.Id, user.Quota).Int64(),
		"used_quota_microusd":   user.UsedQuota.Int64(),
		"topup_total_microusd":  topupTotal.Int64(),
		"price_plan":            pricePlanViewFor(user.PricePlanID),
		"storage_bytes_used":    user.StorageBytesUsed,
		"storage_quota_bytes":   model.FileStorageQuotaBytes(),
		"request_count_recent":  agg.RequestCount,
		"last_used_at":          agg.LastUsedAt,
		"recent_spend_microusd": spend30d,
		"rate_limit":            rateLimit,
		"login_providers":       loginProviders,
		"client_breakdown":      clientBreakdown,
		"countries":             countries,
		"media_jobs":            gin.H{"total": mediaTotal, "failed": mediaFailed},
		"settlement_failures":   gin.H{"open": openSettlements, "waived_legacy": waivedSettlements},
		"agent_sessions":        agg.AgentSessions,
	})
}

type safeAdminWalletLedgerEvent struct {
	ID            string           `json:"id"`
	DeltaMicrousd billing.MicroUSD `json:"delta_microusd"`
	Kind          string           `json:"kind"`
	Action        string           `json:"action,omitempty"`
	ActorUsername string           `json:"actor_username,omitempty"`
	CreatedAt     int64            `json:"created_at"`
}

// GetUserWalletLedgerEvents returns the safe, authoritative wallet timeline
// used by the account drawer. Reference IDs and metadata never leave the API.
func GetUserWalletLedgerEvents(c *gin.Context) {
	user, ok := resolveAdminTargetUser(c)
	if !ok {
		return
	}
	rows, err := model.GetUserWalletLedgerEvents(user.Id, 8)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	items := make([]safeAdminWalletLedgerEvent, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeAdminWalletLedgerEvent{
			ID:            row.ID,
			DeltaMicrousd: row.DeltaMicrousd,
			Kind:          row.Kind,
			Action:        row.Action,
			ActorUsername: row.ActorUsername,
			CreatedAt:     row.CreatedAt.Unix(),
		})
	}
	common.ApiSuccess(c, items)
}

// safeAdminToken is the projection returned by GetUserTokens. It deliberately
// omits the plaintext key and its digest.
type safeAdminToken struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	KeyPrefix      string           `json:"key_prefix"`
	KeySuffix      string           `json:"key_suffix"`
	Status         int              `json:"status"`
	RemainQuota    billing.MicroUSD `json:"remain_quota"`
	UnlimitedQuota bool             `json:"unlimited_quota"`
	RPMLimit       int              `json:"rpm_limit"`
	TPMLimit       int              `json:"tpm_limit"`
	ModelLimits    string           `json:"model_limits"`
	AllowIps       string           `json:"allow_ips"`
	LastUsedAt     int64            `json:"last_used_at,omitempty"`
	UsedQuota      billing.MicroUSD `json:"used_quota"`
	Group          string           `json:"group"`
	CreatedTime    int64            `json:"created_time"`
	ExpiredTime    int64            `json:"expired_time"`
}

// GetUserTokens returns a page of a single account's API keys, projected to
// safe fields only (never the key or its digest).
func GetUserTokens(c *gin.Context) {
	user, ok := resolveAdminTargetUser(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.GetUserTokensAdmin(user.Id, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	items := make([]safeAdminToken, 0, len(tokens))
	remaining := tokenDisplayRemaining(c, tokens)
	tokenIDs := make([]int, 0, len(tokens))
	for _, token := range tokens {
		if token != nil && token.Id > 0 {
			tokenIDs = append(tokenIDs, token.Id)
		}
	}
	for _, token := range tokens {
		if token == nil {
			continue
		}
		allowIps := ""
		if token.AllowIps != nil {
			allowIps = *token.AllowIps
		}
		items = append(items, safeAdminToken{
			ID:             token.PublicID,
			Name:           token.Name,
			KeyPrefix:      token.KeyPrefix,
			KeySuffix:      token.KeySuffix,
			Status:         token.Status,
			RemainQuota:    remaining[token.Id],
			UnlimitedQuota: token.UnlimitedQuota,
			RPMLimit:       token.RPMLimit,
			TPMLimit:       token.TPMLimit,
			ModelLimits:    token.ModelLimits,
			AllowIps:       allowIps,
			LastUsedAt:     token.LastUsedAt,
			UsedQuota:      token.UsedQuota,
			Group:          token.Group,
			CreatedTime:    token.CreatedTime,
			ExpiredTime:    token.ExpiredTime,
		})
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetUserAccountDashboard returns the target account's own self-scoped usage
// dashboard (totals/by_model/time_series) for the per-user usage chart.
func GetUserAccountDashboard(c *gin.Context) {
	user, ok := resolveAdminTargetUser(c)
	if !ok {
		return
	}
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	usage, err := model.BuildDashboardUsageForRoleContext(c.Request.Context(), user.Id, common.RoleCommonUser, startTimestamp, endTimestamp)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, usage)
}

func GenerateAccessToken(c *gin.Context) {
	id := c.GetInt("id")
	user, err := model.GetUserById(id, true)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// get rand int 28-32
	randI := common.GetRandomInt(4)
	key, err := common.GenerateRandomKey(29 + randI)
	if err != nil {
		httperr.Write(c, fmt.Errorf("generate access token: %w", err))
		return
	}
	user.SetAccessToken(key)

	if model.CoreDB().Where("access_token = ?", user.AccessToken).First(user).RowsAffected != 0 {
		httperr.Write(c, types.WrapErr(errors.New("generated access token collided with an existing one"), types.ErrorCodeUUIDCollision))
		return
	}

	if err := model.UpdateUserAccessToken(user.Id, key); err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.AccessToken,
	})
	return
}

func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	userRole := c.GetInt("role")
	user, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// Hide admin remarks: set to empty to trigger omitempty tag, ensuring the remark field is not included in JSON returned to regular users
	user.Remark = ""

	// 计算用户权限信息
	permissions := calculateUserPermissions(userRole)

	// 获取用户设置并提取sidebar_modules
	userSetting := user.GetSetting()

	// 构建响应数据，包含用户信息和权限
	responseData := map[string]interface{}{
		"id":              user.PublicID,
		"username":        user.Username,
		"display_name":    user.DisplayName,
		"role":            user.Role,
		"status":          user.Status,
		"email":           user.Email,
		"has_topped_up":   user.HasToppedUp,
		"home_surface":    user.HomeSurface,
		"price_plan":      pricePlanViewFor(user.PricePlanID),
		"quota":           model.ReadConsoleWalletBalance(c.Request.Context(), user.Id, user.Quota),
		"used_quota":      user.UsedQuota,
		"setting":         user.Setting,
		"sidebar_modules": userSetting.SidebarModules, // 正确提取sidebar_modules字段
		"permissions":     permissions,                // 新增权限字段
	}

	projects, defaultProjectID, err := model.ListUserProjects(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	responseData["features"] = struct {
		Projects bool `json:"projects"`
	}{Projects: model.ProjectsEnabledForUser(id)}
	responseData["email_verified"] = user.HasVerifiedEmail()
	responseData["projects"] = projects
	responseData["default_project_id"] = defaultProjectID
	// Extra API addresses opened to this account ride on the session payload,
	// so the API Keys page knows whether to show them without a wait of its own.
	responseData["access_addresses"] = selfAccessAddresses(id)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responseData,
	})
	return
}

// calculateUserPermissions tells the console which sidebar controls the
// reader may use. Administrators (role 100) see every module and have no
// sidebar customisation; normal users customise only customer modules.
func calculateUserPermissions(userRole int) map[string]interface{} {
	if userRole >= common.RoleRootUser {
		return map[string]interface{}{
			"sidebar_settings": false,
			"sidebar_modules":  map[string]interface{}{},
		}
	}
	return map[string]interface{}{
		"sidebar_settings": true,
		"sidebar_modules": map[string]interface{}{
			"admin": false,
		},
	}
}

func GetUserModelsClassified(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	if _, err := model.GetUserCache(id); err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    classifyModelNames(model.GetEnabledModels()),
	})
}

func classifyModelNames(modelNames []string) map[string][]model.ModelClassifiedItem {
	modelNames = append([]string(nil), modelNames...)
	sort.Strings(modelNames)

	// Build pricing lookup maps
	pricingList := model.GetPricing()
	type pricingInfo struct {
		ModalityOutput         []string
		SupportedEndpointTypes []constant.EndpointType
	}
	pricingMap := make(map[string]pricingInfo, len(pricingList))
	for _, p := range pricingList {
		pricingMap[p.ModelName] = pricingInfo{
			ModalityOutput:         p.ModalityOutput,
			SupportedEndpointTypes: p.SupportedEndpointTypes,
		}
	}

	classified := map[string][]model.ModelClassifiedItem{
		"video": {},
		"image": {},
		"audio": {},
	}

	for _, modelName := range modelNames {
		categories := map[string]bool{}

		// Priority 1: ModalityOutput from tags → DeriveModality
		info, ok := pricingMap[modelName]
		if ok {
			for _, mod := range info.ModalityOutput {
				switch mod {
				case "video":
					categories["video"] = true
				case "image":
					categories["image"] = true
				case "audio":
					categories["audio"] = true
				}
			}
		}

		// Priority 2: SupportedEndpointTypes
		endpointTypes := info.SupportedEndpointTypes
		if len(endpointTypes) == 0 {
			endpointTypes = model.GetModelSupportEndpointTypes(modelName)
		}
		for _, et := range endpointTypes {
			switch et {
			case constant.EndpointTypeImageGeneration:
				categories["image"] = true
			case constant.EndpointTypeVideoGeneration:
				categories["video"] = true
			}
		}

		// Priority 3: Name pattern matching fallback
		if len(categories) == 0 {
			normalized := strings.ToLower(modelName)
			switch {
			case strings.Contains(normalized, "sora") || strings.Contains(normalized, "kling") ||
				strings.Contains(normalized, "veo") || strings.Contains(normalized, "vidu") ||
				strings.Contains(normalized, "video"):
				categories["video"] = true
			case strings.Contains(normalized, "audio"):
				categories["audio"] = true
			case strings.Contains(normalized, "image"):
				categories["image"] = true
			}
		}

		item := model.ModelClassifiedItem{
			ModelName:    modelName,
			ParamsSchema: model.GetModelParamsSchema(modelName),
		}
		for cat := range categories {
			classified[cat] = append(classified[cat], item)
		}
	}
	sort.Slice(classified["video"], func(i, j int) bool {
		return classified["video"][i].ModelName < classified["video"][j].ModelName
	})
	sort.Slice(classified["image"], func(i, j int) bool {
		return classified["image"][i].ModelName < classified["image"][j].ModelName
	})
	sort.Slice(classified["audio"], func(i, j int) bool {
		return classified["audio"][i].ModelName < classified["audio"][j].ModelName
	})
	return classified
}

func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	if _, err := model.GetUserCache(id); err != nil {
		httperr.Write(c, err)
		return
	}
	models := model.GetEnabledModels()
	sort.Strings(models)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
	return
}

// adjustmentReason reads the optional free-text reason an admin attaches to a
// balance change. It never touches the user record itself.
func adjustmentReason(requestData map[string]json.RawMessage) string {
	raw, ok := requestData["reason"]
	if !ok {
		return ""
	}
	var reason string
	if err := json.Unmarshal(raw, &reason); err != nil {
		return ""
	}
	if runes := []rune(reason); len(runes) > 255 {
		reason = string(runes[:255])
	}
	return reason
}

// AdjustUserQuota applies an operator-entered signed wallet movement. Unlike
// the general user update endpoint, this accepts no absolute balance snapshot:
// the model layer updates quota with a SQL delta and appends the matching
// ledger row in the same transaction, so concurrent spend/top-ups are retained.
func AdjustUserQuota(c *gin.Context) {
	id, err := resolveUserID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var payload struct {
		DeltaMicrousd billing.MicroUSD `json:"delta_microusd"`
		Reason        string           `json:"reason"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if payload.DeltaMicrousd == 0 {
		httperr.Code(c, types.ErrorCodeBalanceAdjustmentAmountZero)
		return
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		httperr.Code(c, types.ErrorCodeBalanceAdjustmentReasonRequired)
		return
	}
	if runes := []rune(reason); len(runes) > 255 {
		reason = string(runes[:255])
	}

	user, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.CheckUserManageAllowed(c.GetInt("role"), "update"); err != nil {
		httperr.Write(c, err)
		return
	}

	refID := fmt.Sprintf("admin:%d:user:%d:adjust:%d", c.GetInt("id"), id, time.Now().UnixNano())
	err = model.AdjustUserQuotaWithLedger(model.AdjustUserQuotaWithLedgerInput{
		UserID:         id,
		DeltaQuota:     payload.DeltaMicrousd,
		Kind:           model.WalletLedgerKindAdjustment,
		RefType:        "admin_user_adjustment",
		RefID:          refID,
		PricingVersion: 1,
		Metadata: map[string]any{
			"admin_id":       c.GetInt("id"),
			"admin_username": c.GetString("username"),
			"reason":         reason,
		},
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}

	adminInfo := map[string]interface{}{
		"admin_id":       c.GetInt("id"),
		"admin_username": c.GetString("username"),
	}
	model.RecordLogWithAdminInfo(id, model.LogTypeManage, fmt.Sprintf("管理员调整用户余额 %s（原因：%s）", logger.LogQuota(payload.DeltaMicrousd), reason), adminInfo)
	service.NotifyBalanceChange(id, payload.DeltaMicrousd, service.BalanceReasonAdminAdjust)
	model.WriteAdminAuditLog(c, "user.quota.adjust", "user", user.PublicID, user.Username, nil, gin.H{
		"delta_microusd": payload.DeltaMicrousd,
		"reason":         reason,
	})
	common.ApiSuccess(c, gin.H{
		"delta_microusd": payload.DeltaMicrousd,
	})
}

func userUpdateHasNonQuotaChanges(before, after *model.User, requestData map[string]json.RawMessage) bool {
	if before == nil || after == nil {
		return false
	}
	if before.Username != after.Username || before.DisplayName != after.DisplayName ||
		before.Email != after.Email || before.Remark != after.Remark ||
		before.PricePlanID != after.PricePlanID || before.Role != after.Role ||
		before.Status != after.Status || before.RPMLimit != after.RPMLimit ||
		before.TPMLimit != after.TPMLimit {
		return true
	}
	if raw, ok := requestData["password"]; ok {
		var password string
		if json.Unmarshal(raw, &password) == nil && strings.TrimSpace(password) != "" {
			return true
		}
	}
	return false
}

func UpdateUser(c *gin.Context) {
	var requestData map[string]json.RawMessage
	err := json.NewDecoder(c.Request.Body).Decode(&requestData)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	var updatedUser model.User
	userBytes, err := json.Marshal(requestData)
	if err == nil {
		err = json.Unmarshal(userBytes, &updatedUser)
	}
	if err != nil || updatedUser.Id == 0 {
		if rawID, ok := requestData["id"]; ok {
			updatedUser.Id, err = resolveIDRaw("users", rawID)
		}
	}
	if err != nil || updatedUser.Id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	if updatedUser.Password == "" {
		updatedUser.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&updatedUser); err != nil {
		httperr.Write(c, userFieldInvalid(err))
		return
	}
	if _, ok := requestData["role"]; ok && !common.IsValidateRole(updatedUser.Role) {
		httperr.Invalid(c, "role")
		return
	}
	if _, ok := requestData["status"]; ok && updatedUser.Status != common.UserStatusEnabled && updatedUser.Status != common.UserStatusDisabled {
		httperr.Invalid(c, "status")
		return
	}
	originUser, err := model.GetUserById(updatedUser.Id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if _, ok := requestData["role"]; !ok {
		updatedUser.Role = originUser.Role
	}
	if _, ok := requestData["status"]; !ok {
		updatedUser.Status = originUser.Status
	}
	if _, ok := requestData["email"]; !ok {
		updatedUser.Email = originUser.Email
	}
	// user.Edit() writes username / display_name / remark unconditionally, so
	// a partial update that only touches, say, the price plan would blank
	// them. Backfill from the stored record exactly like role/email
	// above: a client changes a field by sending it, not by echoing every
	// other field back from a possibly stale list snapshot.
	if _, ok := requestData["username"]; !ok {
		updatedUser.Username = originUser.Username
	}
	if _, ok := requestData["display_name"]; !ok {
		updatedUser.DisplayName = originUser.DisplayName
	}
	if _, ok := requestData["remark"]; !ok {
		updatedUser.Remark = originUser.Remark
	}
	if _, ok := requestData["price_plan_id"]; !ok {
		updatedUser.PricePlanID = originUser.PricePlanID
	}
	if _, ok := requestData["rpm_limit"]; !ok {
		updatedUser.RPMLimit = originUser.RPMLimit
	}
	if _, ok := requestData["tpm_limit"]; !ok {
		updatedUser.TPMLimit = originUser.TPMLimit
	}
	updatedUser.HasToppedUp = originUser.HasToppedUp
	desiredQuota := updatedUser.Quota
	quotaChanged := false
	if _, ok := requestData["quota"]; ok {
		quotaChanged = originUser.Quota != desiredQuota
	}
	updatedUser.Quota = originUser.Quota
	myRole := c.GetInt("role")
	if err := model.CheckUserManageAllowed(myRole, "update"); err != nil {
		httperr.Write(c, err)
		return
	}
	if updatedUser.Id == c.GetInt("id") && originUser.Role >= common.RoleRootUser && updatedUser.Role < common.RoleRootUser {
		httperr.Code(c, types.ErrorCodeRootUserProtected, types.D{"action": "demoted by itself"})
		return
	}
	if updatedUser.Password == "$I_LOVE_U" {
		updatedUser.Password = "" // rollback to what it should be
	}
	updatePassword := updatedUser.Password != ""
	if err := updatedUser.EditAuthorized(updatePassword, myRole, originUser.Role); err != nil {
		httperr.Write(c, err)
		return
	}
	if quotaChanged {
		// `reason` is not part of the user record: it is the operator's note for
		// this one balance move, so it belongs in the ledger entry, not in the
		// user's remark field.
		ledgerMetadata := map[string]any{
			"admin_id":       c.GetInt("id"),
			"admin_username": c.GetString("username"),
			"previous_quota": originUser.Quota,
			"new_quota":      desiredQuota,
		}
		if reason := strings.TrimSpace(adjustmentReason(requestData)); reason != "" {
			ledgerMetadata["reason"] = reason
		}
		delta, err := model.SetUserQuotaWithLedger(updatedUser.Id, desiredQuota, model.WalletLedgerKindAdjustment, "admin_user_update", fmt.Sprintf("admin:%d:user:%d:update:%d", c.GetInt("id"), updatedUser.Id, time.Now().UnixNano()), ledgerMetadata)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		adminInfo := map[string]interface{}{
			"admin_id":       c.GetInt("id"),
			"admin_username": c.GetString("username"),
		}
		logMessage := fmt.Sprintf("管理员将用户额度从 %s修改为 %s", logger.LogQuota(originUser.Quota), logger.LogQuota(desiredQuota))
		if reason := strings.TrimSpace(adjustmentReason(requestData)); reason != "" {
			logMessage = fmt.Sprintf("%s（原因：%s）", logMessage, reason)
		}
		model.RecordLogWithAdminInfo(originUser.Id, model.LogTypeManage, logMessage, adminInfo)
		service.NotifyBalanceChange(updatedUser.Id, delta, service.BalanceReasonAdminAdjust)
	}
	if originUser.Role != updatedUser.Role {
		if err := model.InvalidateUserTokensCache(updatedUser.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate tokens cache for user %d: %s", updatedUser.Id, err.Error()))
		}
	}
	auditAction := "user.update"
	if quotaChanged && !userUpdateHasNonQuotaChanges(originUser, &updatedUser, requestData) {
		auditAction = "user.quota.adjust"
	}
	model.WriteAdminAuditLog(c, auditAction, "user", originUser.PublicID, originUser.Username, originUser, gin.H{
		"user":  updatedUser,
		"quota": desiredQuota,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func SetUserHasToppedUp(c *gin.Context) {
	id, err := resolveUserID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var payload struct {
		HasToppedUp bool `json:"has_topped_up"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	before, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.SetUserHasToppedUp(id, payload.HasToppedUp); err != nil {
		httperr.Write(c, err)
		return
	}
	after := *before
	after.HasToppedUp = payload.HasToppedUp
	model.WriteAdminAuditLog(c, "user.has_topped_up.update", "user", before.PublicID, before.Username, before, after)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateSelf(c *gin.Context) {
	var requestData map[string]interface{}
	err := json.NewDecoder(c.Request.Body).Decode(&requestData)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}

	// 检查是否是sidebar_modules更新请求
	if sidebarModules, exists := requestData["sidebar_modules"]; exists {
		userId := c.GetInt("id")
		sidebarModulesStr, ok := sidebarModules.(string)
		if !ok {
			httperr.Invalid(c, "sidebar_modules")
			return
		}
		if _, err := updateSidebarModulesLocked(userId, sidebarModulesStr); err != nil {
			httperr.Write(c, err)
			return
		}
		if err := model.InvalidateUserCache(userId); err != nil {
			httperr.Write(c, err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	}

	// 原有的用户信息更新逻辑
	var user model.User
	requestDataBytes, err := json.Marshal(requestData)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	err = json.Unmarshal(requestDataBytes, &user)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}

	if user.Password == "" {
		user.Password = "$I_LOVE_U" // make Validator happy :)
	}
	if err := common.Validate.Struct(&user); err != nil {
		httperr.Write(c, userFieldInvalid(err))
		return
	}

	userID := c.GetInt("id")
	cleanUser := model.User{
		Id:          userID,
		Password:    user.Password,
		DisplayName: user.DisplayName,
	}
	if user.Password == "$I_LOVE_U" {
		user.Password = "" // rollback to what it should be
		cleanUser.Password = ""
	}
	updatePassword := cleanUser.Password != ""
	var current *model.User
	requestedUsername := strings.TrimSpace(user.Username)
	if requestedUsername != "" || updatePassword {
		var verdict usernameVerdict
		current, verdict, err = applySelfProfileUpdate(
			userID,
			requestedUsername,
			cleanUser.DisplayName,
			cleanUser.Password,
			user.OriginalPassword,
		)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if requestedUsername != "" && !verdict.Available && verdict.Status != "unchanged" {
			httperr.Write(c, verdict.err())
			return
		}
		// The session stores the username as well as the revocation version. Refresh
		// it after either credential changes so subsequent audit facts use the new
		// handle and this browser survives a password rotation.
		if _, err := setupSession(current, c); err != nil {
			common.SysLog(fmt.Sprintf("failed to refresh session after profile change for user %d: %v", cleanUser.Id, err))
		}
		if updatePassword {
			sendPasswordChangedEmail(c, current.Email, current.Username, userMailLang(current, c))
		}
	} else if err := cleanUser.Update(false); err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteUser(c *gin.Context) {
	id, err := resolveUserID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	originUser, err := model.GetUserById(id, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	myRole := c.GetInt("role")
	if myRole <= originUser.Role {
		httperr.Code(c, types.ErrorCodeUserManageForbidden, types.D{"action": "delete"})
		return
	}
	deletedUser, err := model.HardDeleteUserAuthorized(id, myRole, originUser.Role)
	if err != nil {
		// Reporting a failed delete as a success left the console showing a
		// row it believed was gone. Now that delete is reachable from the UI,
		// the caller has to hear about it.
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "user.delete", "user", deletedUser.PublicID, deletedUser.Username, deletedUser, nil)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteSelf(c *gin.Context) {
	id := c.GetInt("id")
	_, err := model.DeleteSelfUser(id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func CreateUser(c *gin.Context) {
	var payload struct {
		Username    string            `json:"username" validate:"max=20"`
		Password    string            `json:"password" validate:"min=8,max=20"`
		DisplayName string            `json:"display_name" validate:"max=20"`
		Role        int               `json:"role"`
		Email       string            `json:"email" validate:"max=50"`
		PricePlanID int               `json:"price_plan_id"`
		Quota       *billing.MicroUSD `json:"quota"`
		Remark      string            `json:"remark" validate:"max=255"`
	}
	err := json.NewDecoder(c.Request.Body).Decode(&payload)
	user := model.User{
		Username:    payload.Username,
		Password:    payload.Password,
		DisplayName: payload.DisplayName,
		Role:        payload.Role,
		Email:       payload.Email,
		PricePlanID: payload.PricePlanID,
		Remark:      payload.Remark,
	}
	if payload.Quota != nil {
		user.Quota = *payload.Quota
	}
	user.Username = strings.TrimSpace(user.Username)
	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	if user.Username == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "username"})
		return
	}
	if user.Password == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "password"})
		return
	}
	if err := common.Validate.Struct(&user); err != nil {
		httperr.Write(c, userFieldInvalid(err))
		return
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if user.Role == 0 {
		user.Role = common.RoleCommonUser
	}
	if !common.IsValidateRole(user.Role) {
		httperr.Invalid(c, "role")
		return
	}
	if err := model.CheckUserManageAllowed(c.GetInt("role"), "create"); err != nil {
		httperr.Write(c, err)
		return
	}
	// Even for admin users, we cannot fully trust them!
	cleanUser := model.User{
		Username:    user.Username,
		Password:    user.Password,
		DisplayName: user.DisplayName,
		Role:        user.Role, // 保持管理员设置的角色
		Email:       user.Email,
		PricePlanID: user.PricePlanID,
		Remark:      user.Remark,
	}
	if err := cleanUser.Insert(0); err != nil {
		httperr.Write(c, err)
		return
	}
	if payload.Quota != nil && user.Quota != common.QuotaForNewUser {
		_, err := model.SetUserQuotaWithLedger(cleanUser.Id, user.Quota, model.WalletLedgerKindAdjustment, "admin_user_create", fmt.Sprintf("admin:%d:user:%d:create:%d", c.GetInt("id"), cleanUser.Id, time.Now().UnixNano()), map[string]any{
			"admin_id":       c.GetInt("id"),
			"admin_username": c.GetString("username"),
			"previous_quota": common.QuotaForNewUser,
			"new_quota":      user.Quota,
		})
		if err != nil {
			httperr.Write(c, err)
			return
		}
	}
	cleanUser.Quota = user.Quota
	model.WriteAdminAuditLog(c, "user.create", "user", cleanUser.PublicID, cleanUser.Username, nil, cleanUser)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type ManageRequest struct {
	Id     string `json:"id"`
	Action string `json:"action"`
}

// ManageUser Only admin user can do this
func ManageUser(c *gin.Context) {
	var req ManageRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)

	if err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}
	id, err := resolveUserID(req.Id)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	user := model.User{Id: id}
	// Fill attributes
	if err := model.CoreDB().Unscoped().Where(&user).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httperr.Code(c, types.ErrorCodeUserNotFound)
			return
		}
		httperr.Write(c, err)
		return
	}
	myRole := c.GetInt("role")
	if err := model.CheckUserManageAllowed(myRole, "update"); err != nil {
		httperr.Write(c, err)
		return
	}
	beforeUser := user
	switch req.Action {
	case "disable":
		user.Status = common.UserStatusDisabled
		if user.Role == common.RoleRootUser {
			httperr.Code(c, types.ErrorCodeRootUserProtected, types.D{"action": "disabled"})
			return
		}
	case "enable":
		user.Status = common.UserStatusEnabled
	case "delete":
		if user.Role == common.RoleRootUser {
			httperr.Write(c, model.ErrCannotDeleteRootUser)
			return
		}
		deleted, err := model.DeleteUserWithExpectedRole(user.Id, myRole, user.Role)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		user = *deleted
	case "promote":
		// Two roles: promote makes an administrator (100), demote a normal
		// user (1). The last enabled administrator is kept by the model.
		if user.Role >= common.RoleRootUser {
			httperr.Code(c, types.ErrorCodeUserRoleUnchanged, types.D{"role": "admin"})
			return
		}
		user.Role = common.RoleRootUser
	case "demote":
		if user.Role < common.RoleRootUser {
			httperr.Code(c, types.ErrorCodeUserRoleUnchanged, types.D{"role": "common"})
			return
		}
		if user.Id == c.GetInt("id") {
			httperr.Code(c, types.ErrorCodeRootUserProtected, types.D{"action": "demoted by itself"})
			return
		}
		user.Role = common.RoleCommonUser
	default:
		httperr.Invalid(c, "action")
		return
	}

	if req.Action != "delete" {
		var role *int
		var status *int
		if req.Action == "promote" || req.Action == "demote" {
			role = &user.Role
		} else {
			status = &user.Status
		}
		updated, err := model.UpdateUserRoleStatus(user.Id, myRole, beforeUser.Role, role, status)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		user = *updated
	}
	var afterAudit any = user
	if req.Action == "delete" {
		afterAudit = nil
	}
	model.WriteAdminAuditLog(c, "user.manage."+req.Action, "user", beforeUser.PublicID, beforeUser.Username, beforeUser, afterAudit)
	clearUser := model.User{
		Role:   user.Role,
		Status: user.Status,
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    clearUser,
	})
	return
}

func EmailBind(c *gin.Context) {
	email := c.Query("email")
	code := c.Query("code")
	if !common.VerifyCodeWithKey(email, code, common.EmailVerificationPurpose) {
		httperr.Code(c, types.ErrorCodeVerificationCodeInvalid)
		return
	}
	user := model.User{
		Id: c.GetInt("id"),
	}
	if user.Id <= 0 {
		session := sessions.Default(c)
		user.Id = common.ParseIntFromAny(session.Get("id"))
	}
	if user.Id <= 0 {
		httperr.Code(c, types.ErrorCodeSessionRequired)
		return
	}
	err := user.FillUserById()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	user.Email = email
	// no need to check if this email already taken, because we have used verification code to check it
	err = model.UpdateUserEmail(user.Id, email)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type UpdateUserSettingRequest struct {
	QuotaWarningType      string  `json:"notify_type"`
	QuotaWarningThreshold float64 `json:"quota_warning_threshold"`
	WebhookUrl            string  `json:"webhook_url,omitempty"`
	WebhookSecret         string  `json:"webhook_secret,omitempty"`
	NotificationEmail     string  `json:"notification_email,omitempty"`
	BarkUrl               string  `json:"bark_url,omitempty"`
	GotifyUrl             string  `json:"gotify_url,omitempty"`
	GotifyToken           string  `json:"gotify_token,omitempty"`
	GotifyPriority        int     `json:"gotify_priority,omitempty"`
}

// mergeUserSettingJSON updates only the fields owned by this endpoint. User
// settings also carry onboarding, credential, session, sidebar, and future
// feature state; rebuilding the struct here would silently erase all of it.
func mergeUserSettingJSON(existing string, req UpdateUserSettingRequest) (string, error) {
	settings := make(map[string]json.RawMessage)
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &settings); err != nil {
			return "", fmt.Errorf("invalid stored user setting: %w", err)
		}
		if settings == nil {
			settings = make(map[string]json.RawMessage)
		}
	}
	set := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		settings[key] = encoded
		return nil
	}

	// accept_unpriced_model is retired: whether an unpriced model runs is the
	// operator's call, so a save also clears any value an older client left.
	// record_ip_log is retired too: every request records the caller's IP.
	for _, key := range []string{
		"webhook_url", "webhook_secret", "notification_email", "bark_url",
		"gotify_url", "gotify_token", "gotify_priority", "accept_unpriced_model",
		"record_ip_log",
	} {
		delete(settings, key)
	}
	baseUpdates := map[string]any{
		"notify_type":             req.QuotaWarningType,
		"quota_warning_threshold": req.QuotaWarningThreshold,
	}
	for key, value := range baseUpdates {
		if err := set(key, value); err != nil {
			return "", err
		}
	}

	switch req.QuotaWarningType {
	case dto.NotifyTypeWebhook:
		if err := set("webhook_url", req.WebhookUrl); err != nil {
			return "", err
		}
		if req.WebhookSecret != "" {
			if err := set("webhook_secret", req.WebhookSecret); err != nil {
				return "", err
			}
		}
	case dto.NotifyTypeEmail:
		if req.NotificationEmail != "" {
			if err := set("notification_email", req.NotificationEmail); err != nil {
				return "", err
			}
		}
	case dto.NotifyTypeBark:
		if err := set("bark_url", req.BarkUrl); err != nil {
			return "", err
		}
	case dto.NotifyTypeGotify:
		priority := req.GotifyPriority
		if priority < 0 || priority > 10 {
			priority = 5
		}
		for key, value := range map[string]any{
			"gotify_url":      req.GotifyUrl,
			"gotify_token":    req.GotifyToken,
			"gotify_priority": priority,
		} {
			if err := set(key, value); err != nil {
				return "", err
			}
		}
	}

	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// updateUserSettingLocked reads and merges preferences while holding the same
// row lock used by onboarding retirement. This prevents either writer from
// replacing fields committed by the other from a stale User snapshot.
func updateUserSettingLocked(userID int, req UpdateUserSettingRequest) (string, error) {
	var mergedSetting string
	err := model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var current model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "setting").First(&current, userID).Error; err != nil {
			return err
		}
		var err error
		mergedSetting, err = mergeUserSettingJSON(current.Setting, req)
		if err != nil {
			return err
		}
		if mergedSetting == current.Setting {
			return nil
		}
		result := tx.Model(&model.User{}).Where("id = ?", current.Id).Update("setting", mergedSetting)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("user setting update affected no row")
		}
		return nil
	})
	return mergedSetting, err
}

func updateSidebarModulesLocked(userID int, sidebarModules string) (string, error) {
	var mergedSetting string
	err := model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var current model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "setting").First(&current, userID).Error; err != nil {
			return err
		}
		settings := make(map[string]json.RawMessage)
		if strings.TrimSpace(current.Setting) != "" {
			if err := json.Unmarshal([]byte(current.Setting), &settings); err != nil {
				return fmt.Errorf("invalid stored user setting: %w", err)
			}
			if settings == nil {
				settings = make(map[string]json.RawMessage)
			}
		}
		encodedSidebar, err := json.Marshal(sidebarModules)
		if err != nil {
			return err
		}
		settings["sidebar_modules"] = encodedSidebar
		encodedSetting, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		mergedSetting = string(encodedSetting)
		if mergedSetting == current.Setting {
			return nil
		}
		result := tx.Model(&model.User{}).Where("id = ?", current.Id).Update("setting", mergedSetting)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("user setting update affected no row")
		}
		return nil
	})
	return mergedSetting, err
}

func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest)
		return
	}

	// 验证预警类型
	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "quota_warning_type", "rule": "not_allowed"})
		return
	}

	// 验证预警阈值
	if req.QuotaWarningThreshold <= 0 {
		httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "quota_warning_threshold", "rule": "format"})
		return
	}

	// 如果是webhook类型,验证webhook地址
	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "webhook_url", "rule": "required"})
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.WebhookUrl); err != nil {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "webhook_url", "rule": "url"})
			return
		}
	}

	// 如果是邮件类型，验证邮箱地址
	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		// 验证邮箱格式
		if !strings.Contains(req.NotificationEmail, "@") {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "notification_email", "rule": "format"})
			return
		}
	}

	// 如果是Bark类型，验证Bark URL
	if req.QuotaWarningType == dto.NotifyTypeBark {
		if req.BarkUrl == "" {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "bark_url", "rule": "required"})
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.BarkUrl); err != nil {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "bark_url", "rule": "url"})
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.BarkUrl, "https://") && !strings.HasPrefix(req.BarkUrl, "http://") {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "bark_url", "rule": "url"})
			return
		}
	}

	// 如果是Gotify类型，验证Gotify URL和Token
	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "gotify_url", "rule": "required"})
			return
		}
		if req.GotifyToken == "" {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "gotify_token", "rule": "required"})
			return
		}
		// 验证URL格式
		if _, err := url.ParseRequestURI(req.GotifyUrl); err != nil {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "gotify_url", "rule": "url"})
			return
		}
		// 检查是否是HTTP或HTTPS
		if !strings.HasPrefix(req.GotifyUrl, "https://") && !strings.HasPrefix(req.GotifyUrl, "http://") {
			httperr.Code(c, types.ErrorCodeSettingInvalid, types.D{"field": "gotify_url", "rule": "url"})
			return
		}
	}

	userId := c.GetInt("id")
	_, err := updateUserSettingLocked(userId, req)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.InvalidateUserCache(userId); err != nil {
		httperr.Write(c, err)
		return
	}
	if err := model.InvalidateUserTokensCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate tokens cache for user %d after setting update: %s", userId, err.Error()))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
