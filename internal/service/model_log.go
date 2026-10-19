package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"

	"github.com/gin-gonic/gin"
)

// 北京时间 (UTC+8)
var beijingLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// nowBeijing 返回当前北京时间
func nowBeijing() time.Time {
	return time.Now().In(beijingLocation)
}

type ModelLogPayload struct {
	ResponseBody   []byte
	ResponseChunks []string
	Usage          any
	Error          string
	StatusCode     int
	IsStream       bool
	Metadata       map[string]any
}

type ModelLogEntry struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"request_id"`
	// SessionID groups multi-turn agent conversations. Same value as
	// the logs row, so `jq 'select(.session_id == "X")'` over a day of
	// jsonl reconstructs an entire conversation in order.
	SessionID      string `json:"session_id,omitempty"`
	SessionSource  string `json:"session_source,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
	ParentThreadID string `json:"parent_thread_id,omitempty"`
	RequestKind    string `json:"request_kind,omitempty"`
	UserID         int    `json:"user_id,omitempty"`
	ProjectID      int    `json:"project_id,omitempty"`
	PayerUserID    int    `json:"payer_user_id,omitempty"`
	TokenID        int    `json:"token_id,omitempty"`
	// Client attribution (parsed from User-Agent in the distributor
	// middleware). ClientUserAgent is the raw header value.
	ClientFramework string `json:"client_framework,omitempty"`
	ClientVersion   string `json:"client_version,omitempty"`
	ClientUserAgent string `json:"client_user_agent,omitempty"`
	// ClientIP is the real originating address resolved via
	// CF-Connecting-IP → X-Real-IP → X-Forwarded-For chain.
	ClientIP string `json:"client_ip,omitempty"`
	// ClientCountry is the ISO 3166-1 alpha-2 country code from
	// Cloudflare's CF-IPCountry header.
	ClientCountry string `json:"client_country,omitempty"`
	// RequestHost is the hostname the request was sent to.
	RequestHost          string          `json:"request_host,omitempty"`
	ChannelID            int             `json:"channel_id,omitempty"`
	ChannelType          int             `json:"channel_type,omitempty"`
	ChannelName          string          `json:"channel_name,omitempty"`
	ProviderAPIType      int             `json:"provider_api_type,omitempty"`
	ProviderFamily       string          `json:"provider_family,omitempty"`
	RelayMode            int             `json:"relay_mode"`
	RelayFormat          string          `json:"relay_format"`
	RequestPath          string          `json:"request_path,omitempty"`
	OriginModel          string          `json:"origin_model,omitempty"`
	UpstreamModel        string          `json:"upstream_model,omitempty"`
	RetryCount           int             `json:"retry_count,omitempty"`
	FallbackHit          bool            `json:"fallback_hit,omitempty"`
	Stream               bool            `json:"stream"`
	StreamInterrupted    bool            `json:"stream_interrupted,omitempty"`
	StatusCode           int             `json:"status_code,omitempty"`
	DurationMs           int64           `json:"duration_ms,omitempty"`
	FirstResponseLatency int64           `json:"first_response_latency_ms,omitempty"`
	RequestBody          string          `json:"request_body,omitempty"`
	RequestBodyEncoding  string          `json:"request_body_encoding,omitempty"`
	RequestBodyBytes     int             `json:"request_body_bytes,omitempty"`
	RequestBodyTruncated bool            `json:"request_body_truncated,omitempty"`
	ResponseBody         string          `json:"response_body,omitempty"`
	ResponseBodyEncoding string          `json:"response_body_encoding,omitempty"`
	ResponseChunks       []string        `json:"response_chunks,omitempty"`
	Usage                json.RawMessage `json:"usage,omitempty"`
	Error                string          `json:"error,omitempty"`
	Metadata             map[string]any  `json:"metadata,omitempty"`
}

const maxModelLogFileSize = 128 << 20 // 128 MiB

// modelLogBodyLimit caps one captured request body. Agents resend whole
// histories, but a body past 8 MiB is almost always pasted binary or an
// abusive payload; the entry keeps its head and records the real size.
const modelLogBodyLimit = 8 << 20

// modelLogDiskPause is the disk usage at which full-text capture stops. The
// relay itself only refuses traffic at the (higher) performance threshold.
const modelLogDiskPause = 85.0

func modelLogPausedForDisk() bool {
	return common.GetSystemStatus().DiskUsage >= modelLogDiskPause
}

var (
	modelLogWriterOnce sync.Once
	modelLogWriter     *RotatingLogWriter
)

