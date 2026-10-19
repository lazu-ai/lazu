package service

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestPR380ModelLogRetainsCompleteLargeResponseAndChunks(t *testing.T) {
	body := []byte(strings.Repeat("complete", 40000))
	chunks := []string{"first", strings.Repeat("middle", 50000), "last"}
	got := boundedModelLogPayload(&ModelLogPayload{ResponseBody: body, ResponseChunks: chunks})
	if !bytes.Equal(got.ResponseBody, body) || !reflect.DeepEqual(got.ResponseChunks, chunks) {
		t.Fatalf("capture silently changed response: body=%d/%d chunks=%d/%d", len(got.ResponseBody), len(body), len(got.ResponseChunks), len(chunks))
	}
}

func TestPR380ModelLogWritesCompleteLargeResponse(t *testing.T) {
	resetModelLogWriterForTest(t)
	oldEnabled, oldPath := common.ModelLogEnabled, common.ModelLogPath
	dir := t.TempDir()
	common.ModelLogEnabled, common.ModelLogPath = true, filepath.Join(dir, "model-responses.jsonl")
	t.Cleanup(func() {
		_ = CloseModelLogWriter()
		common.ModelLogEnabled, common.ModelLogPath = oldEnabled, oldPath
		resetModelLogWriterForTest(t)
	})
	body := []byte(`{"text":"` + strings.Repeat("response", 40000) + `"}`)
	writeModelLogCapture(modelLogCapture{entry: ModelLogEntry{RequestID: "pr380-complete"}, payload: &ModelLogPayload{ResponseBody: body}})
	entry := readWrittenModelLogEntry(t, dir)
	if entry.ResponseBody != string(body) {
		t.Fatalf("written response silently truncated: %d/%d", len(entry.ResponseBody), len(body))
	}
}

func TestPR380ModelLogByteOverloadDropsWholeExchange(t *testing.T) {
	drainModelLogCaptures()
	before := common.SnapshotHotPathMetrics().Counters["model_log.capture.dropped"]
	payload := &ModelLogPayload{ResponseBody: []byte(strings.Repeat("x", modelLogQueueBytes+1))}
	enqueueModelLogCapture(modelLogCapture{entry: ModelLogEntry{RequestID: "whole-record-overload"}, payload: boundedModelLogPayload(payload)})
	drainModelLogCaptures()
	if common.SnapshotHotPathMetrics().Counters["model_log.capture.dropped"] != before+1 || modelLogCaptureBytes.Load() != 0 {
		t.Fatal("byte overload was not accounted as an entire dropped exchange")
	}
}
