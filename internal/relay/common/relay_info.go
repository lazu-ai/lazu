package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// ErrStreamStartDeadline is why a streamed attempt was abandoned at
// StreamStartDeadline. It wraps context.DeadlineExceeded so every reader of a
// stream failure reports it as an upstream timeout.
var ErrStreamStartDeadline = fmt.Errorf("upstream did not start the stream before the deadline: %w", context.DeadlineExceeded)

type ThinkingContentInfo struct {
	IsFirstThinkingContent  bool
	SendLastThinkingContent bool
	HasSentThinkingContent  bool
}

const (
	LastMessageTypeNone     = "none"
	LastMessageTypeText     = "text"
	LastMessageTypeTools    = "tools"
	LastMessageTypeThinking = "thinking"
)

type ClaudeConvertInfo struct {
	LastMessagesType string
	Index            int
	Usage            *dto.Usage
	FinishReason     string
	Done             bool
}

type RerankerInfo struct {
	Documents       []any
	ReturnDocuments bool
}

type BuildInToolInfo struct {
	ToolName          string
	CallCount         int
	SearchContextSize string
}

type ResponsesUsageInfo struct {
	BuiltInTools map[string]*BuildInToolInfo
}

type ChannelMeta struct {
	ChannelType          int
	ChannelId            int
	ChannelIsMultiKey    bool
	ChannelMultiKeyIndex int
	ChannelBaseUrl       string
	ApiType              int
	ApiVersion           string
	ApiKey               string
	Organization         string
	ChannelCreateTime    int64
	ParamOverride        map[string]interface{}
	HeadersOverride      map[string]interface{}
	ChannelSetting       dto.ChannelSettings
	ChannelOtherSettings dto.ChannelOtherSettings
	UpstreamModelName    string
	IsModelMapped        bool
	SupportStreamOptions bool // 是否支持流式选项
}

type RelayInfo struct {
	RedisLedgerInput          *ledger.Input
	LedgerSettlementFinalized bool
	TokenCountMeta            *types.TokenCountMeta
	NativeBodyCache           []byte
	NativeBodyChannelID       int
	ProjectID                 int
	BudgetSettlementAt        time.Time
	PayerUserID               int
	ActorUserID               int
	RequestID                 string
	SessionID                 string
	AgentID                   string
	TokenId                   int
	TokenKey                  string
	UserId                    int
	UsingGroup                string // 使用的分组
	PricePlanID               int    // 专属折扣方案；项目请求取付款人的
	TokenUnlimited            bool
	TokenUsageAlertPercent    int
	StartTime                 time.Time
	FirstResponseTime         time.Time
	isFirstResponse           bool
	// StreamStartDeadline is when a streamed request stops waiting for an
	// upstream to start answering, across all of its attempts: the response
	// headers, and then the first data event. Once an attempt has delivered
	// one, only the idle timeout applies to it. Zero means unbounded, which is
	// every non-streamed request (its headers arrive with the whole answer)
	// and every call that does not come through the relay loop.
	StreamStartDeadline time.Time
	// FirstOutputTime is when the first event carrying output arrived. It is
	// what time-to-first-token means, and it is kept apart from
	// FirstResponseTime because a stream usually opens with a metadata frame —
	// a chat role chunk, a Responses response.created — and the retry logic
	// rightly treats that frame as "the client has been answered" while no
	// token has been produced yet. Zero when nothing carrying output arrived.
	FirstOutputTime time.Time
	// FirstAnswerTime is when the first event carrying answer output — text
	// or tool-call arguments, not reasoning — arrived. Decode speed is read
	// from here to the end: reasoning that an upstream does not stream all
	// happens before it, and is billed without ever reaching the client.
	FirstAnswerTime time.Time
	// lastChunkAt is when the previous output chunk arrived, and MaxStallMs the
	// longest gap between two of them. Average throughput is blind to the shape
	// of a stream: a lane that emits 24 tokens a second and then stops for six
	// seconds reports the same tokens-per-second as one that never pauses, while
	// the client shows a spinner. Only the worst gap tells them apart.
	lastChunkAt time.Time
	MaxStallMs  int
	// FinishReason is what the upstream said ended the reply, in OpenAI's
	// vocabulary. Empty when the channel reports none.
	FinishReason string
	//SendLastReasoningResponse bool
	IsStream                   bool
	IsGeminiBatchEmbedding     bool
	IsPlayground               bool
	UsePrice                   bool
	RelayMode                  int
	OriginModelName            string
	RequestURLPath             string
	PromptTokens               int
	UsagePromptTokens          int
	UsagePromptTokensSet       bool
	UsagePromptTokensAvailable bool
	ShouldIncludeUsage         bool
	DisablePing                bool // 是否禁止向下游发送自定义 Ping
	ClientWs                   *websocket.Conn
	TargetWs                   *websocket.Conn
	InputAudioFormat           string
	OutputAudioFormat          string
	RealtimeTools              []dto.RealTimeTool
	IsFirstRequest             bool
	AudioUsage                 bool
	ReasoningEffort            string
	UserSetting                dto.UserSetting
	UserEmail                  string
	UserQuota                  billing.MicroUSD
	RelayFormat                types.RelayFormat
	SendResponseCount          int
	FinalPreConsumedQuota      billing.MicroUSD // 最终预消耗的配额
	SettledChargeMicrousd      billing.MicroUSD
	StreamFailedWithoutUsage   bool // This attempt failed without provider usage or billable output; refund even if metadata arrived.
	IsClaudeBetaQuery          bool // /v1/messages?beta=true
	ProtocolRequest            *textgen.Request
	ProtocolPlan               *textgen.RelayPlan

	PriceData types.PriceData

	Request dto.Request

	ThinkingContentInfo
	*ClaudeConvertInfo
	*RerankerInfo
	*ResponsesUsageInfo
	*ChannelMeta
}