func AttachModelLogPayload(c *gin.Context, payload *ModelLogPayload) {
	if c == nil || payload == nil {
		return
	}
	*payload = *boundedModelLogPayload(payload)
	common.SetContextKey(c, constant.ContextKeyModelLogPayload, payload)
}

func markModelLogWritten(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyModelLogWritten, true)
}

func isModelLogWritten(c *gin.Context) bool {
	return common.GetContextKeyBool(c, constant.ContextKeyModelLogWritten)
}

func fetchRelayInfo(c *gin.Context) (*relaycommon.RelayInfo, bool) {
	value, ok := common.GetContextKey(c, constant.ContextKeyRelayInfo)
	if !ok || value == nil {
		return nil, false
	}
	info, ok := value.(*relaycommon.RelayInfo)
	return info, ok && info != nil
}

func bytesToSafeString(data []byte) (string, string) {
	if len(data) == 0 {
		return "", ""
	}
	if utf8.Valid(data) {
		return string(data), "plain"
	}
	return base64.StdEncoding.EncodeToString(data), "base64"
}

func usageToRawMessage(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to marshal usage for model log: %v", err))
		return nil
	}
	return data
}

func RecordModelExchange(c *gin.Context, payload *ModelLogPayload) {
	if c != nil && payload != nil {
		var usage *dto.Usage
		switch v := payload.Usage.(type) {
		case *dto.Usage:
			usage = v
		case dto.Usage:
			usage = &v
		}
		// A reported zero is an answer too; only a usage with no reasoning
		// detail at all leaves the count unknown.
		if usage != nil && (usage.OutputTokensDetails != nil || usage.CompletionTokenDetails.ReasoningTokens > 0) {
			c.Set("reported_reasoning_tokens", ReportedReasoningTokens(usage))
		}
	}
	if c == nil || !common.ModelLogEnabled || isModelLogWritten(c) {
		return
	}
	// Full-text capture is the one writer that can fill the disk. Stop it well
	// before the relay's own disk guard would start rejecting traffic.
	if modelLogPausedForDisk() {
		return
	}
	info, ok := fetchRelayInfo(c)
	if !ok {
		// Distributor rejections occur before RelayInfo exists.
		info = &relaycommon.RelayInfo{
			UserId:          common.GetContextKeyInt(c, constant.ContextKeyUserId),
			ActorUserID:     common.GetContextKeyInt(c, constant.ContextKeyActorUserID),
			ProjectID:       common.GetContextKeyInt(c, constant.ContextKeyProjectID),
			PayerUserID:     common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
			TokenId:         common.GetContextKeyInt(c, constant.ContextKeyTokenId),
			OriginModelName: common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
			StartTime:       common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime),
		}
		if c.Request != nil && c.Request.URL != nil {
			info.RequestURLPath = c.Request.URL.Path
		}
		if info.StartTime.IsZero() {
			info.StartTime = time.Now()
		}
	}

	captureTimer := common.StartRelayPhase(c, common.RelayPhaseCapture)
	defer captureTimer.Finish()
	requestBody, _ := common.GetRequestBody(c)
	requestBodyBytes := len(requestBody)

	entry := ModelLogEntry{
		Timestamp:      nowBeijing().Format(time.RFC3339Nano),
		RequestID:      c.GetString(common.RequestIdKey),
		SessionID:      common.GetContextKeyString(c, constant.ContextKeySessionID),
		SessionSource:  common.GetContextKeyString(c, constant.ContextKeySessionSource),
		ThreadID:       common.GetContextKeyString(c, constant.ContextKeyThreadID),
		ParentThreadID: common.GetContextKeyString(c, constant.ContextKeyParentThreadID),
		RequestKind:    common.GetContextKeyString(c, constant.ContextKeyRequestKind),
		UserID:         info.ActorID(), ProjectID: info.ProjectID, PayerUserID: info.BillingUserID(),
		TokenID:              info.TokenId,
		ClientFramework:      common.GetContextKeyString(c, constant.ContextKeyClientFramework),
		ClientVersion:        common.GetContextKeyString(c, constant.ContextKeyClientVersion),
		ClientUserAgent:      common.GetContextKeyString(c, constant.ContextKeyClientUserAgent),
		ClientIP:             common.GetContextKeyString(c, constant.ContextKeyRealClientIP),
		ClientCountry:        common.GetContextKeyString(c, constant.ContextKeyClientCountry),
		RequestHost:          model.RequestHostOf(c),
		RelayMode:            info.RelayMode,
		RelayFormat:          string(info.RelayFormat),
		RequestPath:          info.RequestURLPath,
		OriginModel:          info.OriginModelName,
		Stream:               payload != nil && payload.IsStream,
		RequestBodyBytes:     requestBodyBytes,
		RequestBodyTruncated: requestBodyBytes > modelLogBodyLimit,
		DurationMs:           time.Since(info.StartTime).Milliseconds(),
	}

	if info.ChannelMeta != nil {
		entry.ChannelID = info.ChannelMeta.ChannelId
		entry.ChannelType = info.ChannelMeta.ChannelType
		entry.UpstreamModel = info.ChannelMeta.UpstreamModelName
		entry.ProviderAPIType = info.ChannelMeta.ApiType
		entry.ProviderFamily = string(providercap.ProtocolFamilyOf(info.ChannelMeta.ApiType))
	}
	entry.ChannelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
	useChannels := c.GetStringSlice("use_channel")
	if len(useChannels) > 1 {
		entry.RetryCount = len(useChannels) - 1
		// A retry on the same channel is not a fallback: only a request
		// that moved to another channel hit one.
		entry.FallbackHit = DistinctChannelCount(useChannels) > 1
	}
	if info.HasSendResponse() {
		latencyMs := info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
		if latencyMs > 0 {
			entry.FirstResponseLatency = latencyMs
		}
	}

	if payload != nil {
		if payload.StatusCode != 0 {
			entry.StatusCode = payload.StatusCode
		} else if c.Writer != nil {
			entry.StatusCode = c.Writer.Status()
		}
		if payload.Usage != nil {
			entry.Usage = usageToRawMessage(payload.Usage)
		}
		if payload.Error != "" {
			entry.Error = payload.Error
		}
		if len(payload.Metadata) > 0 {
			entry.Metadata = payload.Metadata
		}
	}
	if entry.StatusCode == 0 && c.Writer != nil {
		entry.StatusCode = c.Writer.Status()
	}
	if entry.Stream {
		success := entry.StatusCode >= 200 && entry.StatusCode < 300 && entry.Error == ""
		entry.StreamInterrupted = !success
	}

	// All JSON reconstruction, image redaction and encoding run on a bounded
	// worker. Take immutable snapshots; neither Gin nor RelayInfo escapes.
	infoSnapshot := relaycommon.RelayInfo{RelayFormat: info.RelayFormat, RelayMode: info.RelayMode, OriginModelName: info.OriginModelName}
	if info.ChannelMeta != nil {
		meta := *info.ChannelMeta
		infoSnapshot.ChannelMeta = &meta
	}
	payloadSnapshot := boundedModelLogPayload(payload)
	enqueueModelLogCapture(modelLogCapture{entry: entry, info: infoSnapshot, request: requestBody, payload: payloadSnapshot})

	markModelLogWritten(c)
}

