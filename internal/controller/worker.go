package controller

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/service"
)

// TriggerChannelTest triggers an immediate channel connectivity test. Admin only.
func TriggerChannelTest(c *gin.Context) {
	uid := c.GetInt("id")
	common.SysLog(fmt.Sprintf("manual channel test triggered by user=%d", uid))

	wsm := service.GetWorkerStatusManager()

	if wsm.IsRunning("channel-test") {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Channel test already running",
			"status":  "running",
		})
		return
	}

	wsm.StartWorker("channel-test")

	// Run channel test in background.
	common.SafeGo("manual-channel-test", func() {
		AutomaticallyTestChannels()
		wsm.CompleteWorker("channel-test", "Channel test completed", nil)
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Channel test task started in background",
		"status":  "running",
	})
}
