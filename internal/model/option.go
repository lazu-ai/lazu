package model

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/config"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	_ "github.com/lazu-ai/lazu/internal/setting/performance_setting"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm/clause"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

var ignoredLegacyRuntimeOptionKeys = map[string]struct{}{
	// Retired: the relay's retry budget is fixed in code (see
	// internal/controller/retry_policy.go). An operator-tunable count is what
	// left production with no same-channel retry at all. Ignoring the key stops
	// a console from an older deploy re-creating a knob that does nothing.
	"RetryTimes": {},
	// Folded into email_setting.low_balance_threshold_usd, which was already
	// the value the gateway actually read. See migrateQuotaRemindThreshold.
	"QuotaRemindThreshold":                   {},
	"MinTopUp":                               {},
	"Price":                                  {},
	"ServerAddress":                          {},
	"SMTPAccount":                            {},
	"SMTPToken":                              {},
	"StripeApiSecret":                        {},
	"StripePriceId":                          {},
	"StripePromotionCodesEnabled":            {},
	"StripeSecretKey":                        {},
	"StripeWebhookSecret":                    {},
	"TopUpLink":                              {},
	"DisplayInCurrencyEnabled":               {},
	"USDExchangeRate":                        {},
	"CustomCurrencySymbol":                   {},
	"CustomCurrencyExchangeRate":             {},
	"general_setting.custom_currency_symbol": {},
	"general_setting.custom_currency_exchange_rate": {},
}

func isIgnoredLegacyRuntimeOption(key string) bool {
	_, ok := ignoredLegacyRuntimeOptionKeys[key]
	return ok
}

func IsIgnoredLegacyOption(key string) bool {
	return isIgnoredLegacyRuntimeOption(key) || isRemovedLegacyMediaOption(key) || isRemovedLegacyAuthOption(key) || isRemovedLegacyReferralOption(key) || isRetiredEmailOption(key)
}

// isRetiredEmailOption covers the editable mail templates and payment mail
// switches retired with the fixed mail system.
func isRetiredEmailOption(key string) bool {
	for _, retired := range email_setting.RetiredOptionKeys {
		if key == retired {
			return true
		}
	}
	return false
}

func isRemovedLegacyReferralOption(key string) bool {
	for _, legacyKey := range legacyReferralOptionKeys {
		if key == legacyKey {
			return true
		}
	}
	return false
}

func isRemovedLegacyMediaOption(key string) bool {
	for _, legacyKey := range legacyMediaOptionKeys {
		if key == legacyKey {
			return true
		}
	}
	return false
}

func isRemovedLegacyAuthOption(key string) bool {
	for _, legacyKey := range legacyAuthOptionKeys {
		if key == legacyKey {
			return true
		}
	}
	return false
}

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = CoreDB().Find(&options).Error
	return options, err
}

func initializeOptionMapDefaults() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMap[LedgerBacklogAlertAmountOption] = "10000000"
	common.OptionMap[LedgerBacklogHardLimitOption] = "100000000"
	common.OptionMap[LedgerNegativeBalanceAlertOption] = "5000000"
	common.OptionMap[FileStorageQuotaBytesOption] = strconv.FormatInt(defaultFileStorageQuotaBytes, 10)
	common.OptionMap[common.DecisionsEnabledOption] = "false"
	common.OptionMap[common.DecisionQuestionGenerationEnabledOption] = "false"
	common.OptionMap[common.DecisionQuestionGenerationDefaultModelOption] = "gpt-6-luna"

	// Seed runtime-adjustable settings. Secrets and startup credentials stay in env/yaml.
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["EmailCodeAuthEnabled"] = strconv.FormatBool(common.EmailCodeAuthEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap["UsageDigestEnabled"] = strconv.FormatBool(common.UsageDigestEnabled)
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ProjectsEnabled"] = strconv.FormatBool(common.ProjectsEnabled)
	common.OptionMap["ProjectsAllowedUsers"] = common.ProjectsAllowedUsers
	common.OptionMap[AccessAddressesOptionKey] = ""
	common.OptionMap[ModelMakerOrderOptionKey] = EncodeModelMakerOrder(ModelMakerOrder())
	common.OptionMap[ModelsDevSyncLastRunOptionKey] = ""
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	// SMTP connection metadata can be adjusted at runtime; credentials stay in env/yaml.
	common.OptionMap["SMTPServer"] = common.SMTPServer
	common.OptionMap["SMTPFrom"] = common.SMTPFrom
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["Notice"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["SidebarModulePreset"] = "saas"
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["QuotaForNewUser"] = strconv.FormatInt(common.QuotaForNewUser.Int64(), 10)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["MinTopUpUSD"] = strconv.Itoa(setting.StripeMinTopUp)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["RateLimitDefaultRPM"] = strconv.Itoa(setting.RateLimitDefaultRPM)
	common.OptionMap["RateLimitDefaultTPM"] = strconv.Itoa(setting.RateLimitDefaultTPM)
	common.OptionMap["RateLimitUnpaidRPM"] = strconv.Itoa(setting.RateLimitUnpaidRPM)
	common.OptionMap["RateLimitUnpaidTPM"] = strconv.Itoa(setting.RateLimitUnpaidTPM)
	common.OptionMap["ChannelLanes"] = setting.ChannelLanes2JSONString()
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()

	// Add registered model settings.
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}
	applyExtensionOptionDefaults(common.OptionMap)

	common.OptionMapRWMutex.Unlock()
}

func InitOptionMap() {
	initializeOptionMapDefaults()
	loadOptionsFromDatabase()
	if apiControlPlaneReader() {
		return
	}
	for key, value := range system_setting.LegacyLegalDefaultUpdates() {
		if err := UpdateOption(key, value); err != nil {
			common.SysLog("failed to upgrade legacy legal default " + key + ": " + err.Error())
		}
	}
}

