package controller

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// agentLaneBusyHint is appended to a busy-provider failure on the agent lane.
// Agent clients render the error message verbatim and nothing else, so this is
// the only place a customer can be told what to do about it. It names the lane
// and the connect dialog, which they have, and not the channel behind it, which
// they do not.
const agentLaneBusyHint = " The agent lane for this model is busy; pin the model to the direct lane in the connect dialog for steadier results."

// annotateLaneFailure adds the agent-lane hint to a final error the customer is
// about to see. Only busy-style failures qualify: a bad request or a blocked
// prompt has nothing to do with the lane, and pointing at it would be advice to
// spend more money for no reason.
func annotateLaneFailure(c *gin.Context, lazuErr *types.LazuError) {
	if lazuErr == nil || c == nil {
		return
	}
	if common.GetContextKeyString(c, constant.ContextKeyUsingGroup) != model.ChannelLaneAgent {
		return
	}
	switch lazuErr.GetErrorCode() {
	case types.ErrorCodeUpstreamOverloaded,
		types.ErrorCodeUpstreamRateLimited,
		types.ErrorCodeUpstreamNetworkError,
		types.ErrorCodeUpstreamTimeout:
	default:
		return
	}
	lazuErr.AppendMessage(agentLaneBusyHint)
}