func (info *RelayInfo) InitChannelMeta(c *gin.Context) {
	channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	paramOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride)
	headerOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelHeaderOverride)
	apiType, _ := common.ChannelType2APIType(channelType)
	channelMeta := &ChannelMeta{
		ChannelType:          channelType,
		ChannelId:            common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelIsMultiKey:    common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey),
		ChannelMultiKeyIndex: common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
		ChannelBaseUrl:       common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl),
		ApiType:              apiType,
		ApiVersion:           common.GetContextKeyString(c, constant.ContextKeyChannelAPIVersion),
		ApiKey:               common.GetContextKeyString(c, constant.ContextKeyChannelKey),
		Organization:         c.GetString("channel_organization"),
		ChannelCreateTime:    c.GetInt64("channel_create_time"),
		ParamOverride:        paramOverride,
		HeadersOverride:      headerOverride,
		UpstreamModelName:    common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		IsModelMapped:        false,
		SupportStreamOptions: false,
	}

	channelSetting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if ok {
		channelMeta.ChannelSetting = channelSetting
	}

	channelOtherSettings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	if ok {
		channelMeta.ChannelOtherSettings = channelOtherSettings
	}

	if streamSupportedChannels[channelMeta.ChannelType] {
		channelMeta.SupportStreamOptions = true
	}

	info.ChannelMeta = channelMeta

	// reset some fields based on channel meta
	// 重置某些字段，例如模型名称等
	if info.Request != nil {
		info.Request.SetModelName(info.OriginModelName)
	}
}

