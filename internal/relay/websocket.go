package relay

import (
	"fmt"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func WssHelper(c *gin.Context, info *relaycommon.RelayInfo) (lazuErr *types.LazuError) {
	info.InitChannelMeta(c)

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	//var requestBody io.Reader
	//firstWssRequest, _ := c.Get("first_wss_request")
	//requestBody = bytes.NewBuffer(firstWssRequest.([]byte))

	statusCodeMappingStr := c.GetString("status_code_mapping")
	resp, err := adaptor.DoRequest(c, info, nil)
	if err != nil {
		return types.NewError(err, types.ErrorCodeAPIConnectionError)
	}

	if resp != nil {
		info.TargetWs = resp.(*websocket.Conn)
		defer info.TargetWs.Close()
	}

	usage, lazuErr := adaptor.DoResponse(c, nil, info)
	realtimeUsage, _ := usage.(*dto.RealtimeUsage)
	if lazuErr != nil {
		// The final envelope is captured while settling, before the controller
		// receives this error. Carry the actual terminal state into that snapshot.
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(lazuErr.GetErrorCode()))
		common.SetContextKey(c, constant.ContextKeyRequestErrorType, "realtime_error")
	}
	// Accepted usage must settle even when the websocket terminates with a
	// provider/read/write error or client cancellation.
	service.PostWssConsumeQuota(c, info, info.UpstreamModelName, realtimeUsage, "")
	if lazuErr != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(lazuErr, statusCodeMappingStr)
		return lazuErr
	}
	return nil
}
