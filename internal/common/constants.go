package common

import (
	//"os"
	//"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lazu-ai/lazu/pkg/billing"
)

var StartTime = time.Now().Unix() // unit: second
var Version = "v0.0.0"            // this hard coding will be replaced automatically when building, no need to manually change
var SystemName = "Lazu"
var Footer = ""
var Logo = ""

var DisplayTokenStatEnabled = true
var DataExportEnabled = true
var DataExportInterval = 5         // unit: minute
var DataExportDefaultTime = "hour" // unit: minute

// Team projects are a runtime switch, not a build-time one: an admin turns
// them on from the settings page and SyncOptions picks it up. The
// PROJECTS_ENABLED / PROJECTS_ALLOWED_USERS environment variables still
// override it, for local development and for deployments configured entirely
// through env.
var ProjectsEnabled = false

// UsageDigestEnabled lets the weekly and monthly usage digests go out. On by
// default; operators can pause it under 运营 › 邮件.
var UsageDigestEnabled = true
var ProjectsAllowedUsers = ""

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()
var CryptoSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false

// EmailCodeAuthEnabled is deliberately independent from RegisterEnabled and
// password registration. SSO-only deployments must opt into mailbox-based
// admission instead of inheriting it merely because their IdP may provision
// new users.
var EmailCodeAuthEnabled = false
var RegisterEnabled = true

// TermsVersion labels the agreement in force. Every account records the value
// current when it was created, so a later revision never rewrites what an
// existing account actually accepted. Bump it whenever the terms change.
var TermsVersion = "2026-08-27"

var EmailDomainRestrictionEnabled = false // 是否启用邮箱域名限制
var EmailAliasRestrictionEnabled = false  // 是否启用邮箱别名限制
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}
var EmailLoginAuthServerList = []string{
	"smtp.sendcloud.net",
	"smtp.azurecomm.net",
}

var DebugEnabled bool
var MemoryCacheEnabled bool

// Deprecated: delete after 2027-01-24 (consume logs always written; see
// model/option_deprecations.go and docs/DEPLOYMENT.md, Deprecated settings).
var LogConsumeEnabled = true
var ModelLogEnabled = false
var ModelLogPath = ""
var ErrorLogPath = ""

var SMTPServer = ""
var SMTPPort = 587
var SMTPSSLEnabled = false
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var QuotaForNewUser billing.MicroUSD = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var PreConsumedQuota = 500

//var RootUserEmail = ""

var IsMasterNode bool
var NodeName = ""
var NodeRegion = ""
var NodeProvider = ""

var requestInterval int
var RequestInterval time.Duration

var SyncFrequency int // unit is second

var PricingSnapshotEnabled = true
var PricingSnapshotRefreshSeconds int
var RelayEventQueueEnabled = true
var RelayEventStream = "lazu:relay_events"
var RelayEventGroup = "lazu-relay-event-workers"
var RelayEventConsumer = ""
var RelayEventConsumerEnabled = true
var RedisVerificationCodeEnabled = false

// Deprecated: delete after 2027-01-24, with the outbox reconciler (failed
// settlements are recovered by the failure reconciler; see
// env_deprecations.go and docs/DEPLOYMENT.md, Deprecated settings).
var SettlementOutboxEnabled = false
var MetricsEnabled = false
var MetricsToken = ""
var OIDCEnabled = false
var OIDCIssuerURL = ""
var OIDCClientID = ""
var OIDCClientSecret = ""
var OIDCRedirectURL = ""
var OIDCFrontendBaseURL = ""
var OIDCPostLoginRedirectURL = "/console"
var OIDCScopes = "openid email profile"
var OIDCUsernameClaim = "preferred_username"
var OIDCEmailClaim = "email"
var OIDCRoleClaim = "groups"
var OIDCAdminRoles = ""
var OIDCAutoCreateUsers = true
var OIDCBreakGlassPasswordEnabled = true
var GoogleOAuthClientID = ""
var GoogleOAuthClientSecret = ""
var GoogleOAuthRedirectURL = ""
var GitHubOAuthClientID = ""
var GitHubOAuthClientSecret = ""
var GitHubOAuthRedirectURL = ""

var TrustedProxyCIDRs = "127.0.0.1/8,::1/128"

const DefaultRelayTimeoutSeconds = 600

var RelayTimeout int // unit is second

var GeminiSafetySetting string

var LogRetentionDays int
var LogRetentionIntervalHours int

// https://docs.cohere.com/docs/safety-modes Type; NONE/CONTEXTUAL/STRICT
var CohereSafetySetting string

const (
	RequestIdKey = "X-Lazu-Request-Id"
)

// Platform roles. There are exactly two: a normal user and an administrator.
// The administrator keeps the historical constant name RoleRootUser (value
// 100); the console calls it 管理员. Retired inherited roles (0 guest, 10
// admin) are rewritten on startup by model.migrateRetiredUserRoles.
const (
	RoleCommonUser = 1
	RoleRootUser   = 100
)

func IsValidateRole(role int) bool {
	return role == RoleCommonUser || role == RoleRootUser
}

var RateLimitKeyExpirationDuration = 20 * time.Minute

const (
	UserStatusEnabled  = 1 // don't use 0, 0 is the default value!
	UserStatusDisabled = 2 // also don't use 0
)

const (
	TokenStatusEnabled   = 1 // don't use 0, 0 is the default value!
	TokenStatusDisabled  = 2 // also don't use 0
	TokenStatusExpired   = 3
	TokenStatusExhausted = 4
)

const (
	ChannelStatusUnknown          = 0
	ChannelStatusEnabled          = 1 // don't use 0, 0 is the default value!
	ChannelStatusManuallyDisabled = 2 // also don't use 0
	ChannelStatusAutoDisabled     = 3
)

const (
	TopUpStatusPending = "pending"
	TopUpStatusSuccess = "success"
	TopUpStatusExpired = "expired"
	// TopUpStatusCanceled is a pending order the payer withdrew from the
	// wallet before paying; Stripe's checkout page is expired with it.
	TopUpStatusCanceled = "canceled"
)
