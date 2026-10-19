package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func InternalIdentitySnapshot(c *gin.Context) common.InternalRelayIdentity {
	identity := common.InternalRelayIdentity{TokenID: c.GetInt("token_id"), UserID: c.GetInt("authenticated_token_owner_id"), ProjectID: common.GetContextKeyInt(c, constant.ContextKeyProjectID), ActorUserID: common.GetContextKeyInt(c, constant.ContextKeyActorUserID), PayerUserID: common.GetContextKeyInt(c, constant.ContextKeyPayerUserID), ClientIP: common.GetRealClientIP(c), RequestHost: c.Request.Host, SessionID: common.GetContextKeyString(c, constant.ContextKeySessionID), SessionSource: common.GetContextKeyString(c, constant.ContextKeySessionSource), AgentID: common.GetContextKeyString(c, constant.ContextKeyAgentID), ParentRequestID: c.GetString(common.RequestIdKey), ForceLane: model.ChannelLaneDirect}
	if credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential); ok && credential != nil {
		identity.EphemeralID = credential.ID
	}
	return identity
}
