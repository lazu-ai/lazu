package controller

import (
	"errors"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/requestpath"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func Playground(c *gin.Context) {
	var lazuErr *types.LazuError

	defer func() {
		if lazuErr != nil {
			httperr.WriteOpenAI(c, lazuErr)
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		lazuErr = types.NewError(errors.New("the playground does not accept access tokens"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
		return
	}

	// The playground always prices and routes on the stable lane.
	group := model.ChannelLaneDirect
	modelName := c.GetString("original_model")

	userId := c.GetInt("id")

	// Write user context to ensure unset-price policy is available.
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		lazuErr = types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		return
	}
	if model.GatewayLedgerEnabled() {
		snapshot, snapshotErr := requestpath.Current().PlaygroundIdentity(c.Request.Context(), userId)
		if snapshotErr != nil {
			lazuErr = types.NewError(snapshotErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
			return
		}
		c.Set(model.IdentitySnapshotContextKey, snapshot)
		common.SetContextKey(c, constant.ContextKeyProjectID, snapshot.Principal.ProjectID)
		common.SetContextKey(c, constant.ContextKeyPayerUserID, userId)
		common.SetContextKey(c, constant.ContextKeyActorUserID, userId)
		userCache = snapshot.Actor
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", group),
		Group:  group,
	}
	_ = middleware.SetupContextForToken(c, tempToken)
	_, lazuErr = getChannel(c, group, modelName, 0, pickSameChannel)
	if lazuErr != nil {
		return
	}
	//middleware.SetupContextForSelectedChannel(c, channel, playgroundRequest.Model)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())

	Relay(c, types.RelayFormatOpenAI)
}