// ToString is a short debug line built from an allowlist: identifiers,
// protocol and model names. It never prints keys, emails, base URLs or
// organizations, and must not be replaced by %+v, which would.
func (info *RelayInfo) ToString() string {
	if info == nil {
		return "RelayInfo<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "RelayInfo{format=%s mode=%d", info.RelayFormat, info.RelayMode)
	if plan := info.ProtocolPlan; plan != nil {
		fmt.Fprintf(b, " plan=%s->%s native=%t", plan.ClientProtocol.Family, plan.UpstreamProtocol.Family, plan.Native)
	}
	fmt.Fprintf(b, " stream=%t model=%q user=%d token=%d", info.IsStream, info.OriginModelName, info.UserId, info.TokenId)
	if cm := info.ChannelMeta; cm != nil {
		fmt.Fprintf(b, " channel=%d type=%d upstream_model=%q mapped=%t", cm.ChannelId, cm.ChannelType, cm.UpstreamModelName, cm.IsModelMapped)
	}
	if info.RequestID != "" {
		fmt.Fprintf(b, " request=%s", info.RequestID)
	}
	b.WriteString("}")
	return b.String()
}

// 定义支持流式选项的通道类型
var streamSupportedChannels = map[int]bool{
	constant.ChannelTypeOpenAI:    true,
	constant.ChannelTypeAnthropic: true,
	constant.ChannelTypeGemini:    true,
	constant.ChannelTypeXai:       true,
}

func GenRelayInfoWs(c *gin.Context, ws *websocket.Conn) *RelayInfo {
	info := genBaseRelayInfo(c, nil)
	info.RelayFormat = types.RelayFormatOpenAIRealtime
	info.ClientWs = ws
	info.InputAudioFormat = "pcm16"
	info.OutputAudioFormat = "pcm16"
	info.IsFirstRequest = true
	return info
}

func GenRelayInfoClaude(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatClaude
	info.ShouldIncludeUsage = false
	info.ClaudeConvertInfo = &ClaudeConvertInfo{
		LastMessagesType: LastMessageTypeNone,
	}
	if c.Query("beta") == "true" {
		info.IsClaudeBetaQuery = true
	}
	return info
}

func GenRelayInfoRerank(c *gin.Context, request *dto.RerankRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayMode = relayconstant.RelayModeRerank
	info.RelayFormat = types.RelayFormatRerank
	info.RerankerInfo = &RerankerInfo{
		Documents:       request.Documents,
		ReturnDocuments: request.GetReturnDocuments(),
	}
	return info
}

func GenRelayInfoOpenAIAudio(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAIAudio
	return info
}

func GenRelayInfoEmbedding(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatEmbedding
	return info
}

func GenRelayInfoResponses(c *gin.Context, request *dto.OpenAIResponsesRequest) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayMode = relayconstant.RelayModeResponses
	info.RelayFormat = types.RelayFormatOpenAIResponses

	PopulateResponsesUsageInfo(info, request)
	return info
}

func PopulateResponsesUsageInfo(info *RelayInfo, request *dto.OpenAIResponsesRequest) {
	if info == nil || request == nil {
		return
	}

	info.ResponsesUsageInfo = &ResponsesUsageInfo{
		BuiltInTools: make(map[string]*BuildInToolInfo),
	}
	if len(request.Tools) == 0 {
		return
	}

	for _, tool := range request.GetToolsMap() {
		toolType := common.Interface2String(tool["type"])
		if toolType == "" {
			continue
		}
		info.ResponsesUsageInfo.BuiltInTools[toolType] = &BuildInToolInfo{
			ToolName:  toolType,
			CallCount: 0,
		}
		switch toolType {
		case dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview:
			searchContextSize := common.Interface2String(tool["search_context_size"])
			if searchContextSize == "" {
				searchContextSize = "medium"
			}
			info.ResponsesUsageInfo.BuiltInTools[toolType].SearchContextSize = searchContextSize
		}
	}
}

func GenRelayInfoGemini(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatGemini
	info.ShouldIncludeUsage = false

	return info
}

func GenRelayInfoImage(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAIImage
	return info
}

func GenRelayInfoOpenAI(c *gin.Context, request dto.Request) *RelayInfo {
	info := genBaseRelayInfo(c, request)
	info.RelayFormat = types.RelayFormatOpenAI
	return info
}

func genBaseRelayInfo(c *gin.Context, request dto.Request) *RelayInfo {

	//channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	//channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	//paramOverride := common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride)

	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}

	isStream := false

	if request != nil {
		isStream = request.IsStream(c)
	}
	common.SetContextKey(c, constant.ContextKeyIsStream, isStream)

	// firstResponseTime = time.Now() - 1 second

	finalGroup, _ := c.Get("final_group")
	finalGroupStr, _ := finalGroup.(string)

	info := &RelayInfo{
		ProjectID:   common.GetContextKeyInt(c, constant.ContextKeyProjectID),
		PayerUserID: common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
		ActorUserID: common.GetContextKeyInt(c, constant.ContextKeyActorUserID),
		Request:     request,

		RequestID: c.GetString(common.RequestIdKey),
		SessionID: common.GetContextKeyString(c, constant.ContextKeySessionID),
		AgentID:   common.GetContextKeyString(c, constant.ContextKeyAgentID),
		UserId:    common.GetContextKeyInt(c, constant.ContextKeyUserId),
		// 先取上下文中的 UsingGroup，后面会在价格/配额阶段进行兜底
		UsingGroup:  common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		PricePlanID: common.GetContextKeyInt(c, constant.ContextKeyUserPricePlanID),
		UserQuota:   common.GetContextKeyMicroUSD(c, constant.ContextKeyUserQuota),
		UserEmail:   common.GetContextKeyString(c, constant.ContextKeyUserEmail),

		OriginModelName: common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		PromptTokens:    common.GetContextKeyInt(c, constant.ContextKeyPromptTokens),

		TokenId:                common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenKey:               common.GetContextKeyString(c, constant.ContextKeyTokenKey),
		TokenUnlimited:         common.GetContextKeyBool(c, constant.ContextKeyTokenUnlimited),
		TokenUsageAlertPercent: common.GetContextKeyInt(c, constant.ContextKeyTokenUsageAlertPercent),

		isFirstResponse: true,
		RelayMode:       relayconstant.Path2RelayMode(c.Request.URL.Path),
		RequestURLPath:  c.Request.URL.String(),
		IsStream:        isStream,

		StartTime:         startTime,
		FirstResponseTime: startTime.Add(-time.Second),
		ThinkingContentInfo: ThinkingContentInfo{
			IsFirstThinkingContent:  true,
			SendLastThinkingContent: false,
		},
	}

	// 如果已经明确使用分组，更新 UsingGroup
	if finalGroupStr != "" {
		info.UsingGroup = finalGroupStr
	}

	if info.RelayMode == relayconstant.RelayModeUnknown {
		info.RelayMode = c.GetInt("relay_mode")
	}

	if strings.HasPrefix(c.Request.URL.Path, "/pg") {
		info.IsPlayground = true
		info.RequestURLPath = strings.TrimPrefix(info.RequestURLPath, "/pg")
		info.RequestURLPath = "/v1" + info.RequestURLPath
	}

	userSetting, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	if ok {
		info.UserSetting = userSetting
	}

	return info
}

