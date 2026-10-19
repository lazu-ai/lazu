package service

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

type protocolTokenShadowTask struct {
	requestID string
	meta      *types.TokenCountMeta
	model     string
	format    types.RelayFormat
	stream    bool
	admission int
	bytes     int64
}

var tokenShadowQueue = make(chan protocolTokenShadowTask, 256)
var tokenShadowOnce sync.Once
var tokenShadowQueuedBytes atomic.Int64

const tokenShadowQueueBytes = 64 << 20

// EnqueueProtocolTokenShadow retains immutable text metadata only. Gin contexts
// and mutable RelayInfo objects never escape into the tokenizer worker.
func EnqueueProtocolTokenShadow(c *gin.Context) {
	if c.Request != nil {
		if state := common.InternalRelayCallStateFromContext(c.Request.Context()); state != nil && state.Preflight {
			return
		}
	}
	value, exists := c.Get("protocol_token_shadow_meta")
	if !exists {
		return
	}
	meta, ok := value.(*types.TokenCountMeta)
	if !ok || meta == nil {
		return
	}
	tokenShadowOnce.Do(func() {
		common.SafeGo("protocol-token-shadow", func() {
			memo := newProtocolTokenShadowMemo()
			for task := range tokenShadowQueue {
				func() {
					defer tokenShadowWork.Done()
					defer tokenShadowQueuedBytes.Add(-task.bytes)
					defer func() {
						if recovered := recover(); recovered != nil {
							common.IncrementOperationalCounter("protocol.token_shadow.failed")
							common.SysError(fmt.Sprintf("token shadow failed: %v", recovered))
						}
					}()
					runProtocolTokenShadowWithMemo(task, memo)
				}()
			}
		})
	})
	format, _ := c.Get("protocol_token_shadow_format")
	typedFormat, _ := format.(types.RelayFormat)
	task := protocolTokenShadowTask{requestID: c.GetString(common.RequestIdKey), meta: meta, model: c.GetString("protocol_token_shadow_model"), format: typedFormat, stream: common.GetContextKeyBool(c, constant.ContextKeyIsStream), admission: common.GetContextKeyInt(c, constant.ContextKeyProtocolAdmissionTokens)}
	task.bytes = int64(len(meta.CombineText))
	if tokenShadowQueuedBytes.Add(task.bytes) > tokenShadowQueueBytes {
		tokenShadowQueuedBytes.Add(-task.bytes)
		common.IncrementOperationalCounter("protocol.token_shadow.dropped")
		return
	}
	tokenShadowWork.Add(1)
	select {
	case tokenShadowQueue <- task:
	default:
		tokenShadowWork.Done()
		tokenShadowQueuedBytes.Add(-task.bytes)
		common.IncrementOperationalCounter("protocol.token_shadow.dropped")
	}
}
func runProtocolTokenShadow(task protocolTokenShadowTask) {
	runProtocolTokenShadowWithMemo(task, nil)
}

func runProtocolTokenShadowWithMemo(task protocolTokenShadowTask, memo *protocolTokenShadowMemo) {
	c := &gin.Context{}
	info := &relaycommon.RelayInfo{OriginModelName: task.model, RelayFormat: task.format, IsStream: task.stream}
	tokens, err := memo.count(task, func() (int, error) { return countRequestTokens(c, task.meta, info) })
	fields := map[string]interface{}{}
	if err != nil {
		fields["protocol_prompt_token_shadow_skip"] = "shadow_estimate_failed"
	} else {
		fields["protocol_local_prompt_tokens"] = tokens
		fields["protocol_admission_token_delta"] = tokens - task.admission
	}
	patchRequestDiagnostics(task.requestID, fields)
}

func RecordRelayTimingDiagnostics(c *gin.Context, fields map[string]interface{}) {
	if !model.HasRequestLogTarget(c) {
		return
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" || len(fields) == 0 {
		return
	}
	enqueueDiagnosticPatch(requestID, fields)
}

type diagnosticPatch struct {
	requestID string
	fields    map[string]interface{}
}

var diagnosticsQueue = make(chan diagnosticPatch, 512)
var diagnosticsOnce sync.Once

func enqueueDiagnosticPatch(requestID string, fields map[string]interface{}) {
	diagnosticsOnce.Do(func() {
		for i := 0; i < 8; i++ {
			common.SafeGo("relay-diagnostics", func() {
				for patch := range diagnosticsQueue {
					func() {
						defer diagnosticWork.Done()
						patchRequestDiagnostics(patch.requestID, patch.fields)
					}()
				}
			})
		}
	})
	diagnosticWork.Add(1)
	select {
	case diagnosticsQueue <- diagnosticPatch{requestID, fields}:
	default:
		diagnosticWork.Done()
		common.IncrementOperationalCounter("relay.diagnostics.dropped")
	}
}
func patchRequestDiagnostics(requestID string, fields map[string]interface{}) {
	if requestID == "" {
		return
	}
	if common.RelayEventQueueEnabled && common.RedisEnabled && common.RDB != nil {
		if err := model.PublishRelayEvent(model.RelayEventTypeRequestDiagnostics, model.RequestDiagnosticsEvent{RequestID: requestID, Fields: fields}); err != nil {
			common.IncrementOperationalCounter("relay.diagnostics.failed")
		}
		return
	}
	if model.GatewayLedgerEnabled() {
		common.IncrementOperationalCounter("relay.diagnostics.failed")
		return
	}
	// Embedded local fixtures retain direct diagnostics; a hosted gateway
	// never falls back to a telemetry SQL writer.
	// A delayed consumer may not have written the event yet. The bounded
	// diagnostics queue applies backpressure only to itself, never the relay.
	for attempt := 0; attempt < 8; attempt++ {
		err := model.UpdateRequestLogDiagnostics(requestID, fields)
		if err == nil {
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.IncrementOperationalCounter("relay.diagnostics.failed")
			return
		}
		time.Sleep(time.Duration(1<<attempt) * 10 * time.Millisecond)
	}
	common.IncrementOperationalCounter("relay.diagnostics.expired")
}
