package service

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/lazu-ai/lazu/internal/common"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

const modelLogQueueBytes = 64 << 20

type modelLogCapture struct {
	entry   ModelLogEntry
	info    relaycommon.RelayInfo
	request []byte
	payload *ModelLogPayload
	bytes   int64
}

var modelLogCaptureQueue = make(chan modelLogCapture, 512)
var modelLogCaptureOnce sync.Once
var modelLogCapturePending sync.WaitGroup
var modelLogCaptureMu sync.RWMutex
var modelLogCaptureBytes atomic.Int64

func boundedModelLogPayload(payload *ModelLogPayload) *ModelLogPayload {
	if payload == nil {
		return nil
	}
	result := *payload
	result.Metadata = make(map[string]interface{}, len(payload.Metadata)+3)
	for key, value := range payload.Metadata {
		result.Metadata[key] = value
	}
	// The byte-bounded queue rejects an entire exchange on overload. Never
	// remove a prefix or an intermediate SSE chunk from an admitted exchange.
	if len(payload.ResponseBody) <= modelLogQueueBytes {
		result.ResponseBody = append([]byte(nil), payload.ResponseBody...)
	}
	result.ResponseChunks = append([]string(nil), payload.ResponseChunks...)
	return &result
}
func enqueueModelLogCapture(task modelLogCapture) {
	task.bytes = int64(len(task.request))
	if task.payload != nil {
		task.bytes += int64(len(task.payload.ResponseBody))
		for _, chunk := range task.payload.ResponseChunks {
			task.bytes += int64(len(chunk))
		}
	}
	modelLogCaptureMu.RLock()
	defer modelLogCaptureMu.RUnlock()
	modelLogCaptureOnce.Do(func() {
		common.SafeGo("model-log-capture", func() {
			for capture := range modelLogCaptureQueue {
				func() {
					defer modelLogCapturePending.Done()
					defer modelLogCaptureBytes.Add(-capture.bytes)
					defer func() {
						if recovered := recover(); recovered != nil {
							common.IncrementOperationalCounter("model_log.capture.failed")
							common.SysError(fmt.Sprintf("model log capture failed: %v", recovered))
						}
					}()
					writeModelLogCapture(capture)
				}()
			}
		})
	})
	if modelLogCaptureBytes.Add(task.bytes) > modelLogQueueBytes {
		modelLogCaptureBytes.Add(-task.bytes)
		common.IncrementOperationalCounter("model_log.capture.dropped")
		return
	}
	modelLogCapturePending.Add(1)
	select {
	case modelLogCaptureQueue <- task:
	default:
		modelLogCapturePending.Done()
		modelLogCaptureBytes.Add(-task.bytes)
		common.IncrementOperationalCounter("model_log.capture.dropped")
	}
}
func writeModelLogCapture(task modelLogCapture) {
	request := redactLoggedImageData(task.request)
	if len(request) > modelLogBodyLimit {
		request = request[:modelLogBodyLimit]
	}
	task.entry.RequestBody, task.entry.RequestBodyEncoding = bytesToSafeString(request)
	if task.payload != nil {
		task.entry.Metadata = task.payload.Metadata
	}
	response := rebuildLoggedStreamResponseBody(&task.info, task.payload)
	response = redactLoggedImageData(response)
	task.entry.ResponseBody, task.entry.ResponseBodyEncoding = bytesToSafeString(response)
	// Store the reconstructed body once; raw SSE chunks are transient inputs.
	task.entry.ResponseChunks = nil
	if err := appendModelLogEntry(task.entry); err != nil {
		common.SysError("failed to append model log entry: " + err.Error())
	}
}
func drainModelLogCaptures() {
	modelLogCaptureMu.Lock()
	defer modelLogCaptureMu.Unlock()
	modelLogCapturePending.Wait()
}