func appendModelLogEntry(entry ModelLogEntry) error {
	if common.ModelLogPath == "" {
		return fmt.Errorf("model log path is empty")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	writer := getModelLogWriter(common.ModelLogPath)
	return writer.Append(data)
}

// RotatingLogWriter 是按日期轮转的日志写入器
// 支持按日期自动切割，单文件大小限制
type RotatingLogWriter struct {
	baseDir      string
	baseName     string
	ext          string
	maxFileSize  int64
	enabledFunc  func() bool
	mu           sync.Mutex
	currentDate  string
	currentIndex int
	currentSize  int64
	file         *os.File
	lastSync     time.Time
}

// NewRotatingLogWriter 创建新的轮转日志写入器
func NewRotatingLogWriter(basePath string, maxSize int64, enabledFunc func() bool) *RotatingLogWriter {
	dir := filepath.Dir(basePath)
	name := filepath.Base(basePath)
	ext := filepath.Ext(name)
	baseName := strings.TrimSuffix(name, ext)
	if baseName == "" {
		baseName = "log"
	}
	return &RotatingLogWriter{
		baseDir:     dir,
		baseName:    baseName,
		ext:         ext,
		maxFileSize: maxSize,
		enabledFunc: enabledFunc,
	}
}

func getModelLogWriter(basePath string) *RotatingLogWriter {
	modelLogWriterOnce.Do(func() {
		modelLogWriter = NewRotatingLogWriter(basePath, maxModelLogFileSize, func() bool {
			return common.ModelLogEnabled && common.ModelLogPath != ""
		})
	})
	return modelLogWriter
}

// CloseModelLogWriter 关闭模型日志写入器，在程序优雅退出时调用
// 如果功能未启用（ModelLogEnabled=false），writer 不会被初始化，返回 nil 表示无需关闭
func CloseModelLogWriter() error {
	drainModelLogCaptures()
	if modelLogWriter == nil {
		// 功能未启用或从未写入过日志，无需关闭
		return nil
	}
	return modelLogWriter.Close()
}

func (w *RotatingLogWriter) Append(data []byte) error {
	if len(data) == 0 || (w.enabledFunc != nil && !w.enabledFunc()) {
		return nil
	}
	now := nowBeijing()
	date := now.Format("2006-01-02")

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.ensureFile(date, len(data)); err != nil {
		return err
	}

	n, err := w.file.Write(data)
	if err != nil {
		return err
	}
	w.currentSize += int64(n)

	// Reduce risk of data loss by syncing periodically.
	if now.Sub(w.lastSync) >= 3*time.Second {
		if err := w.file.Sync(); err != nil {
			return err
		}
		w.lastSync = now
	}
	return nil
}

// Close 关闭日志写入器，确保数据完整写入磁盘
func (w *RotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}

	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		w.file = nil
		return err
	}

	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingLogWriter) ensureFile(date string, nextWriteLen int) error {
	if w.file == nil || w.currentDate != date {
		if err := w.rotate(date, 1); err != nil {
			return err
		}
	}

	maxSize := w.maxFileSize
	if maxSize <= 0 {
		maxSize = maxModelLogFileSize
	}
	if w.currentSize+int64(nextWriteLen) > maxSize {
		return w.rotate(date, w.currentIndex+1)
	}
	return nil
}