func GenRelayInfo(c *gin.Context, relayFormat types.RelayFormat, request dto.Request, ws *websocket.Conn) (*RelayInfo, error) {
	switch relayFormat {
	case types.RelayFormatOpenAI:
		return GenRelayInfoOpenAI(c, request), nil
	case types.RelayFormatOpenAIAudio:
		return GenRelayInfoOpenAIAudio(c, request), nil
	case types.RelayFormatOpenAIImage:
		return GenRelayInfoImage(c, request), nil
	case types.RelayFormatOpenAIRealtime:
		return GenRelayInfoWs(c, ws), nil
	case types.RelayFormatClaude:
		return GenRelayInfoClaude(c, request), nil
	case types.RelayFormatRerank:
		if request, ok := request.(*dto.RerankRequest); ok {
			return GenRelayInfoRerank(c, request), nil
		}
		return nil, errors.New("request is not a RerankRequest")
	case types.RelayFormatDecisions:
		info := genBaseRelayInfo(c, request)
		info.RelayMode = relayconstant.RelayModeDecisions
		info.RelayFormat = types.RelayFormatDecisions
		return info, nil
	case types.RelayFormatGemini:
		return GenRelayInfoGemini(c, request), nil
	case types.RelayFormatEmbedding:
		return GenRelayInfoEmbedding(c, request), nil
	case types.RelayFormatOpenAIResponses:
		if request, ok := request.(*dto.OpenAIResponsesRequest); ok {
			return GenRelayInfoResponses(c, request), nil
		}
		return nil, errors.New("request is not a OpenAIResponsesRequest")
	default:
		return nil, errors.New("invalid relay format")
	}
}

func (info *RelayInfo) SetPromptTokens(promptTokens int) {
	info.PromptTokens = promptTokens
}

func (info *RelayInfo) SetFirstResponseTime() {
	if info.isFirstResponse {
		info.FirstResponseTime = time.Now()
		info.isFirstResponse = false
	}
}

// MarkOutputChunk records that an event carrying output just arrived: the
// first one stamps FirstOutputTime, and every later one widens the worst gap
// seen so far. The wait before the first has no predecessor — it is
// time-to-first-token, which means something different — so it is not a
// stall. Protocol frames between output chunks are not chunks here: a keep-
// alive arriving on time is exactly the case where a stream looks healthy
// while no output is moving.
func (info *RelayInfo) MarkOutputChunk(answer bool) {
	now := time.Now()
	if info.FirstOutputTime.IsZero() {
		info.FirstOutputTime = now
	}
	if answer && info.FirstAnswerTime.IsZero() {
		info.FirstAnswerTime = now
	}
	if !info.lastChunkAt.IsZero() {
		if gap := int(now.Sub(info.lastChunkAt).Milliseconds()); gap > info.MaxStallMs {
			info.MaxStallMs = gap
		}
	}
	info.lastChunkAt = now
}

