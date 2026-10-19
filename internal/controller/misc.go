package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/console_setting"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

func getRecentAnnouncements() []map[string]interface{} {
	rawAnnouncements := console_setting.GetConsoleSetting().Announcements
	normalizedJSON, normalizedList, changed, err := console_setting.NormalizeAnnouncementsJSONString(rawAnnouncements)
	if err != nil {
		common.SysError("failed to normalize announcements: " + err.Error())
		return console_setting.GetAnnouncements()
	}

	if changed {
		if err := model.UpdateOption("console_setting.announcements", normalizedJSON); err != nil {
			common.SysError("failed to persist pruned announcements: " + err.Error())
		}
	}

	return normalizedList
}

func TestStatus(c *gin.Context) {
	if err := model.PingDB(); err != nil {
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeDatabaseUnreachable))
		return
	}
	// 获取HTTP统计信息
	httpStats := middleware.GetStats()
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "",
		"http_stats": httpStats,
	})
}

func GetStatus(c *gin.Context) {
	cs := console_setting.GetConsoleSetting()
	common.OptionMapRWMutex.RLock()
	headerNavModules := common.OptionMap["HeaderNavModules"]
	sidebarModulesAdmin := common.OptionMap["SidebarModulesAdmin"]
	sidebarModulePreset := common.OptionMap["SidebarModulePreset"]
	common.OptionMapRWMutex.RUnlock()

	legalSetting := system_setting.GetLegalSettings()

	data := gin.H{
		"version":                  common.Version,
		"start_time":               common.StartTime,
		"email_verification":       common.EmailVerificationEnabled,
		"system_name":              common.SystemName,
		"logo":                     common.Logo,
		"footer_html":              common.Footer,
		"server_address":           system_setting.ServerAddress,
		"docs_link":                operation_setting.GetGeneralSetting().DocsLink,
		"source_url":               common.SiteSourceURL(),
		"micro_usd_per_usd":        billing.MicroUSDPerUSD,
		"quota_display_type":       operation_setting.GetQuotaDisplayType(),
		"enable_batch_update":      false,
		"enable_data_export":       common.DataExportEnabled,
		"data_export_default_time": common.DataExportDefaultTime,

		// 面板启用开关
		"uptime_kuma_enabled":   cs.UptimeKumaEnabled,
		"announcements_enabled": cs.AnnouncementsEnabled,
		"faq_enabled":           cs.FAQEnabled,

		// 模块管理配置
		"HeaderNavModules":    headerNavModules,
		"SidebarModulesAdmin": sidebarModulesAdmin,
		"SidebarModulePreset": sidebarModulePreset,

		// The wallet draws its amount row from these before any wallet query
		// answers; the same numbers gate /api/user/stripe/pay.
		"enable_stripe_topup": stripeTopupEnabled(),
		"stripe_min_topup":    getStripeMinTopup(),
		"stripe_max_topup":    getStripeMaxTopup(),

		"user_agreement_enabled": legalSetting.HasUserAgreement(),
		"privacy_policy_enabled": legalSetting.HasPrivacyPolicy(),
	}
	for _, add := range statusFields {
		add(c, data)
	}

	// 根据启用状态注入可选内容
	if cs.AnnouncementsEnabled {
		announcements, err := model.ListPublishedAnnouncements(20, requestLocale(c), 0)
		if err != nil {
			common.SysError("failed to load announcements: " + err.Error())
			data["announcements"] = []map[string]interface{}{}
		} else {
			for i := range announcements {
				announcements[i].Source = ""
				announcements[i].SourceModel = ""
				announcements[i].SourceCommitTo = ""
			}
			data["announcements"] = announcements
		}
	}
	if cs.FAQEnabled {
		data["faq"] = console_setting.GetFAQ()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
	return
}

func GetNotice(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["Notice"],
	})
	return
}

func GetUserAgreement(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    system_setting.GetLegalSettings().UserAgreementFor(c.Query("lang")),
	})
	return
}

func GetPrivacyPolicy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    system_setting.GetLegalSettings().PrivacyPolicyFor(c.Query("lang")),
	})
	return
}

func GetHomePageContent(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    common.OptionMap["HomePageContent"],
	})
	return
}

