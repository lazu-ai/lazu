package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"

	"gorm.io/gorm"
)

type Log struct {
	ProjectID       int    `json:"-" gorm:"index;not null;default:0"`
	PayerUserID     int    `json:"-" gorm:"index;not null;default:0"`
	RejectionReason string `json:"reason,omitempty" gorm:"size:64;index;default:''"`

	Id                  int    `json:"-" gorm:"primaryKey;autoIncrement"`
	PublicID            string `json:"id" gorm:"type:varchar(64);index:idx_logs_partition_public,unique,priority:2"`
	RequestTS           int64  `json:"-" gorm:"primaryKey;autoIncrement:false;index:idx_logs_partition_public,unique,priority:1"`
	RoutingDecisionJSON string `json:"-" gorm:"type:text"`
	RoutingOutcomeJSON  string `json:"-" gorm:"type:text"`
	// The same facts as typed columns, which every reader filters and joins
	// on (see routing_columns.go). Written together with the JSON copies.
	RoutingDecision LogRoutingDecision `json:"-" gorm:"embedded;embeddedPrefix:rd_"`
	RoutingOutcome  LogRoutingOutcome  `json:"-" gorm:"embedded;embeddedPrefix:ro_"`
	RequestID       string             `json:"request_id" gorm:"size:64;index:idx_request_id"`
	// UserId is serialized so the console can attribute a row to an account:
	// the admin log table renders a user column and drills into
	// /api/user/{id}. Self routes only ever return the caller's own rows, so
	// echoing the caller's own id back is not a disclosure.
	UserId           int              `json:"user_id" gorm:"index:idx_logs_user_created,priority:1"`
	CreatedAt        int64            `json:"created_at" gorm:"bigint;index:idx_created_at_type;index:idx_logs_user_created,priority:2;index:idx_logs_token_created,priority:2;index:idx_logs_channel_created,priority:2"`
	Type             int              `json:"type" gorm:"index:idx_created_at_type"`
	Content          string           `json:"content"`
	Username         string           `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName        string           `json:"token_name" gorm:"index;default:''"`
	ModelName        string           `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	AmountMicrousd   billing.MicroUSD `json:"amount_microusd" gorm:"type:bigint;default:0"`
	PricingVersion   int64            `json:"pricing_version" gorm:"default:0"`
	LineItems        string           `json:"line_items" gorm:"type:text"`
	PromptTokens     int              `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int              `json:"completion_tokens" gorm:"default:0"`
	UseTime          int              `json:"use_time" gorm:"default:0"`
	IsStream         bool             `json:"is_stream"`
	// ChannelId stays unserialized: upstream channel ids are operator-only
	// routing internals. Admin listings resolve the human name into
	// ChannelName (see GetAllLogs), and the console renders that instead.
	ChannelId   int    `json:"-" gorm:"index:idx_logs_channel_created,priority:1"`
	ChannelName string `json:"channel_name" gorm:"default:''"`
	TokenId     int    `json:"-" gorm:"default:0;index:idx_logs_token_created,priority:1"`
	Group       string `json:"group" gorm:"index"`
	Ip          string `json:"ip" gorm:"index;default:''"`
	// SessionID groups every request of one task (see
	// middleware/agent_session.go for how each agent's own id is recognised);
	// SessionSource says where it came from, "token" being the per-Key
	// fallback. ThreadID / ParentThreadID place the request in the task's
	// sub-agent tree and RequestKind is main / subagent / compact / review.
	SessionID      string `json:"session_id" gorm:"size:64;index;default:''"`
	SessionSource  string `json:"session_source" gorm:"size:32;default:''"`
	ThreadID       string `json:"thread_id" gorm:"size:64;default:''"`
	ParentThreadID string `json:"parent_thread_id" gorm:"size:64;default:''"`
	RequestKind    string `json:"request_kind" gorm:"size:16;default:''"`
	// SessionFirstMessage rides from the request to the log writer only for a
	// session this process has not seen; it is stored on the session, never
	// on the log row.
	SessionFirstMessage string `json:"-" gorm:"-"`
	// AgentID is caller-controlled via X-Agent-Id / X-Lazu-Agent-Id, with a
	// best-effort fallback to the recognized client framework. It lets audit
	// views group "which agent/tool is driving this session" without parsing
	// user-agent strings at query time.
	AgentID string `json:"agent_id" gorm:"size:64;index;default:''"`
	// ClientFramework is a stable tag for who sent the request
	// (claude-code / codex / openai-python / curl / ...). See
	// internal/common/user_agent.go for the recognized values.
	ClientFramework string `json:"client_framework" gorm:"size:64;index;default:''"`
	// ClientVersion mirrors the framework's reported version string,
	// when extractable from the User-Agent.
	ClientVersion string `json:"client_version" gorm:"size:32;default:''"`
	// ClientCountry is the ISO 3166-1 alpha-2 country code from
	// Cloudflare's CF-IPCountry header. Indexed so geo dashboards and
	// per-token anti-fraud queries are cheap.
	ClientCountry string `json:"client_country" gorm:"size:2;index;default:''"`
	// RequestHost is the hostname the caller sent the request to (lowercase,
	// no port), so traffic on each API address an account was opened to can
	// be told apart. See access_address.go. Not indexed: the rare "who calls
	// this address" question is bounded by created_at, and building an index
	// on the live table at startup would hold up writes.
	RequestHost string `json:"request_host" gorm:"size:128;default:''"`
	Other       string `json:"other"`
	// The gateway's own time before and after the upstream call, copied out
	// of Other by a PostgreSQL trigger so the daily timing check aggregates
	// columns instead of decoding every document (gateway_timing_columns.go).
	GatewayPreMs  *float64 `json:"-" gorm:"type:float8"`
	GatewayPostMs *float64 `json:"-" gorm:"type:float8"`
	// TRUE when the gateway lowered the request's reasoning effort to a cap,
	// copied out of Other by a trigger (effort_clamped_column.go).
	EffortClamped *bool  `json:"-" gorm:"type:boolean"`
	UsageFact     string `json:"usage_fact,omitempty" gorm:"type:text"`
	// Upstream cost and margin in microUSD, copied out of the usage fact so
	// they can be summed in SQL. NULL when the source's cost is unknown.
	UpstreamCostMicrousd *int64 `json:"-" gorm:"column:upstream_cost_microusd"`
	GrossMarginMicrousd  *int64 `json:"-" gorm:"column:gross_margin_microusd"`
}

type relayConsumeLogEvent struct {
	RejectionReason  string           `json:"rejection_reason,omitempty"`
	ProjectID        int              `json:"project_id"`
	PayerUserID      int              `json:"payer_user_id"`
	PublicID         string           `json:"public_id"`
	RequestTS        int64            `json:"request_ts"`
	ChannelName      string           `json:"channel_name"`
	RequestID        string           `json:"request_id"`
	UserID           int              `json:"user_id"`
	CreatedAt        int64            `json:"created_at"`
	Type             int              `json:"type"`
	Content          string           `json:"content"`
	Username         string           `json:"username"`
	TokenName        string           `json:"token_name"`
	ModelName        string           `json:"model_name"`
	AmountMicrousd   billing.MicroUSD `json:"amount_microusd"`
	PricingVersion   int64            `json:"pricing_version"`
	LineItems        string           `json:"line_items"`
	PromptTokens     int              `json:"prompt_tokens"`
	CompletionTokens int              `json:"completion_tokens"`
	UseTime          int              `json:"use_time"`
	IsStream         bool             `json:"is_stream"`
	ChannelID        int              `json:"channel_id"`
	TokenID          int              `json:"token_id"`
	Group            string           `json:"group"`
	IP               string           `json:"ip"`
	SessionID        string           `json:"session_id"`
	SessionSource    string           `json:"session_source,omitempty"`
	ThreadID         string           `json:"thread_id,omitempty"`
	ParentThreadID   string           `json:"parent_thread_id,omitempty"`
	RequestKind      string           `json:"request_kind,omitempty"`
	FirstMessage     string           `json:"first_message,omitempty"`
	UpstreamCost     *int64           `json:"upstream_cost_microusd,omitempty"`
	GrossMargin      *int64           `json:"gross_margin_microusd,omitempty"`
	AgentID          string           `json:"agent_id"`
	ClientFramework  string           `json:"client_framework"`
	ClientVersion    string           `json:"client_version"`
	ClientCountry    string           `json:"client_country"`
	RequestHost      string           `json:"request_host,omitempty"`
	Other            string           `json:"other"`
	UsageFact        string           `json:"usage_fact"`
}

type relayResultEvent struct {
	RoutingDecision *RoutingDecision        `json:"routing_decision,omitempty"`
	RoutingAttempts []RoutingAttemptOutcome `json:"routing_attempts,omitempty"`
	ConsumeLog      *relayConsumeLogEvent   `json:"consume_log,omitempty"`
	RoutingOutcome  *RoutingOutcome         `json:"routing_outcome,omitempty"`
}

const (
	relayResultConsumeLogContextKey    = "relay_result_consume_log"
	relayResultFlushExpectedContextKey = "relay_result_flush_expected"
)

func newRelayConsumeLogEvent(row *Log) relayConsumeLogEvent {
	if row == nil {
		return relayConsumeLogEvent{}
	}
	ensureConsumeLogPublicID(row)
	if row.PublicID == "" {
		row.PublicID = common.GeneratePublicID(common.PublicLogIDPrefix)
	}
	if row.RequestTS == 0 {
		row.RequestTS = requestTimestamp(row.RequestID, row.CreatedAt)
	}
	return relayConsumeLogEvent{
		RejectionReason: row.RejectionReason,
		ProjectID:       row.ProjectID, PayerUserID: row.PayerUserID,
		PublicID:  row.PublicID,
		RequestTS: row.RequestTS, ChannelName: row.ChannelName,
		RequestID:        row.RequestID,
		UserID:           row.UserId,
		CreatedAt:        row.CreatedAt,
		Type:             row.Type,
		Content:          row.Content,
		Username:         row.Username,
		TokenName:        row.TokenName,
		ModelName:        row.ModelName,
		AmountMicrousd:   row.AmountMicrousd,
		PricingVersion:   row.PricingVersion,
		LineItems:        row.LineItems,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		UseTime:          row.UseTime,
		IsStream:         row.IsStream,
		ChannelID:        row.ChannelId,
		TokenID:          row.TokenId,
		Group:            row.Group,
		IP:               row.Ip,
		SessionID:        row.SessionID,
		SessionSource:    row.SessionSource,
		ThreadID:         row.ThreadID,
		ParentThreadID:   row.ParentThreadID,
		RequestKind:      row.RequestKind,
		FirstMessage:     row.SessionFirstMessage,
		UpstreamCost:     row.UpstreamCostMicrousd,
		GrossMargin:      row.GrossMarginMicrousd,
		AgentID:          row.AgentID,
		ClientFramework:  row.ClientFramework,
		ClientVersion:    row.ClientVersion,
		ClientCountry:    row.ClientCountry,
		RequestHost:      row.RequestHost,
		Other:            row.Other,
		UsageFact:        row.UsageFact,
	}
}

func (event relayConsumeLogEvent) toLog() Log {
	return Log{
		RejectionReason: event.RejectionReason,
		ProjectID:       event.ProjectID, PayerUserID: event.PayerUserID,
		PublicID:  event.PublicID,
		RequestTS: event.RequestTS, ChannelName: event.ChannelName,
		RequestID:            event.RequestID,
		UserId:               event.UserID,
		CreatedAt:            event.CreatedAt,
		Type:                 event.Type,
		Content:              event.Content,
		Username:             event.Username,
		TokenName:            event.TokenName,
		ModelName:            event.ModelName,
		AmountMicrousd:       event.AmountMicrousd,
		PricingVersion:       event.PricingVersion,
		LineItems:            event.LineItems,
		PromptTokens:         event.PromptTokens,
		CompletionTokens:     event.CompletionTokens,
		UseTime:              event.UseTime,
		IsStream:             event.IsStream,
		ChannelId:            event.ChannelID,
		TokenId:              event.TokenID,
		Group:                event.Group,
		Ip:                   event.IP,
		SessionID:            event.SessionID,
		SessionSource:        event.SessionSource,
		ThreadID:             event.ThreadID,
		ParentThreadID:       event.ParentThreadID,
		RequestKind:          event.RequestKind,
		SessionFirstMessage:  event.FirstMessage,
		UpstreamCostMicrousd: event.UpstreamCost,
		GrossMarginMicrousd:  event.GrossMargin,
		AgentID:              event.AgentID,
		ClientFramework:      event.ClientFramework,
		ClientVersion:        event.ClientVersion,
		ClientCountry:        event.ClientCountry,
		RequestHost:          event.RequestHost,
		Other:                event.Other,
		UsageFact:            event.UsageFact,
	}
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
)

// HasPublicAPIRequest reports whether the user has ever sent a consume request
// with a normal API token. An extension's hidden internal credential deliberately
// increments account usage for billing, but it cannot authenticate the curl
// command shown by onboarding and therefore must not complete this step.
func HasPublicAPIRequest(userID int) (bool, error) {
	if userID <= 0 {
		return false, errors.New("user id is required")
	}
	if CoreDB() == nil || TelemetryDB() == nil {
		return false, errors.New("database is not initialized")
	}

	var internalTokenIDs []int
	hiddenCondition, hiddenArgs := hiddenTokenCondition()
	if err := CoreDB().Unscoped().Model(&Token{}).
		Where("user_id = ?", userID).
		Where(hiddenCondition, hiddenArgs...).
		Pluck("id", &internalTokenIDs).Error; err != nil {
		return false, err
	}

	query := TelemetryDB().Model(&Log{}).
		Where("user_id = ? AND type = ? AND token_id > 0", userID, LogTypeConsume)
	if len(internalTokenIDs) > 0 {
		query = query.Where("token_id NOT IN ?", internalTokenIDs)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetTokenLastUsedAt returns when each Key last settled a charge. It reads
// tokens.last_used_at, which is stamped on every charge and, unlike the
// consume log, survives log retention.
func GetTokenLastUsedAt(tokenIDs []int) (map[int]int64, error) {
	result := make(map[int]int64)
	if len(tokenIDs) == 0 || CoreDB() == nil {
		return result, nil
	}
	var rows []Token
	if err := CoreDB().Model(&Token{}).Select("id", "last_used_at").
		Where("id IN ? AND last_used_at > 0", tokenIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, item := range rows {
		result[item.Id] = item.LastUsedAt
	}
	return result, nil
}

func formatUserLogs(logs []*Log) {
	for i := range logs {
		redactCustomerLog(logs[i])
		logs[i].Id = logs[i].Id % 1024
	}
}

// accountLog is a log about an account rather than a request: a top-up, an
// administrator's change, a key being viewed. It carries no project; the
// writer gives it the account's own (attributeEventsToOwnedProjects).
func accountLog(userId int, logType int, content string) *Log {
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	return log
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	log := accountLog(userId, logType, content)
	err := publishConsumeLogEvent(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	log := accountLog(userId, logType, content)
	if len(adminInfo) > 0 {
		log.Other = common.MapToJsonStr(map[string]interface{}{
			"admin_info": adminInfo,
		})
	}
	err := publishConsumeLogEvent(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"node_provider":           common.NodeProvider,
		"node_region":             common.NodeRegion,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	log := accountLog(userId, LogTypeTopup, content)
	log.Ip = callerIp
	log.Other = common.MapToJsonStr(map[string]interface{}{
		"admin_info": adminInfo,
	})
	err := publishConsumeLogEvent(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	projectID := common.GetContextKeyInt(c, constant.ContextKeyProjectID)
	if projectID > 0 && c.GetBool("project_rejection_logged") {
		return
	}
	reason := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
	if reason == "" && other != nil {
		reason, _ = other["error_code"].(string)
	}

	if other == nil {
		other = make(map[string]interface{})
	}
	stampRequestFacts(c, other)
	other["usage_unavailable"] = true
	if start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime); !start.IsZero() {
		duration := time.Since(start).Milliseconds()
		other["duration_ms"] = duration
		useTimeSeconds = int(duration / 1000)
	}
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, content))
	// The token-auth chain only publishes "username" once the user cache has
	// been written; a request rejected before that — notably one made with the
	// Key of a member who was removed from the project — would leave the
	// console's actor column blank. Resolve it once, from the authenticated
	// user id, and only when it would otherwise be empty.
	username := c.GetString("username")
	if username == "" && userId > 0 && CoreDB() != nil {
		if resolved, err := GetUsernameById(userId, false); err == nil {
			username = resolved
		}
	}
	otherStr := common.MapToJsonStr(other)
	log := &Log{
		ProjectID:        projectID,
		PayerUserID:      common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
		RejectionReason:  reason,
		UserId:           userId,
		RequestID:        c.GetString(common.RequestIdKey),
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		// Every request records the caller's real address, resolved through
		// the trusted proxy chain; c.ClientIP() alone is the proxy peer.
		Ip:                  common.GetRealClientIP(c),
		SessionID:           common.GetContextKeyString(c, constant.ContextKeySessionID),
		SessionSource:       common.GetContextKeyString(c, constant.ContextKeySessionSource),
		ThreadID:            common.GetContextKeyString(c, constant.ContextKeyThreadID),
		ParentThreadID:      common.GetContextKeyString(c, constant.ContextKeyParentThreadID),
		RequestKind:         common.GetContextKeyString(c, constant.ContextKeyRequestKind),
		SessionFirstMessage: common.GetContextKeyString(c, constant.ContextKeySessionFirstMessage),
		AgentID:             common.GetContextKeyString(c, constant.ContextKeyAgentID),
		ClientFramework:     common.GetContextKeyString(c, constant.ContextKeyClientFramework),
		ClientVersion:       common.GetContextKeyString(c, constant.ContextKeyClientVersion),
		ClientCountry:       common.GetContextKeyString(c, constant.ContextKeyClientCountry),
		RequestHost:         RequestHostOf(c),
		Other:               otherStr,
	}
	if queueConsumeLogForRelayResult(c, log) {
		return
	}
	if err := publishConsumeLogEvent(log); err != nil {
		logger.LogError(c, "failed to record error log: "+err.Error())
		return
	}
	MarkRequestLogTarget(c, log.RequestID)

	if projectID > 0 {
		c.Set("project_rejection_logged", true)
	}
}

type RecordConsumeLogParams struct {
	ProjectID        int                    `json:"project_id"`
	PayerUserID      int                    `json:"payer_user_id"`
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	AmountMicrousd   billing.MicroUSD       `json:"amount_microusd"`
	PricingVersion   int64                  `json:"pricing_version"`
	LineItems        string                 `json:"line_items"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
	UsageFact        usagepkg.Fact          `json:"usage_fact"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	// Legacy relay adapters still calling this helper inherit only the trusted
	// authentication context. Never derive a shared log's project from actor ID.
	if params.ProjectID == 0 && c != nil {
		if projectID := common.GetContextKeyInt(c, constant.ContextKeyProjectID); projectID > 0 {
			params.ProjectID = projectID
			params.PayerUserID = common.GetContextKeyInt(c, constant.ContextKeyPayerUserID)
			userId = c.GetInt("id")
		}
	}
	if !common.LogConsumeEnabled {
		return
	}
	if common.ShouldLog(common.LogLevelInfo) {
		logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d model=%s amount_microusd=%d", userId, params.ModelName, params.AmountMicrousd))
	}
	username := c.GetString("username")
	if params.Other == nil {
		params.Other = map[string]interface{}{}
	}
	stampRequestFacts(c, params.Other)
	// Success rows carry the same millisecond duration error rows do;
	// use_time alone is whole seconds measured from a different clock.
	if start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime); !start.IsZero() {
		if _, ok := params.Other["duration_ms"]; !ok {
			params.Other["duration_ms"] = time.Since(start).Milliseconds()
		}
	}
	if _, ok := params.Other["status_code"]; !ok && c.Writer != nil && c.Writer.Written() {
		params.Other["status_code"] = c.Writer.Status()
	}
	otherStr := common.MapToJsonStr(params.Other)
	usageFact := params.UsageFact.Normalize()
	if usageFact.IsZero() {
		usageFact = usagepkg.FactFromLegacyLogFields(
			params.PromptTokens,
			params.CompletionTokens,
			params.Other,
		)
	}
	usageFact = enrichRequestUsageFact(c, userId, params, usageFact)
	params.UsageFact = usageFact
	log := &Log{
		ProjectID: params.ProjectID, PayerUserID: params.PayerUserID,
		UserId:           userId,
		RequestID:        c.GetString(common.RequestIdKey),
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		AmountMicrousd:   params.AmountMicrousd,
		PricingVersion:   params.PricingVersion,
		LineItems:        params.LineItems,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		// Every request records the caller's real address from the trusted
		// proxy chain.
		Ip: common.GetRealClientIP(c),
		// Attribution fields — populated by the distributor middleware.
		// session_id always recorded (not PII), client framework/version
		// recorded as best-effort UA parse, country from CF-IPCountry
		// when behind Cloudflare.
		SessionID:           common.GetContextKeyString(c, constant.ContextKeySessionID),
		SessionSource:       common.GetContextKeyString(c, constant.ContextKeySessionSource),
		ThreadID:            common.GetContextKeyString(c, constant.ContextKeyThreadID),
		ParentThreadID:      common.GetContextKeyString(c, constant.ContextKeyParentThreadID),
		RequestKind:         common.GetContextKeyString(c, constant.ContextKeyRequestKind),
		SessionFirstMessage: common.GetContextKeyString(c, constant.ContextKeySessionFirstMessage),
		AgentID:             common.GetContextKeyString(c, constant.ContextKeyAgentID),
		ClientFramework:     common.GetContextKeyString(c, constant.ContextKeyClientFramework),
		ClientVersion:       common.GetContextKeyString(c, constant.ContextKeyClientVersion),
		ClientCountry:       common.GetContextKeyString(c, constant.ContextKeyClientCountry),
		RequestHost:         RequestHostOf(c),
		Other:               otherStr,
		UsageFact:           usageFact.JSONString(),
	}
	if usageFact.ProviderCostUSD != nil {
		cost := int64(math.Round(*usageFact.ProviderCostUSD * float64(billing.MicroUSDPerUSD)))
		log.UpstreamCostMicrousd = &cost
		// Margin is derived from the two stored integers so charge − cost
		// always equals margin to the microUSD; converting the float margin
		// on its own drifted by one on rounding boundaries.
		margin := params.AmountMicrousd.Int64() - cost
		log.GrossMarginMicrousd = &margin
	}
	attachRoutingOutcomeUsageSnapshot(c, params)
	if queueConsumeLogForRelayResult(c, log) {
		return
	}
	if err := publishConsumeLogEvent(log); err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
		return
	}
	MarkRequestLogTarget(c, log.RequestID)
}

func enrichRequestUsageFact(c *gin.Context, userId int, params RecordConsumeLogParams, fact usagepkg.Fact) usagepkg.Fact {
	if c != nil {
		fact.RequestID = c.GetString(common.RequestIdKey)
		fact.SessionID = common.GetContextKeyString(c, constant.ContextKeySessionID)
		fact.AgentID = common.GetContextKeyString(c, constant.ContextKeyAgentID)
		fact.Source = resolveRequestFactSource(c)
	}
	fact.UserID = userId
	fact.TokenID = params.TokenId
	fact.ChannelID = params.ChannelId
	fact.ModelName = params.ModelName

	customerCostUSD := float64(params.AmountMicrousd.Int64()) / float64(billing.MicroUSDPerUSD)
	fact.CustomerCostUSD = float64Ptr(customerCostUSD)
	if upstreamCostUSD, ok := resolveUpstreamCostUSDAt(params, fact, common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)); ok {
		fact.ProviderCostUSD = float64Ptr(upstreamCostUSD)
		fact.GrossMarginUSD = float64Ptr(customerCostUSD - upstreamCostUSD)
	}
	return fact.Normalize()
}

func resolveRequestFactSource(c *gin.Context) string {
	source := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyClientFramework))
	if source != "" {
		return source
	}
	source = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyRequestAuthKeySource))
	if source != "" {
		return source
	}
	return "relay"
}

// BuildConsumeLogRelayEnvelope captures the immutable telemetry facts before a
// financial settlement. The same envelope is written by the settlement Lua or
// its durable spool replay, without a separate API-side telemetry write.
func BuildConsumeLogRelayEnvelope(log *Log) (string, error) {
	if log == nil {
		return "", nil
	}
	ensureConsumeLogPublicID(log)
	event := newRelayConsumeLogEvent(log)
	raw, err := json.Marshal(relayResultEvent{ConsumeLog: &event})
	return string(raw), err
}

func publishConsumeLogEvent(log *Log) error {
	ensureConsumeLogPublicID(log)
	if relayEventWriterRequired() {
		return PublishRelayEvent(RelayEventTypeConsumeLog, newRelayConsumeLogEvent(log))
	}
	if relayEventQueueAvailable() {
		if err := PublishRelayEvent(RelayEventTypeConsumeLog, newRelayConsumeLogEvent(log)); err == nil {
			return nil
		} else {
			common.SysLog("failed to publish consume log event, falling back to sync write: " + err.Error())
		}
	}
	return writeConsumeLogFallbackSync(log)
}

func writeConsumeLogFallbackSync(log *Log) error {
	if log == nil {
		return nil
	}
	event := newRelayConsumeLogEvent(log)
	return persistRelayResultBatch([]relayResultEvent{{ConsumeLog: &event}})
}

func queueConsumeLogForRelayResult(c *gin.Context, log *Log) bool {
	if c == nil || log == nil {
		return false
	}
	if expected, ok := c.Get(relayResultFlushExpectedContextKey); !ok || expected != true {
		return false
	}
	ensureConsumeLogPublicID(log)
	event := newRelayConsumeLogEvent(log)
	c.Set(relayResultConsumeLogContextKey, event)
	MarkRequestLogTarget(c, log.RequestID)
	return true
}

func MarkRelayResultFlushExpected(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(relayResultFlushExpectedContextKey, true)
}

func FlushRelayResultEvent(c *gin.Context, outcome *RoutingOutcome) bool {
	if c == nil {
		return false
	}
	c.Set(relayResultFlushExpectedContextKey, false)
	event := relayResultEvent{}
	if value, ok := c.Get(relayResultDecisionContextKey); ok {
		event.RoutingDecision, _ = value.(*RoutingDecision)
		c.Set(relayResultDecisionContextKey, nil)
	}
	if value, ok := c.Get(relayResultAttemptsContextKey); ok {
		event.RoutingAttempts, _ = value.([]RoutingAttemptOutcome)
		c.Set(relayResultAttemptsContextKey, nil)
	}
	if value, ok := c.Get(relayResultConsumeLogContextKey); ok {
		c.Set(relayResultConsumeLogContextKey, nil)
		switch typed := value.(type) {
		case relayConsumeLogEvent:
			event.ConsumeLog = &typed
		case *relayConsumeLogEvent:
			event.ConsumeLog = typed
		}
	}
	if outcome != nil {
		event.RoutingOutcome = outcome
	}
	if published, _ := c.Get(relayResultPublishedContextKey); published == true {
		return true
	}
	if event.ConsumeLog == nil && event.RoutingOutcome == nil {
		return false
	}
	if relayEventWriterRequired() {
		if err := PublishRelayEvent(RelayEventTypeRelayResult, event); err != nil {
			common.SysLog("failed to publish relay result telemetry: " + err.Error())
		} else {
			markRelayResultLogTarget(c, event)
		}
		return true
	}
	if relayEventQueueAvailable() {
		if err := PublishRelayEvent(RelayEventTypeRelayResult, event); err == nil {
			markRelayResultLogTarget(c, event)
			return true
		} else {
			common.SysLog("failed to publish relay result event, falling back to sync write: " + err.Error())
		}
	}
	if err := handleRelayResultEvent(event); err != nil {
		common.SysLog("failed to sync relay result fallback: " + err.Error())
	} else {
		markRelayResultLogTarget(c, event)
	}
	return true
}

func handleRelayResultEvent(event relayResultEvent) error {
	return persistRelayResultBatch([]relayResultEvent{event})
}

func handleRelayResultEventBatch(events []relayResultEvent) error {
	return persistRelayResultBatch(events)
}

func createConsumeLogsBatch(logs []Log) error {
	events := make([]relayResultEvent, 0, len(logs))
	for i := range logs {
		ensureConsumeLogPublicID(&logs[i])
		event := newRelayConsumeLogEvent(&logs[i])
		events = append(events, relayResultEvent{ConsumeLog: &event})
	}
	return persistRelayResultBatch(events)
}

func ensureConsumeLogPublicID(log *Log) {
	if log == nil || log.PublicID != "" || log.RequestID == "" {
		return
	}
	log.PublicID = fmt.Sprintf("%s_%s", common.PublicLogIDPrefix, log.RequestID)
	if log.Type != LogTypeUnknown && log.Type != LogTypeConsume && log.Type != LogTypeError {
		log.PublicID += fmt.Sprintf("_%d", log.Type)
	}
}

func consumeLogSideEffects(log *Log, async bool) {
	if log == nil {
		return
	}
	if log.Type == LogTypeConsume && log.RequestID != "" {
		if err := ReleaseSpendBudgetReservation(SpendBudgetChargeInput{
			ProjectID: log.ProjectID, PayerUserID: log.PayerUserID, ActorUserID: log.UserId,
			UserID:    log.UserId,
			TokenID:   log.TokenId,
			RequestID: log.RequestID,
		}); err != nil {
			common.SysLog("failed to release spend budget reservation: " + err.Error())
		}
		StampUserRequestActivity(log.UserId, log.CreatedAt)
		RecordSessionActivity(log, false)
	}
	if common.DataExportEnabled {
		fn := func() {
			createdAt := log.CreatedAt
			if createdAt == 0 {
				createdAt = common.GetTimestamp()
			}
			LogUsageData(log.UserId, log.Username, log.ModelName, log.AmountMicrousd, createdAt, log.PromptTokens+log.CompletionTokens)
		}
		if async {
			common.SafeGo("usage-data-log", fn)
		} else {
			fn()
		}
	}
	if log.ModelName != "" {
		modelName := log.ModelName
		fn := func() {
			if err := IncrementModelRequests(modelName); err != nil {
				common.SysLog("failed to increment model requests: " + err.Error())
			}
		}
		if async {
			common.SafeGo("model-request-counter", fn)
		} else {
			fn()
		}
	}
}

func attachRoutingOutcomeUsageSnapshot(c *gin.Context, params RecordConsumeLogParams) {
	if c == nil {
		return
	}
	usageFact := params.UsageFact.Normalize()
	if usageFact.IsZero() {
		usageFact = usagepkg.FactFromLegacyLogFields(
			params.PromptTokens,
			params.CompletionTokens,
			params.Other,
		)
	}
	snapshot := RoutingOutcomeUsageSnapshot{
		ActualInputTokens: usageFact.PromptTokens,
		CachedInputTokens: dimensionQuantity(usageFact, usagepkg.DimensionCacheRead),
		CacheWriteTokens: dimensionQuantity(usageFact, usagepkg.DimensionCacheWrite) +
			dimensionQuantity(usageFact, usagepkg.DimensionCacheWrite5m) +
			dimensionQuantity(usageFact, usagepkg.DimensionCacheWrite1h),
		CacheWrite5mTokens: dimensionQuantity(usageFact, usagepkg.DimensionCacheWrite5m),
		CacheWrite1hTokens: dimensionQuantity(usageFact, usagepkg.DimensionCacheWrite1h),
		CacheMissTokens:    dimensionQuantity(usageFact, usagepkg.DimensionCacheMiss),
		ActualOutputTokens: usageFact.CompletionTokens,
		AudioInputTokens:   dimensionQuantity(usageFact, usagepkg.DimensionAudioInput),
		AudioOutputTokens:  dimensionQuantity(usageFact, usagepkg.DimensionAudioOutput),
		ImageInputTokens:   dimensionQuantity(usageFact, usagepkg.DimensionImageInput),
		ImageOutputTokens:  dimensionQuantity(usageFact, usagepkg.DimensionImageOutput),
		ChargedCostUSD:     float64(params.AmountMicrousd.Int64()) / float64(billing.MicroUSDPerUSD),
		HasChargedCost:     true,
	}
	if usageFact.ProviderCostUSD != nil {
		snapshot.UpstreamCostUSD = *usageFact.ProviderCostUSD
		snapshot.HasUpstreamCost = true
		if usageFact.GrossMarginUSD != nil {
			snapshot.GrossMarginUSD = *usageFact.GrossMarginUSD
		} else {
			snapshot.GrossMarginUSD = snapshot.ChargedCostUSD - *usageFact.ProviderCostUSD
		}
		snapshot.HasGrossMargin = true
	} else if upstreamCostUSD, ok := resolveUpstreamCostUSDAt(params, usageFact, common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)); ok {
		snapshot.UpstreamCostUSD = upstreamCostUSD
		snapshot.HasUpstreamCost = true
		snapshot.GrossMarginUSD = snapshot.ChargedCostUSD - upstreamCostUSD
		snapshot.HasGrossMargin = true
	}
	common.SetContextKey(c, constant.ContextKeyRoutingOutcomeUsage, snapshot)
}

func resolveUpstreamCostUSD(params RecordConsumeLogParams, usageFact usagepkg.Fact) (float64, bool) {
	return resolveUpstreamCostUSDAt(params, usageFact, time.Now())
}

func resolveUpstreamCostUSDAt(params RecordConsumeLogParams, usageFact usagepkg.Fact, at time.Time) (float64, bool) {
	if at.IsZero() {
		at = time.Now()
	}
	if providerCostUSD, ok := floatFromAny(params.Other["provider_cost_usd"]); ok && providerCostUSD >= 0 {
		return providerCostUSD, true
	}
	price, ok := GetActiveProviderCostPrice(params.ModelName, params.ChannelId)
	if !ok {
		return 0, false
	}
	usage := usageFactToBillingUsage(usageFact)
	price, _ = price.ForInputTokens(usage.InputTokenTotal())
	quote := billing.Quote(usage, price.ToBillingPriceSheetAt(at), nil)
	if len(quote.LineItems) == 0 && quote.TotalMicrousd == 0 {
		return 0, false
	}
	return float64(quote.TotalMicrousd.Int64()) / float64(billing.MicroUSDPerUSD), true
}

func usageFactToBillingUsage(fact usagepkg.Fact) billing.Usage {
	fact = fact.Normalize()
	if fact.ImageUsage != nil {
		return billing.Usage{Images: fact.ImageUsage}
	}
	usage := billing.Usage{}
	for _, dimension := range fact.Dimensions {
		if dimension.Quantity <= 0 {
			continue
		}
		switch dimension.Name {
		case usagepkg.DimensionInput:
			usage.InputTokens += dimension.Quantity
		case usagepkg.DimensionOutput:
			usage.OutputTokens += dimension.Quantity
		case usagepkg.DimensionCacheRead:
			usage.CacheReadTokens += dimension.Quantity
		case usagepkg.DimensionCacheWrite, usagepkg.DimensionCacheWrite5m:
			usage.CacheWrite5mTokens += dimension.Quantity
		case usagepkg.DimensionCacheWrite1h:
			usage.CacheWrite1hTokens += dimension.Quantity
		case usagepkg.DimensionAudioInput:
			usage.AudioInputTokens += dimension.Quantity
		case usagepkg.DimensionAudioOutput:
			usage.AudioOutputTokens += dimension.Quantity
		case usagepkg.DimensionImageInput:
			usage.ImageInputTokens += dimension.Quantity
		case usagepkg.DimensionImageOutput:
			usage.ImageOutputTokens += dimension.Quantity
		case usagepkg.DimensionRequest:
			usage.RequestCount += dimension.Quantity
		case usagepkg.DimensionSecond:
			usage.Seconds += dimension.Quantity
		}
	}
	if usage.InputTokens == 0 && fact.InputTokens > 0 {
		usage.InputTokens = int64(fact.InputTokens)
	}
	if usage.OutputTokens == 0 && fact.OutputTokens > 0 {
		usage.OutputTokens = int64(fact.OutputTokens)
	}
	if usage.InputTokens == 0 && len(fact.Dimensions) == 0 && fact.PromptTokens > 0 {
		usage.InputTokens = int64(fact.PromptTokens)
	}
	if usage.OutputTokens == 0 && len(fact.Dimensions) == 0 && fact.CompletionTokens > 0 {
		usage.OutputTokens = int64(fact.CompletionTokens)
	}
	return usage
}

func floatFromAny(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func dimensionQuantity(fact usagepkg.Fact, name string) int {
	for _, dimension := range fact.Dimensions {
		if dimension.Name == name {
			return int(dimension.Quantity)
		}
	}
	return 0
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, userID int, tokenName string, startIdx int, num int, channel int, group string) (logs []*Log, total int64, err error) {
	tx := allLogsQuery(logType, startTimestamp, endTimestamp, modelName, username, userID, tokenName, channel, group)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = orderLogsNewestFirst(tx).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	return logs, total, resolveLogChannelNames(logs)
}

// allLogsQuery is the administrator's log list before its order and page.
func allLogsQuery(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, userID int, tokenName string, channel int, group string) *gorm.DB {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = TelemetryDB().Where("logs.type <> ?", LogTypeUnknown)
	} else {
		tx = TelemetryDB().Where("logs.type = ?", logType)
	}

	if modelName != "" {
		tx = tx.Where("logs.model_name like ?", modelName)
	}
	if userID > 0 {
		tx = tx.Where("logs.user_id = ?", userID)
	} else if username != "" {
		tx = tx.Where("logs.username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	return tx.Model(&Log{})
}

// resolveLogChannelNames fills each admin log row's channel name.
func resolveLogChannelNames(logs []*Log) error {
	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if err := CoreDB().Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
			return err
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return nil
}

// orderLogsNewestFirst is the order of every log list: created_at, then id,
// newest first. Each filter a list narrows by has a (column, created_at)
// index (logListIndexes), so on the partitioned table every partition yields
// rows already in this order and LIMIT stops after one page. Ordered by id
// alone, a page of a filter that matches few rows walked the id index and
// threw away every other row (schema_v37.go).
func orderLogsNewestFirst(tx *gorm.DB) *gorm.DB {
	return tx.Order("logs.created_at DESC").Order("logs.id DESC")
}

// logSearchWindow bounds a log search by content or type, which no index
// serves; a request id is found anywhere in the retained logs.
const logSearchWindow = 24 * time.Hour

// SearchAllLogs finds the request with this id, and the last day's logs whose
// content starts with the keyword or whose type it names.
func SearchAllLogs(keyword string) ([]*Log, error) {
	base := func() *gorm.DB { return TelemetryDB().Model(&Log{}).Where("logs.type <> ?", LogTypeUnknown) }
	return searchLogs(base, keyword, time.Now().Add(-logSearchWindow).Unix(), true)
}

// searchLogs runs a search as two indexed reads: the exact request id, then
// the content prefix (and, when asked, the log type) among logs since since,
// or within the window base already holds when since is 0.
func searchLogs(base func() *gorm.DB, keyword string, since int64, matchType bool) ([]*Log, error) {
	var byID, recent []*Log
	// Account logs have no request id; an empty keyword is not one.
	if keyword != "" {
		if err := orderLogsNewestFirst(base().Where("logs.request_id = ?", keyword)).Limit(common.MaxRecentItems).Find(&byID).Error; err != nil {
			return nil, err
		}
	}
	query := base()
	if since > 0 {
		query = query.Where("logs.created_at >= ?", since)
	}
	if logType, err := strconv.Atoi(keyword); err == nil && matchType {
		query = query.Where("(logs.type = ? OR logs.content LIKE ?)", logType, keyword+"%")
	} else {
		query = query.Where("logs.content LIKE ?", keyword+"%")
	}
	if err := orderLogsNewestFirst(query).Limit(common.MaxRecentItems).Find(&recent).Error; err != nil {
		return nil, err
	}
	type key struct {
		requestTS int64
		publicID  string
		id        int
	}
	seen := make(map[key]bool, len(byID)+len(recent))
	logs := make([]*Log, 0, len(byID)+len(recent))
	for _, log := range append(byID, recent...) {
		k := key{log.RequestTS, log.PublicID, log.Id}
		if !seen[k] {
			seen[k] = true
			logs = append(logs, log)
		}
	}
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].CreatedAt != logs[j].CreatedAt {
			return logs[i].CreatedAt > logs[j].CreatedAt
		}
		return logs[i].Id > logs[j].Id
	})
	if len(logs) > common.MaxRecentItems {
		logs = logs[:common.MaxRecentItems]
	}
	return logs, nil
}

type Stat struct {
	AmountMicrousd billing.MicroUSD `json:"amount_microusd"`
	Rpm            int              `json:"rpm"`
	Tpm            int              `json:"tpm"`
}

type TokenConsumeSummary struct {
	RequestCount     int64            `json:"request_count"`
	AmountMicrousd   billing.MicroUSD `json:"amount_microusd"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	AvgUseTime       float64          `json:"avg_use_time"`
}

