package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	common2 "github.com/lazu-ai/lazu/internal/common"
	appconstant "github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func SetupApiRequestHeader(info *common.RelayInfo, c *gin.Context, req *http.Header) {
	if info.RelayMode == constant.RelayModeAudioTranscription || info.RelayMode == constant.RelayModeAudioTranslation {
		// multipart/form-data
	} else if info.RelayMode == constant.RelayModeRealtime {
		// websocket
	} else {
		req.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		req.Set("Accept", c.Request.Header.Get("Accept"))
		if info.IsStream && c.Request.Header.Get("Accept") == "" {
			req.Set("Accept", "text/event-stream")
		}
	}

	forwardProtocolPassthroughHeaders(c.Request.Header, req)
	if info.ChannelMeta != nil && info.ChannelOtherSettings.CodexClientOnly && common2.IsCodexResponsesRequest(c.Request) {
		common2.ForwardCodexHeaders(c.Request.Header, *req)
	}
	if info.ChannelMeta != nil && info.ChannelOtherSettings.ClaudeCodeClientOnly && common2.IsClaudeCodeRequest(c.Request) {
		common2.ForwardClaudeCodeHeaders(c.Request.Header, *req)
	}
}

// clientRestrictionError refuses a request the source's client restriction
// does not admit. The selector already filters these out; this is the last
// line before bytes leave the gateway.
func clientRestrictionError(c *gin.Context, info *common.RelayInfo) error {
	if info.ChannelMeta == nil {
		return nil
	}
	if info.ChannelOtherSettings.CodexClientOnly && (c == nil || !common2.IsCodexResponsesRequest(c.Request)) {
		return errors.New("channel requires an original Codex Responses request")
	}
	if info.ChannelOtherSettings.ClaudeCodeClientOnly && (c == nil || !common2.IsClaudeCodeRequest(c.Request)) {
		return errors.New("channel requires a Claude Code request")
	}
	return nil
}

var passthroughHeaderPrefixes = []string{
	"openai-",
	"anthropic-",
	"x-goog-",
}

// blockedAnthropicBetaFlags 已知会导致第三方上游（经销商、Bedrock、Azure）报错的 beta flag 黑名单
// 发现新的不兼容 flag 时在此处追加
var blockedAnthropicBetaFlags = map[string]struct{}{
	"adaptive-thinking-2025-11-24": {},
}

// FilterAnthropicBeta 过滤 anthropic-beta header 中不兼容的 beta flag
func FilterAnthropicBeta(betaHeader string) string {
	if betaHeader == "" {
		return ""
	}
	var filtered []string
	for _, flag := range strings.Split(betaHeader, ",") {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			continue
		}
		if _, blocked := blockedAnthropicBetaFlags[flag]; blocked {
			continue
		}
		filtered = append(filtered, flag)
	}
	return strings.Join(filtered, ",")
}

var blockedPassthroughHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"x-api-key":           {},
	"api-key":             {},
	"x-goog-api-key":      {},
	"cookie":              {},
	"x-forwarded-for":     {},
	"x-forwarded-proto":   {},
	"host":                {},
}

