package dto

type UserSetting struct {
	NotifyType            string  `json:"notify_type,omitempty"`             // QuotaWarningType 额度预警类型
	QuotaWarningThreshold float64 `json:"quota_warning_threshold,omitempty"` // QuotaWarningThreshold 额度预警阈值
	WebhookUrl            string  `json:"webhook_url,omitempty"`             // WebhookUrl webhook地址
	WebhookSecret         string  `json:"webhook_secret,omitempty"`          // WebhookSecret webhook密钥
	NotificationEmail     string  `json:"notification_email,omitempty"`      // NotificationEmail 通知邮箱地址
	BarkUrl               string  `json:"bark_url,omitempty"`                // BarkUrl Bark推送URL
	GotifyUrl             string  `json:"gotify_url,omitempty"`              // GotifyUrl Gotify服务器地址
	GotifyToken           string  `json:"gotify_token,omitempty"`            // GotifyToken Gotify应用令牌
	GotifyPriority        int     `json:"gotify_priority"`                   // GotifyPriority Gotify消息优先级
	SidebarModules        string  `json:"sidebar_modules,omitempty"`         // SidebarModules 左侧边栏模块配置
	// OnboardingActive marks an account that has not finished first-run setup.
	// It is written when the account is created, so accounts that predate
	// onboarding are never nagged by it.
	OnboardingActive bool `json:"onboarding_active,omitempty"`
	// NeedsPassword is a compatibility projection of users.needs_password.
	// Credential code must use the dedicated column rather than persisting this
	// value in the general-purpose settings JSON.
	NeedsPassword bool `json:"needs_password,omitempty"`
	// Language is the console interface language the account last used
	// (en, zh, zh-TW, ja …). Mail to the account is written in it.
	Language string `json:"language,omitempty"`
	// Timezone is the account's IANA time zone. The console fills it from the
	// browser once; the settings page changes it. Usage digests cut their
	// periods and pick their send time in it.
	Timezone string `json:"timezone,omitempty"`
	// Mail the account turned off. Every one defaults to on, so the zero
	// value keeps sending.
	WeeklyDigestOff   bool `json:"weekly_digest_off,omitempty"`
	MonthlyDigestOff  bool `json:"monthly_digest_off,omitempty"`
	ProductUpdatesOff bool `json:"product_updates_off,omitempty"`
}

var (
	NotifyTypeEmail   = "email"   // Email 邮件
	NotifyTypeWebhook = "webhook" // Webhook
	NotifyTypeBark    = "bark"    // Bark 推送
	NotifyTypeGotify  = "gotify"  // Gotify 推送
)