// ResetFirstResponse re-arms the first-response marker for a new attempt.
//
// The marker is time-to-first-token for the attempt that ends up serving the
// client, so a retried attempt must not inherit the previous one's. Leaving it
// set would also make HasSendResponse permanently true for the rest of the
// request, which both blocks further retries and suppresses the refund of a
// reservation that bought nothing.
func (info *RelayInfo) ResetFirstResponse() {
	info.isFirstResponse = true
	// A retried attempt must not inherit the abandoned one's stream shape
	// either: its gaps belong to a response the client never saw.
	info.lastChunkAt = time.Time{}
	info.MaxStallMs = 0
	info.FirstOutputTime = time.Time{}
	info.FirstAnswerTime = time.Time{}
	// The same "never responded" sentinel GenRelayInfo uses, not the zero time:
	// the recorded frt is StartTime-relative and is stored unclamped on every
	// request that never stamps one, which is all non-streaming traffic.
	info.FirstResponseTime = info.StartTime.Add(-time.Second)
}

// OutputTTFT is time-to-first-token measured from the given start, or zero
// when no event carrying output arrived.
func (info *RelayInfo) OutputTTFT(from time.Time) time.Duration {
	if info.FirstOutputTime.IsZero() || !info.FirstOutputTime.After(from) {
		return 0
	}
	return info.FirstOutputTime.Sub(from)
}

// AnswerTTFT is the time to the first answer token measured from the given
// start, or zero when no answer output arrived.
func (info *RelayInfo) AnswerTTFT(from time.Time) time.Duration {
	if info.FirstAnswerTime.IsZero() || !info.FirstAnswerTime.After(from) {
		return 0
	}
	return info.FirstAnswerTime.Sub(from)
}

func (info *RelayInfo) HasSendResponse() bool {
	return info.FirstResponseTime.After(info.StartTime)
}

// RemoveDisabledFields 从请求 JSON 数据中移除渠道设置中禁用的字段
// service_tier: 服务层级字段，可能导致额外计费（OpenAI、Claude、Responses API 支持）
// speed: Claude 推理速度模式字段（仅 Claude 支持，默认过滤）
// inference_geo: Claude 数据驻留区域字段（仅 Claude 支持，默认过滤）
// store: 数据存储授权字段，涉及用户隐私（仅 OpenAI、Responses API 支持，默认允许透传，禁用后可能导致 Codex 无法使用）
// safety_identifier: 安全标识符，用于向 OpenAI 报告违规用户（仅 OpenAI 支持，涉及用户隐私）
// stream_options.include_obfuscation: Responses API 流混淆控制字段（默认过滤）
func RemoveDisabledFields(jsonData []byte, channelOtherSettings dto.ChannelOtherSettings, channelPassThroughEnabled bool) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || channelPassThroughEnabled {
		return jsonData, nil
	}

	fields := []struct {
		path   string
		remove bool
	}{
		{"service_tier", !channelOtherSettings.AllowServiceTier},
		{"speed", !channelOtherSettings.AllowSpeed},
		{"inference_geo", !channelOtherSettings.AllowInferenceGeo},
		{"store", channelOtherSettings.DisableStore},
		{"safety_identifier", !channelOtherSettings.AllowSafetyIdentifier},
		{"stream_options.include_obfuscation", !channelOtherSettings.AllowIncludeObfuscation},
	}
	for _, field := range fields {
		if !field.remove || !gjson.GetBytes(jsonData, field.path).Exists() {
			continue
		}
		var err error
		jsonData, err = sjson.DeleteBytes(jsonData, field.path)
		if err != nil {
			return nil, err
		}
	}
	if options := gjson.GetBytes(jsonData, "stream_options"); options.IsObject() && len(options.Map()) == 0 {
		return sjson.DeleteBytes(jsonData, "stream_options")
	}
	return jsonData, nil
}

// BillingUserID is separate from request identity. Legacy callers without a
// project retain their existing personal-wallet behavior.
func (info *RelayInfo) BillingUserID() int {
	if info.PayerUserID > 0 {
		return info.PayerUserID
	}
	return info.UserId
}
func (info *RelayInfo) ActorID() int {
	if info.ProjectID > 0 {
		return info.ActorUserID
	}
	return info.UserId
}