// InitMigrationOptionMap initializes settings for the offline schema command.
// Migration writes are published by the jobs process when runtime resumes;
// they must not require a running Redis or silently bypass runtime publishing.
func InitMigrationOptionMap() error {
	initializeOptionMapDefaults()
	options, err := AllOption()
	if err != nil {
		return err
	}
	for _, option := range options {
		if err := updateOptionMap(option.Key, option.Value); err != nil {
			return err
		}
	}
	for key, value := range system_setting.LegacyLegalDefaultUpdates() {
		if err := updateOptionForMigration(key, value); err != nil {
			return err
		}
	}
	return nil
}

// updateOptionForMigration is reserved for explicit schema/backfill operations.
// Administrative callers must use UpdateOption, including its Redis publisher.
func updateOptionForMigration(key, value string) error {
	if err := validateLedgerOption(key, value); err != nil {
		return err
	}
	if err := validateFileStorageOption(key, value); err != nil {
		return err
	}
	if IsIgnoredLegacyOption(key) {
		return nil
	}
	if CoreDB() == nil {
		return errors.New("migration Core database unavailable")
	}
	option := Option{Key: key, Value: value}
	if err := CoreDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&option).Error; err != nil {
		return err
	}
	return updateOptionMap(key, value)
}

func loadOptionsFromDatabase() {
	options, _ := AllOption()
	for _, option := range options {
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		logControlRefresh("options", syncOptionsOnce())
	}
}

func UpdateOption(key string, value string) error {
	if err := validateLedgerOption(key, value); err != nil {
		return err
	}
	if err := validateFileStorageOption(key, value); err != nil {
		return err
	}
	if IsIgnoredLegacyOption(key) {
		common.SysDebug("ignored legacy option update: " + key)
		return nil
	}

	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	if err := CoreDB().FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	if err := CoreDB().Save(&option).Error; err != nil {
		return err
	}
	// Update OptionMap
	if err := updateOptionMap(key, value); err != nil {
		return err
	}
	return publishCommittedOptions()
}

func GetOptionValue(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[key]
}

func updateOptionMap(key string, value string) (err error) {
	if IsIgnoredLegacyOption(key) {
		common.SysDebug("ignored legacy option: " + key)
		return nil
	}
	if key == "general_setting.quota_display_type" {
		value = operation_setting.NormalizeQuotaDisplayType(value)
	}

	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	common.OptionMap[key] = value

	// 检查是否是模型配置 - 使用更规范的方式处理
	if handleConfigUpdate(key, value) {
		return nil // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Enabled") {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "EmailCodeAuthEnabled":
			common.EmailCodeAuthEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "UsageDigestEnabled":
			common.UsageDigestEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "ProjectsEnabled":
			common.ProjectsEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		}
	}
	switch key {
	case "ProjectsAllowedUsers":
		common.ProjectsAllowedUsers = value
	case AccessAddressesOptionKey:
		applyAccessAddressesOption(value)
	case ModelMakerOrderOptionKey:
		applyModelMakerOrderOption(value)
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = ParseEmailDomainWhitelist(value)
	case "SMTPServer":
		// 只有数据库显式设置了非空值才覆盖 config/backend/config.yaml 的值
		if value != "" {
			common.SMTPServer = value
		}
	case "SMTPPort":
		intValue, _ := strconv.Atoi(value)
		if intValue > 0 {
			common.SMTPPort = intValue
		}
	case "SMTPFrom":
		if value != "" {
			common.SMTPFrom = value
		}
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "QuotaForNewUser":
		common.QuotaForNewUser = parseMicroUSDOption(value)
	case "PreConsumedQuota":
		common.PreConsumedQuota, _ = strconv.Atoi(value)
	case "MinTopUpUSD":
		// Whole USD. The retired key MinTopUp stays ignored so a value from an
		// older deploy cannot come back as a floor nobody chose.
		if minimum, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil {
			setting.StripeMinTopUp = setting.NormalizeMinTopUp(minimum)
		}
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, _ = strconv.Atoi(value)
	case "RateLimitDefaultRPM":
		setting.RateLimitDefaultRPM, _ = strconv.Atoi(value)
	case "RateLimitDefaultTPM":
		setting.RateLimitDefaultTPM, _ = strconv.Atoi(value)
	case "RateLimitUnpaidRPM":
		setting.RateLimitUnpaidRPM, _ = strconv.Atoi(value)
	case "RateLimitUnpaidTPM":
		setting.RateLimitUnpaidTPM, _ = strconv.Atoi(value)
	case "ChannelLanes":
		err = setting.UpdateChannelLanesByJSONString(value)
	case "DataExportInterval":
		common.DataExportInterval, _ = strconv.Atoi(value)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, _ = strconv.Atoi(value)
	}
	return err
}

func parseMicroUSDOption(value string) billing.MicroUSD {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return billing.MicroUSD(parsed)
}

// handleConfigUpdate 处理分层配置更新，返回是否已处理
func handleConfigUpdate(key, value string) bool {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false // 不是分层配置
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	config.UpdateConfigFromMap(cfg, configMap)

	return true // 已处理
}

// ParseEmailDomainWhitelist turns the stored comma list into domains.
//
// strings.Split("", ",") is [""], not an empty slice, so a cleared allowlist
// used to leave one empty domain behind. The enable guard in UpdateOption only
// refuses an empty slice, so the restriction could then be switched on with no
// real domain in the list — and every registration and email-code sign-in was
// rejected, because no address ends in "".
func ParseEmailDomainWhitelist(value string) []string {
	domains := make([]string, 0)
	for _, entry := range strings.Split(value, ",") {
		if domain := strings.TrimSpace(entry); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}
