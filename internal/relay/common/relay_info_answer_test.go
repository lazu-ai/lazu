package common

import (
	"testing"
	"time"
)

// Reasoning chunks start the first-token clock but not the answer's; a retry
// re-arms both.
func TestMarkOutputChunkSeparatesFirstAnswer(t *testing.T) {
	start := time.Now().Add(-time.Second)
	info := &RelayInfo{StartTime: start}
	info.MarkOutputChunk(false)
	if info.OutputTTFT(start) <= 0 || info.AnswerTTFT(start) != 0 {
		t.Fatalf("after reasoning: ttft=%v answer=%v", info.OutputTTFT(start), info.AnswerTTFT(start))
	}
	time.Sleep(5 * time.Millisecond)
	info.MarkOutputChunk(true)
	if info.AnswerTTFT(start) <= info.OutputTTFT(start) {
		t.Fatalf("answer %v must come after first token %v", info.AnswerTTFT(start), info.OutputTTFT(start))
	}
	info.ResetFirstResponse()
	if !info.FirstAnswerTime.IsZero() || !info.FirstOutputTime.IsZero() {
		t.Fatal("retry must re-arm both first-token clocks")
	}
}