// forwardProtocolPassthroughHeaders forwards protocol-level compatibility headers.
// It intentionally blocks credential headers to avoid leaking caller secrets upstream.
func forwardProtocolPassthroughHeaders(src http.Header, dst *http.Header) {
	for key, values := range src {
		if len(values) == 0 {
			continue
		}

		lowerKey := strings.ToLower(key)
		if _, blocked := blockedPassthroughHeaders[lowerKey]; blocked {
			continue
		}

		allowed := false
		for _, prefix := range passthroughHeaderPrefixes {
			if strings.HasPrefix(lowerKey, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}

		// 对 anthropic-beta header 做黑名单过滤
		if lowerKey == "anthropic-beta" {
			dst.Del(key)
			for _, value := range values {
				filtered := FilterAnthropicBeta(value)
				if filtered != "" {
					dst.Add(key, filtered)
				}
			}
			continue
		}

		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// processHeaderOverride 处理请求头覆盖，支持变量替换
// 支持的变量：{api_key}
func processHeaderOverride(info *common.RelayInfo) (map[string]string, error) {
	headerOverride := make(map[string]string)
	for k, v := range info.HeadersOverride {
		str, ok := v.(string)
		if !ok {
			return nil, types.NewError(nil, types.ErrorCodeChannelHeaderOverrideInvalid)
		}

		// 替换支持的变量
		if strings.Contains(str, "{api_key}") {
			str = strings.ReplaceAll(str, "{api_key}", info.ApiKey)
		}

		headerOverride[k] = str
	}
	return headerOverride, nil
}

func DoApiRequest(a BaseAdaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	requestBody, err = capUpstreamEffort(c, info, fullRequestURL, requestBody)
	if err != nil {
		return nil, err
	}
	if common2.DebugEnabled {
		println("fullRequestURL:", fullRequestURL)
	}
	req, err := newUpstreamRequest(c, c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	headers := req.Header
	headerOverride, err := processHeaderOverride(info)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		headers.Set(key, value)
	}
	err = a.SetupRequestHeader(c, &headers, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	// Adapter and channel headers are final now; keep their betas and add the
	// capability required by the final Claude effort payload exactly once.
	if c.GetBool(effortBetaRequiredContext) && !hasBeta(req.Header, claudeEffortBeta) {
		req.Header.Add("anthropic-beta", claudeEffortBeta)
	}
	// Check the final wire request, after every header writer, on both the
	// raw-body and DTO paths. This must run before provider-attempt telemetry.
	if err := applyClaudeSystemBetaCompatibility(c, req, info); err != nil {
		return nil, err
	}
	if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
		return nil, err
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func DoFormRequest(a BaseAdaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	if common2.DebugEnabled {
		println("fullRequestURL:", fullRequestURL)
	}
	req, err := newUpstreamRequest(c, c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	// set form data
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	headers := req.Header
	headerOverride, err := processHeaderOverride(info)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		headers.Set(key, value)
	}
	err = a.SetupRequestHeader(c, &headers, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func DoWssRequest(a BaseAdaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*websocket.Conn, error) {
	if err := clientRestrictionError(c, info); err != nil {
		return nil, err
	}

	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	targetHeader := http.Header{}
	headerOverride, err := processHeaderOverride(info)
	if err != nil {
		return nil, err
	}
	for key, value := range headerOverride {
		targetHeader.Set(key, value)
	}
	err = a.SetupRequestHeader(c, &targetHeader, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	targetHeader.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	common2.SetContextKey(c, appconstant.ContextKeyProviderAttempted, true)
	common2.SetContextKey(c, appconstant.ContextKeyAnyProviderAttempted, true)
	targetConn, _, err := websocket.DefaultDialer.DialContext(requestContext(c), fullRequestURL, targetHeader)
	if err != nil {
		return nil, fmt.Errorf("dial failed to %s: %w", fullRequestURL, err)
	}
	// send request body
	//all, err := io.ReadAll(requestBody)
	//err = service.WssString(c, targetConn, string(all))
	return targetConn, nil
}

func startPingKeepAlive(c *gin.Context, pingInterval time.Duration) context.CancelFunc {
	pingerCtx, stopPinger := context.WithCancel(context.Background())

	common2.SafeCtxGo(pingerCtx, "sse-ping-keepalive", func() {
		defer func() {
			// 增加panic恢复处理
			if r := recover(); r != nil {
				if common2.DebugEnabled {
					println("SSE ping goroutine panic recovered:", fmt.Sprintf("%v", r))
				}
			}
			if common2.DebugEnabled {
				println("SSE ping goroutine stopped.")
			}
		}()

		if pingInterval <= 0 {
			pingInterval = helper.DefaultPingInterval
		}

		ticker := time.NewTicker(pingInterval)
		// 确保在任何情况下都清理ticker
		defer func() {
			ticker.Stop()
			if common2.DebugEnabled {
				println("SSE ping ticker stopped")
			}
		}()

		var pingMutex sync.Mutex
		if common2.DebugEnabled {
			println("SSE ping goroutine started")
		}

		// 增加超时控制，防止goroutine长时间运行
		maxPingDuration := 120 * time.Minute // 最大ping持续时间
		pingTimeout := time.NewTimer(maxPingDuration)
		defer pingTimeout.Stop()

		for {
			select {
			// 发送 ping 数据
			case <-ticker.C:
				if err := sendPingData(c, &pingMutex); err != nil {
					if common2.DebugEnabled {
						println("SSE ping error, stopping goroutine:", err.Error())
					}
					return
				}
			// 收到退出信号
			case <-pingerCtx.Done():
				return
			// request 结束
			case <-c.Request.Context().Done():
				return
			// 超时保护，防止goroutine无限运行
			case <-pingTimeout.C:
				if common2.DebugEnabled {
					println("SSE ping goroutine timeout, stopping")
				}
				return
			}
		}
	})

	return stopPinger
}

func sendPingData(c *gin.Context, mutex *sync.Mutex) error {
	// 增加超时控制，防止锁死等待
	done := make(chan error, 1)
	common2.SafeCtxGo(c.Request.Context(), "sse-ping-write", func() {
		mutex.Lock()
		defer mutex.Unlock()

		err := helper.PingData(c)
		if err != nil {
			logger.LogError(c, "SSE ping error: "+err.Error())
			done <- err
			return
		}

		if common2.DebugEnabled {
			println("SSE ping data sent.")
		}
		done <- nil
	})

	// 设置发送ping数据的超时时间
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("SSE ping data send timeout")
	case <-c.Request.Context().Done():
		return errors.New("request context cancelled during ping")
	}
}

func DoRequest(c *gin.Context, req *http.Request, info *common.RelayInfo) (*http.Response, error) {
	return doRequest(c, req, info)
}

func newUpstreamRequest(c *gin.Context, method string, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(requestContext(c), method, url, body)
	if err != nil {
		return nil, err
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Hostname() == "" {
		return nil, errors.New("invalid upstream URL: configure the channel base URL with an http:// or https:// scheme and a host")
	}
	return req, nil
}

func requestContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func doRequest(c *gin.Context, req *http.Request, info *common.RelayInfo) (*http.Response, error) {
	if err := clientRestrictionError(c, info); err != nil {
		return nil, err
	}

	attemptCtx, disarmStartDeadline, endAttempt := withStreamStartDeadline(requestContext(c), info)
	req = req.WithContext(attemptCtx)

	var client *http.Client
	var err error
	if info.ChannelSetting.Proxy != "" {
		if info.IsStream {
			client, err = service.NewStreamingProxyHttpClient(info.ChannelSetting.Proxy)
		} else {
			client, err = service.NewProxyHttpClient(info.ChannelSetting.Proxy)
		}
		if err != nil {
			return nil, fmt.Errorf("new proxy http client failed: %w", err)
		}
	} else if info.IsStream {
		client = service.GetRelayStreamingHttpClient()
	} else {
		client = service.GetRelayHttpClient()
	}

	var stopPinger context.CancelFunc
	if info.IsStream {
		helper.SetEventStreamHeaders(c)
		// 处理流式请求的 ping 保活
		generalSettings := operation_setting.GetGeneralSetting()
		if generalSettings.PingIntervalEnabled && !info.DisablePing {
			pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
			stopPinger = startPingKeepAlive(c, pingInterval)
			// 使用defer确保在任何情况下都能停止ping goroutine
			defer func() {
				if stopPinger != nil {
					stopPinger()
					if common2.DebugEnabled {
						println("SSE ping goroutine stopped by defer")
					}
				}
			}()
		}
	}

	// This is the exact boundary between gateway-local request preparation and
	// an upstream provider attempt. Health telemetry must not charge failures
	// before this line to the selected channel.
	releaseCapacity, capacityErr := service.AcquireChannelCapacity(req.Context(), info.ChannelId, info.ChannelSetting)
	if capacityErr != nil {
		endAttempt()
		if errors.Is(context.Cause(attemptCtx), common.ErrStreamStartDeadline) {
			// A retry may start with little of the budget left and spend it
			// waiting on capacity admission; that is still a timeout.
			return nil, streamStartDeadlineError(c)
		}
		var exhausted *service.ChannelCapacityError
		if errors.As(capacityErr, &exhausted) {
			seconds := int((exhausted.RetryAfter + time.Second - 1) / time.Second)
			common2.SetContextKey(c, appconstant.ContextKeyUpstreamRetryAfter, seconds)
			c.Header("Retry-After", fmt.Sprint(seconds))
			return nil, types.NewError(capacityErr, types.ErrorCodeUpstreamCapacity)
		}
		return nil, types.NewError(capacityErr, types.ErrorCodeInternalError)
	}
	common2.SetContextKey(c, appconstant.ContextKeyProviderAttempted, true)
	common2.SetContextKey(c, appconstant.ContextKeyAnyProviderAttempted, true)
	if info.RelayFormat == types.RelayFormatDecisions {
		common2.SetContextKey(c, appconstant.ContextKeyDecisionUpstreamStartedAt, time.Now())
		metrics.Default.OnPreCall(info.ChannelId)
	}
	timing := common2.EnsureRelayTiming(c)
	// Pool/dial/TLS waits are gateway preparation. Start the provider interval
	// only when the request headers have been written onto the transport.
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{WroteHeaders: timing.StartUpstream, GotFirstResponseByte: timing.FirstUpstreamByte}))
	resp, err := client.Do(req)
	if err == nil && !disarmStartDeadline() {
		// The deadline fired between the headers arriving and the disarm:
		// the attempt context is being cancelled, so the body is unreadable.
		_ = resp.Body.Close()
		resp, err = nil, common.ErrStreamStartDeadline
	}
	if err != nil {
		releaseCapacity()
		endAttempt()
		if errors.Is(err, common.ErrStreamStartDeadline) || errors.Is(context.Cause(attemptCtx), common.ErrStreamStartDeadline) {
			// Checked before the client-cancel branch below: the attempt
			// context was cancelled by the deadline, not by the client, and
			// a slow provider must not be recorded as a client that left.
			return nil, streamStartDeadlineError(c)
		}
		if errors.Is(err, context.Canceled) && errors.Is(req.Context().Err(), context.Canceled) {
			logger.LogWarn(c, "upstream request stopped because the client canceled the request")
			return nil, types.NewError(
				err,
				types.ErrorCodeClientCanceled,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
				types.ErrOptionWithHideErrMsg("request canceled by client"),
				types.ErrOptionWithDetails(map[string]string{"raw_message": common2.MaskSensitiveInfo(err.Error())}),
			)
		}
		logger.LogError(c, "do request failed: "+err.Error())
		code := types.ErrorCodeUpstreamNetworkError
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			code = types.ErrorCodeUpstreamTimeout
		}
		return nil, types.NewError(
			err,
			code,
			types.ErrOptionWithHideErrMsg("upstream provider connection error"),
			types.ErrOptionWithDetails(map[string]string{"raw_message": common2.MaskSensitiveInfo(err.Error())}),
		)
	}
	if resp == nil {
		releaseCapacity()
		endAttempt()
		return nil, errors.New("resp is nil")
	}
	resp.Body = &capacityReadCloser{ReadCloser: resp.Body, release: func() {
		releaseCapacity()
		endAttempt()
	}}

	_ = req.Body.Close()
	_ = c.Request.Body.Close()
	return resp, nil
}

// withStreamStartDeadline bounds how long a streamed attempt waits for its
// upstream's response headers. A provider that queues a request and answers
// with a 429 minutes later would otherwise hold the client for as long as
// RELAY_TIMEOUT allows, while the SSE pings keep both connections alive. The
// deadline cancels the attempt context with ErrStreamStartDeadline; disarm
// stops it once the headers are in, and reports false if it already fired.
// The context lives on for the body and ends with endAttempt.
func withStreamStartDeadline(parent context.Context, info *common.RelayInfo) (ctx context.Context, disarm func() bool, endAttempt func()) {
	if info == nil || !info.IsStream || info.StreamStartDeadline.IsZero() {
		return parent, func() bool { return true }, func() {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(time.Until(info.StreamStartDeadline), func() { cancel(common.ErrStreamStartDeadline) })
	return ctx, timer.Stop, func() {
		timer.Stop()
		cancel(context.Canceled)
	}
}

func streamStartDeadlineError(c *gin.Context) *types.LazuError {
	logger.LogError(c, "upstream sent no response headers before the stream start deadline")
	return types.NewError(
		common.ErrStreamStartDeadline,
		types.ErrorCodeUpstreamTimeout,
		types.ErrOptionWithHideErrMsg("upstream provider did not respond in time"),
	)
}

// Keep the lease while a stream or response body is being read, rather than
// releasing it as soon as the provider sends its headers.
type capacityReadCloser struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (body *capacityReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(body.release)
	return err
}
