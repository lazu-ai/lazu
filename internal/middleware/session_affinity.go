package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
)

// Session affinity results, as written to the log's admin details.
const (
	sessionAffinityNone     = "none"
	sessionAffinityHit      = "hit"
	sessionAffinityRejected = "rejected"
)

// sessionAffinityKeyFor returns the affinity record's key when this request
// takes part: the site switch is on and the request carries an agent's own
// session id. The per-Key daily fallback is not a session; pinning it would
// hold every request a Key makes in a day to one channel.
func sessionAffinityKeyFor(c *gin.Context, reqCtx *relayrouter.RequestContext) string {
	if !common.SessionChannelAffinityEnabled || c == nil || reqCtx == nil {
		return ""
	}
	if reqCtx.UserID <= 0 || reqCtx.SessionID == "" || reqCtx.ResolvedModel == "" {
		return ""
	}
	switch common.GetContextKeyString(c, constant.ContextKeySessionSource) {
	case "", SessionSourceToken:
		return ""
	}
	if c.Request != nil && c.Request.URL.Path == "/v1/decisions" {
		return ""
	}
	return relayrouter.SessionAffinityKey(reqCtx.UserID, reqCtx.SessionID, reqCtx.ResolvedModel)
}

// routeBySessionAffinity returns the decision to reuse the session's previous
// channel, or nil to let the router choose. Either way it leaves the request
// marked for the record to be written once it succeeds.
func routeBySessionAffinity(c *gin.Context, reqCtx *relayrouter.RequestContext) *relayrouter.Decision {
	key := sessionAffinityKeyFor(c, reqCtx)
	clearSessionAffinityContext(c)
	if key == "" {
		return nil
	}
	common.SetContextKey(c, constant.ContextKeySessionAffinityKey, key)
	common.SetContextKey(c, constant.ContextKeySessionAffinityResult, sessionAffinityNone)
	record, found, err := relayrouter.LookupSessionAffinity(c.Request.Context(), key)
	if err != nil {
		logger.LogWarn(c, fmt.Sprintf("session affinity lookup failed: %v", err))
		common.SetContextKey(c, constant.ContextKeySessionAffinityReason, relayrouter.AffinitySkipLookupFailed)
		return nil
	}
	if !found {
		return nil
	}
	common.SetContextKey(c, constant.ContextKeySessionAffinityRecord, record)
	common.SetContextKey(c, constant.ContextKeySessionAffinityChannel, record.ChannelID)
	decision, reason := relayrouter.SelectByAffinity(reqCtx, record)
	if decision == nil {
		common.SetContextKey(c, constant.ContextKeySessionAffinityResult, sessionAffinityRejected)
		common.SetContextKey(c, constant.ContextKeySessionAffinityReason, reason)
		return nil
	}
	common.SetContextKey(c, constant.ContextKeySessionAffinityResult, sessionAffinityHit)
	return decision
}

// clearSessionAffinityContext forgets what an earlier entry of a models[]
// fallback chain looked up, so only the model that is served is recorded.
func clearSessionAffinityContext(c *gin.Context) {
	if common.GetContextKeyString(c, constant.ContextKeySessionAffinityKey) == "" {
		return
	}
	common.SetContextKey(c, constant.ContextKeySessionAffinityKey, "")
	common.SetContextKey(c, constant.ContextKeySessionAffinityRecord, relayrouter.AffinityRecord{})
	common.SetContextKey(c, constant.ContextKeySessionAffinityResult, "")
	common.SetContextKey(c, constant.ContextKeySessionAffinityChannel, 0)
	common.SetContextKey(c, constant.ContextKeySessionAffinityReason, "")
}

// sessionAffinityPinnedKey is the upstream key a hit must go out on: the one
// the session's previous turn used on this channel. ok is false when there is
// none to pin, and the channel rotates its keys as usual.
func sessionAffinityPinnedKey(c *gin.Context, channel *model.Channel) (relayrouter.AffinityRecord, bool) {
	if channel == nil || common.GetContextKeyString(c, constant.ContextKeySessionAffinityResult) != sessionAffinityHit {
		return relayrouter.AffinityRecord{}, false
	}
	record, ok := common.GetContextKeyType[relayrouter.AffinityRecord](c, constant.ContextKeySessionAffinityRecord)
	if !ok || record.ChannelID != channel.Id {
		return relayrouter.AffinityRecord{}, false
	}
	return record, true
}

// recordSessionAffinity stores the channel and key that answered the request,
// after it has answered. It runs after retries, so a request that had to move
// to another channel or key leaves the session on the one that worked. Only a
// success the channel-health verdict would count as one moves the record.
func recordSessionAffinity(c *gin.Context) {
	key := common.GetContextKeyString(c, constant.ContextKeySessionAffinityKey)
	if key == "" || !common.SessionChannelAffinityEnabled {
		return
	}
	statusCode := c.Writer.Status()
	errorCode := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
	if c.GetBool(manualRoutingOutcomeOverrideKey) {
		statusCode = c.GetInt(manualRoutingOutcomeStatusKey)
		errorCode = c.GetString(manualRoutingOutcomeErrorCodeKey)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) || errorCode != "" ||
		statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return
	}
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	group := model.NormalizeChannelLane(c.GetString("final_group"))
	if channelID <= 0 || group == "" {
		return
	}
	record := relayrouter.AffinityRecord{
		ChannelID: channelID,
		Group:     group,
		KeyHash:   relayrouter.AffinityKeyHash(common.GetContextKeyString(c, constant.ContextKeyChannelKey)),
	}
	if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		record.KeyIndex = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	}
	if common.GetContextKeyString(c, constant.ContextKeySessionAffinityResult) == sessionAffinityHit {
		// The lookup already renewed an unchanged record.
		if previous, ok := common.GetContextKeyType[relayrouter.AffinityRecord](c, constant.ContextKeySessionAffinityRecord); ok && previous == record {
			return
		}
	}
	if err := relayrouter.StoreSessionAffinity(key, record); err != nil {
		logger.LogWarn(c, fmt.Sprintf("session affinity write failed: %v", err))
	}
}
