package service

import (
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

func TestSessionAffinityAdminInfoExplainsRejectedRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	if info := sessionAffinityAdminInfo(c); info != nil {
		t.Fatalf("a request outside affinity has admin info: %v", info)
	}
	common.SetContextKey(c, constant.ContextKeySessionAffinityResult, "rejected")
	common.SetContextKey(c, constant.ContextKeySessionAffinityChannel, 42)
	common.SetContextKey(c, constant.ContextKeySessionAffinityReason, "channel_unavailable")
	want := map[string]interface{}{"result": "rejected", "previous_channel_id": 42, "reason": "channel_unavailable"}
	if got := sessionAffinityAdminInfo(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("admin info = %v, want %v", got, want)
	}
}