func (s TokenConsumeSummary) TotalTokens() int64 {
	return s.PromptTokens + s.CompletionTokens
}

type TokenUsageSeriesPoint struct {
	Bucket           int64            `json:"bucket"`
	RequestCount     int64            `json:"request_count"`
	AmountMicrousd   billing.MicroUSD `json:"amount_microusd"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
}

func (p TokenUsageSeriesPoint) TotalTokens() int64 {
	return p.PromptTokens + p.CompletionTokens
}

type TokenModelUsage struct {
	ModelName        string           `json:"model_name"`
	RequestCount     int64            `json:"request_count"`
	AmountMicrousd   billing.MicroUSD `json:"amount_microusd"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	AvgUseTime       float64          `json:"avg_use_time"`
	LastUsedAt       int64            `json:"last_used_at"`
}

type TokenConsumeLogFilter struct {
	ModelName string
	Keyword   string
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, userID int, tokenName string, channel int, group string, tokenIDs ...int) (stat Stat) {
	tx := TelemetryDB().Table("logs").Select("COALESCE(SUM(amount_microusd), 0) amount_microusd")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := TelemetryDB().Table("logs").Select("count(*) rpm, sum(prompt_tokens) + sum(completion_tokens) tpm")
	if len(tokenIDs) > 0 && tokenIDs[0] > 0 {
		tx = tx.Where("token_id = ?", tokenIDs[0])
		rpmTpmQuery = rpmTpmQuery.Where("token_id = ?", tokenIDs[0])
	}

	if userID > 0 {
		tx = tx.Where("user_id = ?", userID)
		rpmTpmQuery = rpmTpmQuery.Where("user_id = ?", userID)
	} else if username != "" {
		tx = tx.Where("username = ?", username)
		rpmTpmQuery = rpmTpmQuery.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
		rpmTpmQuery = rpmTpmQuery.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name like ?", modelName)
		rpmTpmQuery = rpmTpmQuery.Where("model_name like ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	tx.Scan(&stat)
	rpmTpmQuery.Scan(&stat)

	return stat
}

// GetUserUsedModels returns a list of distinct model names that a user has used
func GetUserUsedModels(userId int) ([]string, error) {
	var models []string
	err := TelemetryDB().Model(&Log{}).
		Where("user_id = ? AND type = ? AND model_name != ''", userId, LogTypeConsume).
		Distinct("model_name").
		Pluck("model_name", &models).Error
	return models, err
}

func GetConsumeLogByRequestIDForToken(userID int, tokenID int, requestID string, principals ...ProjectPrincipal) (*Log, error) {
	return GetConsumeLogByRequestIDForTokenContext(context.Background(), userID, tokenID, requestID, principals...)
}

func GetConsumeLogByRequestIDForTokenContext(ctx context.Context, userID int, tokenID int, requestID string, principals ...ProjectPrincipal) (*Log, error) {
	// A receipt belongs to one logical Key and its human actor.
	var log Log
	tx := TelemetryDB().WithContext(ctx).Model(&Log{}).Where("request_id = ? AND type = ? AND token_id = ?", requestID, LogTypeConsume, tokenID)
	if tokenID <= 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(principals) > 0 && principals[0].ProjectID > 0 {
		p := principals[0]
		if p.TokenID != tokenID || p.ActorUserID <= 0 {
			return nil, gorm.ErrRecordNotFound
		}
		tx = tx.Where("user_id = ?", p.ActorUserID)
		var project Project
		if err := CoreDB().WithContext(ctx).First(&project, p.ProjectID).Error; err != nil {
			return nil, err
		}
		tx = ProjectLogScope(tx, project)
	} else {
		if userID <= 0 {
			return nil, gorm.ErrRecordNotFound
		}
		tx = tx.Where("user_id = ?", userID)
	}
	if err := tx.First(&log).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

func GetTokenConsumeSummaryByTokenID(tokenID int, startTimestamp int64, endTimestamp int64) (summary TokenConsumeSummary, err error) {
	return getConsumeSummaryByScope("token_id", tokenID, startTimestamp, startTimestamp > 0, endTimestamp, endTimestamp > 0)
}

func GetUserConsumeSummaryByUserIDInWindow(userID int, startTimestamp int64, endTimestamp int64) (summary TokenConsumeSummary, err error) {
	return getConsumeSummaryByScope("user_id", userID, startTimestamp, true, endTimestamp, true)
}

func getConsumeSummaryByScope(scopeColumn string, scopeID int, startTimestamp int64, applyStart bool, endTimestamp int64, applyEnd bool, principals ...ProjectPrincipal) (summary TokenConsumeSummary, err error) {
	if scopeID <= 0 {
		return summary, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": scopeColumn})
	}

	tx := TelemetryDB().Model(&Log{}).
		Where(scopeColumn+" = ? AND type = ?", scopeID, LogTypeConsume)
	if len(principals) > 0 {
		tx = applyProjectKeyUsageScope(tx, principals[0])
	}
	if applyStart {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if applyEnd {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}

	err = tx.Select(
		"COUNT(*) AS request_count, " +
			"COALESCE(SUM(amount_microusd), 0) AS amount_microusd, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(AVG(use_time), 0) AS avg_use_time",
	).Scan(&summary).Error
	return summary, err
}

func GetTokenUsageSeriesByTokenID(tokenID int, startTimestamp int64, endTimestamp int64, granularity string) ([]TokenUsageSeriesPoint, error) {
	return getUsageSeriesByScope("token_id", tokenID, startTimestamp, startTimestamp > 0, endTimestamp, endTimestamp > 0, granularity)
}

func GetUserUsageSeriesByUserIDInWindow(userID int, startTimestamp int64, endTimestamp int64, granularity string) ([]TokenUsageSeriesPoint, error) {
	return getUsageSeriesByScope("user_id", userID, startTimestamp, true, endTimestamp, true, granularity)
}

func getUsageSeriesByScope(scopeColumn string, scopeID int, startTimestamp int64, applyStart bool, endTimestamp int64, applyEnd bool, granularity string, principals ...ProjectPrincipal) ([]TokenUsageSeriesPoint, error) {
	if scopeID <= 0 {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": scopeColumn})
	}

	bucketExpr := tokenUsageSeriesBucketExpr(granularity)
	tx := TelemetryDB().Model(&Log{}).
		Where(scopeColumn+" = ? AND type = ?", scopeID, LogTypeConsume)
	if len(principals) > 0 {
		tx = applyProjectKeyUsageScope(tx, principals[0])
	}
	if applyStart {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if applyEnd {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}

	var points []TokenUsageSeriesPoint
	if err := tx.Select(
		bucketExpr + " AS bucket, " +
			"COUNT(*) AS request_count, " +
			"COALESCE(SUM(amount_microusd), 0) AS amount_microusd, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens",
	).
		Group("bucket").
		Order("bucket ASC").
		Scan(&points).Error; err != nil {
		return nil, err
	}
	return points, nil
}

func tokenUsageSeriesBucketExpr(granularity string) string {
	switch granularity {
	case "hour":
		return "created_at - (created_at % 3600)"
	case "month":
		switch TelemetryDB().Dialector.Name() {
		case common.DatabaseTypePostgreSQL:
			return "EXTRACT(EPOCH FROM (DATE_TRUNC('month', TO_TIMESTAMP(created_at) AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))::bigint"
		default:
			return "CAST(strftime('%s', datetime(created_at, 'unixepoch', 'start of month')) AS INTEGER)"
		}
	default:
		return "created_at - (created_at % 86400)"
	}
}

func GetTokenModelUsageByTokenID(tokenID int, startTimestamp int64, endTimestamp int64, limit int) (usages []TokenModelUsage, err error) {
	if tokenID <= 0 {
		return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "token_id"})
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	tx := TelemetryDB().Model(&Log{}).
		Where("token_id = ? AND type = ?", tokenID, LogTypeConsume)
	if startTimestamp > 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}

	err = tx.Select(
		"model_name, " +
			"COUNT(*) AS request_count, " +
			"COALESCE(SUM(amount_microusd), 0) AS amount_microusd, " +
			"COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) AS completion_tokens, " +
			"COALESCE(AVG(use_time), 0) AS avg_use_time, " +
			"MAX(created_at) AS last_used_at",
	).
		Where("model_name <> ''").
		Group("model_name").
		Order("amount_microusd DESC").
		Limit(limit).
		Find(&usages).Error
	return usages, err
}

func GetTokenConsumeLogsByTokenIDWithFilters(tokenID int, startTimestamp int64, endTimestamp int64, startIdx int, num int, filter TokenConsumeLogFilter) (logs []*Log, total int64, err error) {
	if tokenID <= 0 {
		return nil, 0, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "token_id"})
	}
	if num <= 0 {
		num = common.ItemsPerPage
	}
	if num > 100 {
		num = 100
	}
	if startIdx < 0 {
		startIdx = 0
	}

	tx := tokenConsumeLogsQuery(tokenID, startTimestamp, endTimestamp, filter)
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = orderLogsNewestFirst(tx).Limit(num).Offset(startIdx).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	formatUserLogs(logs)
	return logs, total, nil
}

// tokenConsumeLogsQuery is a key's consume log list before its order and page.
func tokenConsumeLogsQuery(tokenID int, startTimestamp int64, endTimestamp int64, filter TokenConsumeLogFilter) *gorm.DB {
	tx := TelemetryDB().Model(&Log{}).
		Where("token_id = ? AND type = ?", tokenID, LogTypeConsume)
	if startTimestamp > 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp > 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if filter.ModelName != "" {
		tx = tx.Where("model_name = ?", filter.ModelName)
	}
	if filter.Keyword != "" {
		keyword := "%" + strings.TrimSpace(filter.Keyword) + "%"
		tx = tx.Where(
			"model_name LIKE ? OR content LIKE ? OR request_id LIKE ?",
			keyword,
			keyword,
			keyword,
		)
	}

	return tx
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if TelemetryDB() != nil && TelemetryDB().Dialector.Name() == "postgres" {
		now := time.Now().UTC()
		days := int(now.Sub(time.Unix(targetTimestamp, 0)).Hours() / 24)
		if days < ModelRankingWindowDays {
			return 0, types.Err(types.ErrorCodeLogCleanupCutoffTooRecent, types.D{"min_days": ModelRankingWindowDays})
		}
		return 0, MaintainTelemetryPartitions(ctx, now, days)
	}

	// Manual cleanup must preserve the same source window as automatic retention.
	latestCutoff := time.Now().Add(-ModelRankingWindowDays * 24 * time.Hour).Unix()
	if targetTimestamp > latestCutoff {
		return 0, types.Err(types.ErrorCodeLogCleanupCutoffTooRecent, types.D{"min_days": ModelRankingWindowDays})
	}
	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		result := TelemetryDB().Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
		if nil != result.Error {
			return total, result.Error
		}

		total += result.RowsAffected

		if result.RowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}

func applyProjectKeyUsageScope(tx *gorm.DB, principal ProjectPrincipal) *gorm.DB {
	if principal.ProjectID <= 0 || principal.PayerUserID <= 0 || principal.TokenID <= 0 || principal.ActorUserID <= 0 {
		return tx.Where("1 = 0")
	}
	// A log row written before projects existed carries project_id 0; it still
	// belongs to this Key's summary when its user is the project's payer.
	return tx.Where("(project_id = ? OR (project_id = 0 AND user_id = ?)) AND user_id = ?",
		principal.ProjectID, principal.PayerUserID, principal.ActorUserID)
}
func GetProjectKeyConsumeSummary(principal ProjectPrincipal, start, end int64) (TokenConsumeSummary, error) {
	return getConsumeSummaryByScope("token_id", principal.TokenID, start, true, end, true, principal)
}
func GetProjectKeyUsageSeries(principal ProjectPrincipal, start, end int64, granularity string) ([]TokenUsageSeriesPoint, error) {
	return getUsageSeriesByScope("token_id", principal.TokenID, start, true, end, true, granularity, principal)
}

// CountTokenEffortClampedSince counts, per Key, the requests since `since`
// whose reasoning effort the gateway lowered to a cap.
func CountTokenEffortClampedSince(tokenIDs []int, since int64) (map[int]int64, error) {
	result := make(map[int]int64)
	if len(tokenIDs) == 0 || TelemetryDB() == nil || !TelemetryDB().Migrator().HasTable(&Log{}) {
		return result, nil
	}
	type row struct {
		TokenID int   `gorm:"column:token_id"`
		Count   int64 `gorm:"column:n"`
	}
	rows := make([]row, 0)
	if err := TelemetryDB().Model(&Log{}).
		Select("token_id, COUNT(*) AS n").
		Where(effortClampedCondition(TelemetryDB())+" AND request_ts >= ? AND type = ? AND token_id IN ? AND created_at >= ?", logRequestTSFloor(since), LogTypeConsume, tokenIDs, since).
		Group("token_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, item := range rows {
		result[item.TokenID] = item.Count
	}
	return result, nil
}

// stampRequestFacts adds what every usage and error row records about the
// request itself: the client's raw User-Agent (clipped) and whether the
// client went away before the gateway finished.
func stampRequestFacts(c *gin.Context, other map[string]interface{}) {
	if c == nil || c.Request == nil || other == nil {
		return
	}
	for key, value := range common.EnsureRelayTiming(c).Fields() {
		other[key] = value
	}
	if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
		if len(ua) > 256 {
			ua = ua[:256]
		}
		other["user_agent"] = ua
	}
	if errors.Is(c.Request.Context().Err(), context.Canceled) {
		other["client_cancelled"] = true
	}
}