func (w *RotatingLogWriter) rotate(date string, startIndex int) error {
	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}

	w.currentDate = date
	w.currentIndex = startIndex

	maxSize := w.maxFileSize
	if maxSize <= 0 {
		maxSize = maxModelLogFileSize
	}

	for {
		path := w.filePath(date, w.currentIndex)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}

		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}

		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return err
		}

		if info.Size() >= maxSize {
			_ = file.Close()
			w.currentIndex++
			continue
		}

		w.file = file
		w.currentSize = info.Size()
		w.lastSync = time.Now()
		break
	}
	return nil
}

func (w *RotatingLogWriter) filePath(date string, index int) string {
	filename := fmt.Sprintf("%s-%s", w.baseName, date)
	if index > 1 {
		filename = fmt.Sprintf("%s-%s-%d", w.baseName, date, index)
	}
	if w.ext != "" {
		filename += w.ext
	}
	return filepath.Join(w.baseDir, filename)
}

// RequestDurationMS shares the distributor clock with error receipts.
func RequestDurationMS(c *gin.Context) int64 {
	start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if start.IsZero() {
		return 0
	}
	return time.Since(start).Milliseconds()
}

// CaptureIncompleteModelResponse retains a bounded diagnostic fragment, not a
// completed response or provider usage. The terminal hook supplies final status.
func CaptureIncompleteModelResponse(c *gin.Context, body []byte, status int) {
	if !common.ModelLogEnabled || isModelLogWritten(c) {
		return
	}
	const limit = 64 * 1024
	n := len(body)
	if n > limit {
		body = body[:limit]
	}
	AttachModelLogPayload(c, &ModelLogPayload{ResponseBody: append([]byte(nil), body...), Metadata: map[string]any{
		"response_complete": false, "response_truncated": n > limit,
		"upstream_status_code": status, "response_bytes_received": n,
		"response_channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
	}})
}

// RecordUnfinishedModelExchange reuses the existing exactly-once scheduling
// guard. Middleware invokes it too, covering requests that never enter Relay.
func RecordUnfinishedModelExchange(c *gin.Context) {
	if !common.ModelLogEnabled || isModelLogWritten(c) {
		return
	}
	payload := extractModelLogPayload(c)
	if payload == nil {
		payload = &ModelLogPayload{}
	}
	payload.StatusCode = c.Writer.Status()
	payload.IsStream = common.GetContextKeyBool(c, constant.ContextKeyIsStream)
	payload.Error = common.MaskSensitiveInfo(common.GetContextKeyString(c, constant.ContextKeyRequestErrorMessage))
	if payload.Error == "" {
		payload.Error = common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
	}
	if payload.Metadata == nil {
		payload.Metadata = make(map[string]any)
	}
	payload.Metadata["terminal_fallback"] = true
	payload.Metadata["response_complete"] = false
	payload.Metadata["any_provider_attempted"] = common.GetContextKeyBool(c, constant.ContextKeyAnyProviderAttempted)
	payload.Metadata["usage_unavailable"] = payload.Usage == nil
	RecordModelExchange(c, payload)
}