func SendEmailVerification(c *gin.Context) {
	email := c.Query("email")
	// The same address policy as the email-code door: shape, domain allowlist
	// and alias restriction, each with its own code.
	if err := validateEmailForAuth(email); err != nil {
		httperr.Write(c, err)
		return
	}
	if !allowMailTo(c, email) {
		return
	}

	// Account lookup, token issuance and mail delivery all stay off the request
	// path. Moving only SMTP out of band still leaks registered addresses through
	// the synchronous database branch.
	lang := requestMailLang(c)
	enqueueAuthEmailJob("auth-verification-request", func() {
		dispatchEmailVerification(email, lang)
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func dispatchEmailVerification(email string, lang string) {
	if model.IsEmailAlreadyTaken(email) {
		// The response is identical to the unregistered case: telling the
		// requester which addresses already exist hands an attacker a free
		// account-enumeration oracle. Only the mailbox owner learns the truth,
		// and they get a route back in instead of a dead end.
		subject, body := authAccountExistsMail(lang)
		sendAuthEmail(subject, email, body)
		return
	}
	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	subject, body := authVerificationMail(lang, code)
	sendAuthEmail(subject, email, body)
}

func SendPasswordResetEmail(c *gin.Context) {
	email := c.Query("email")
	if err := common.Validate.Var(email, "required,email"); err != nil {
		httperr.Invalid(c, "email")
		return
	}
	if !allowMailTo(c, email) {
		return
	}
	// Every branch below answers identically. Whether an address is registered,
	// and whether that account even uses a password, is told only to the
	// mailbox owner — never to whoever submitted the form.
	lang := requestMailLang(c)
	enqueueAuthEmailJob("auth-password-reset-request", func() {
		dispatchPasswordResetEmail(email, lang)
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// dispatchPasswordResetEmail decides which mail, if any, the address receives.
// It runs after the response is decided, so its branches cannot leak timing or
// content differences back to the requester.
func dispatchPasswordResetEmail(email string, requestLang string) {
	user, err := model.GetUserByEmail(email)
	if err != nil || user == nil {
		return
	}
	canonicalEmail := strings.TrimSpace(user.Email)
	if canonicalEmail == "" {
		return
	}
	// A social-only account has a random placeholder password, so a reset link
	// would strand the user. Point them at the provider they actually use.
	lang := userMailLang(user, nil)
	if user.GetSetting().Language == "" {
		lang = requestLang
	}
	if user.NeedsPassword {
		subject, body := authPasswordNotUsedMail(lang, linkedProviderLabel(user.Id))
		sendAuthEmail(subject, canonicalEmail, body)
		return
	}
	code := common.GenerateVerificationCode(0)
	verificationKey := model.PasswordResetVerificationKey(user.Id, user.SessionVersion)
	common.RegisterVerificationCodeWithKey(verificationKey, code, common.PasswordResetPurpose)
	// The reset page is served by the console, which can live on a different
	// origin than the API, so the link is built from the frontend base URL.
	link := fmt.Sprintf(
		"%s/user/reset?email=%s&user_id=%d&session_version=%d&token=%s",
		authEmailBaseURL(),
		url.QueryEscape(canonicalEmail),
		user.Id,
		user.SessionVersion,
		url.QueryEscape(code),
	)
	subject, body := authPasswordResetMail(lang, link)
	sendAuthEmail(subject, canonicalEmail, body)
}

type PasswordResetRequest struct {
	Email                string `json:"email"`
	UserID               int    `json:"user_id"`
	IssuedSessionVersion *int   `json:"session_version"`
	Token                string `json:"token"`
	Password             string `json:"password"`
}

// Password rules a reset checks, sent as details.rule with password_invalid.
const (
	passwordRuleMinLength = "min_length"
	passwordRuleMaxLength = "max_length"
	passwordRuleMaxBytes  = "max_bytes"
)

// validatePasswordResetRequest returns the first problem with a reset
// request, or nil.
func validatePasswordResetRequest(req PasswordResetRequest) error {
	switch {
	case strings.TrimSpace(req.Email) == "":
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "email"})
	case req.UserID <= 0:
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "user_id"})
	case req.IssuedSessionVersion == nil || *req.IssuedSessionVersion < 0:
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "session_version"})
	case strings.TrimSpace(req.Token) == "":
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "token"})
	case req.Password == "":
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "password"})
	}
	passwordLength := utf8.RuneCountInString(req.Password)
	if passwordLength < 8 {
		return types.Err(types.ErrorCodePasswordInvalid, types.D{"field": "password", "rule": passwordRuleMinLength, "limit": 8})
	}
	if passwordLength > 64 {
		return types.Err(types.ErrorCodePasswordInvalid, types.D{"field": "password", "rule": passwordRuleMaxLength, "limit": 64})
	}
	if len([]byte(req.Password)) > 72 {
		return types.Err(types.ErrorCodePasswordInvalid, types.D{"field": "password", "rule": passwordRuleMaxBytes, "limit": 72})
	}
	return nil
}

func ResetPassword(c *gin.Context) {
	// Cached v1 clients do not submit a user-selected password. Fail without
	// consuming the token so users can refresh onto the v2 form safely.
	httperr.Code(c, types.ErrorCodePasswordResetClientUpgradeRequired)
}

func ResetPasswordV2(c *gin.Context) {
	var req PasswordResetRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"reason": "the body must be a JSON object"})
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Token = strings.TrimSpace(req.Token)
	if err := validatePasswordResetRequest(req); err != nil {
		httperr.Write(c, err)
		return
	}
	// The request carries the non-secret owner/version claims from the email
	// link. Do not look up the submitted address here: arbitrary invalid reset
	// attempts must take the same path whether or not that mailbox exists.
	resetUser, err := model.ResetUserPasswordWithToken(
		req.Email,
		req.UserID,
		*req.IssuedSessionVersion,
		req.Token,
		req.Password,
	)
	if err != nil {
		// A consumed reset token is deliberately never resurrected after a DB
		// failure: retrying the same credential would turn a one-time secret into
		// a replayable one. The owner can request a fresh link. A link that does
		// not match answers password_reset_link_invalid; anything else is
		// logged and answered as internal_error.
		httperr.Write(c, err)
		return
	}
	canonicalEmail := strings.TrimSpace(resetUser.Email)
	cleanUser, sessionErr := setupSession(resetUser, c)
	if sessionErr != nil {
		// The password is already committed and old sessions are revoked. Do not
		// report the reset itself as failed merely because this browser could not
		// persist its convenience auto-login cookie.
		common.SysLog(fmt.Sprintf("password reset session setup failed for user %d: %v", resetUser.Id, sessionErr))
		cleanUser = nil
	}
	sendPasswordChangedEmail(c, canonicalEmail, resetUser.Username, userMailLang(resetUser, c))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanUser,
	})
}
