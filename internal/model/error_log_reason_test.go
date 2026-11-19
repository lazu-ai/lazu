package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Relay paths put the code into other as types.ErrorCode; the reason column
// must still carry it, or channel failures cannot be filtered or watched.
func TestRecordErrorLogKeepsTypedErrorCodeAsReason(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	previousRedis, previousQueue := common.RedisEnabled, common.RelayEventQueueEnabled
	common.RedisEnabled, common.RelayEventQueueEnabled = false, false
	t.Cleanup(func() { common.RedisEnabled, common.RelayEventQueueEnabled = previousRedis, previousQueue })

	for _, tc := range []struct {
		name string
		code any
		want string
	}{
		{"typed", types.ErrorCodeUpstreamBillingFailed, string(types.ErrorCodeUpstreamBillingFailed)},
		{"string", string(types.ErrorCodeUpstreamAuthFailed), string(types.ErrorCodeUpstreamAuthFailed)},
	} {
		name, code, want := tc.name, tc.code, tc.want
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			requestID := common.GenerateRequestID()
			c.Set(common.RequestIdKey, requestID)
			RecordErrorLog(c, 0, 7, "m", "", "upstream refused", 0, 0, false, "", map[string]interface{}{"error_code": code})
			var row Log
			if err := db.Where("request_id = ?", requestID).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.RejectionReason != want {
				t.Fatalf("rejection_reason = %q, want %q", row.RejectionReason, want)
			}
		})
	}
}
