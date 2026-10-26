package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/requestpath"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func validUserInfo(username string, role int) bool {
	// check username is empty
	if strings.TrimSpace(username) == "" {
		return false
	}
	if !common.IsValidateRole(role) {
		return false
	}
	return true
}

func resolveSessionUserID(c *gin.Context) (int, error) {
	if userID := c.GetInt("id"); userID > 0 {
		return userID, nil
	}
	session := sessions.Default(c)
	if session == nil {
		return 0, nil
	}
	userID := common.ParseIntFromAny(session.Get("id"))
	if userID <= 0 {
		return 0, nil
	}
	if model.CoreDB() != nil {
		current, err := sessionVersionCurrent(session, userID)
		if err != nil {
			return 0, err
		}
		if !current {
			return 0, nil
		}
	}
	return userID, nil
}

func supportsTokenIDHeader(path string) bool {
	switch {
	case strings.HasPrefix(path, "/v1/images/generations"):
		return true
	case strings.HasPrefix(path, "/v1/videos"):
		return true
	default:
		return false
	}
}

func resolveTokenByIDHeader(c *gin.Context) (*model.Token, bool, error) {
	// Extensions may authenticate their own signed server-side calls.
	if token, handled, err := resolveExtensionToken(c); handled {
		return token, true, err
	}
	if !supportsTokenIDHeader(c.Request.URL.Path) {
		return nil, false, nil
	}
	tokenIDRaw := strings.TrimSpace(c.GetHeader("X-Token-Id"))
	if tokenIDRaw == "" {
		return nil, false, nil
	}
	tokenID, err := strconv.Atoi(tokenIDRaw)
	if err != nil || tokenID <= 0 {
		return nil, true, fmt.Errorf("invalid X-Token-Id")
	}
	userID, err := resolveSessionUserID(c)
	if err != nil {
		return nil, true, err
	}
	if userID <= 0 {
		return nil, true, fmt.Errorf("X-Token-Id requires authenticated session")
	}
	tokenByID, err := requestpath.Current().SessionToken(tokenID, userID)
	if err != nil {
		return nil, true, err
	}
	if tokenByID.IsInternal() && !internalProxyVerified(c) {
		return nil, true, fmt.Errorf("internal token requires a trusted relay request")
	}
	if err := model.ValidateSessionTokenState(tokenByID, tokenByID.DisplayKey()); err != nil {
		return tokenByID, true, err
	}
	return tokenByID, true, nil
}

func authHelper(c *gin.Context, minRole int) {
	session := sessions.Default(c)
	username := session.Get("username")
	role := session.Get("role")
	id := session.Get("id")
	status := session.Get("status")
	useAccessToken := false
	if username == nil {
		// Check access token
		accessToken := c.Request.Header.Get("Authorization")
		if accessToken == "" {
			httperr.Abort(c, types.Err(types.ErrorCodeSessionRequired))
			return
		}
		user := model.ValidateAccessToken(accessToken)
		if user != nil && user.Username != "" {
			if !validUserInfo(user.Username, user.Role) {
				httperr.Abort(c, types.Err(types.ErrorCodeSessionInvalid))
				return
			}
			// Token is valid
			username = user.Username
			role = user.Role
			id = user.Id
			status = user.Status
			useAccessToken = true
		} else {
			httperr.Abort(c, types.Err(types.ErrorCodeAccessTokenInvalid))
			return
		}
	}
	// get header X-Lazu-User
	apiUserIdStr := c.Request.Header.Get("X-Lazu-User")
	if apiUserIdStr == "" {
		httperr.Abort(c, types.Err(types.ErrorCodeSessionUserMismatch))
		return
	}
	apiUserId, err := strconv.Atoi(apiUserIdStr)
	if err != nil {
		apiUserId, err = model.ResolvePublicIntID("users", apiUserIdStr)
		if err != nil {
			httperr.Abort(c, types.Err(types.ErrorCodeSessionUserMismatch))
			return
		}
	}
	if id != apiUserId {
		httperr.Abort(c, types.Err(types.ErrorCodeSessionUserMismatch))
		return
	}
	if !useAccessToken && model.CoreDB() != nil {
		userID := common.ParseIntFromAny(id)
		current, err := sessionVersionCurrent(session, userID)
		if err != nil {
			c.Header("Retry-After", "1")
			httperr.Abort(c, types.Err(types.ErrorCodeGatewayTemporarilyUnavailable))
			return
		}
		if !current {
			httperr.Abort(c, types.Err(types.ErrorCodeSessionExpired))
			return
		}
	}
	if status.(int) == common.UserStatusDisabled {
		httperr.Abort(c, types.Err(types.ErrorCodeUserBanned))
		return
	}
	if role.(int) < minRole {
		httperr.Abort(c, types.Err(types.ErrorCodeAccessDenied))
		return
	}
	if !validUserInfo(username.(string), role.(int)) {
		httperr.Abort(c, types.Err(types.ErrorCodeSessionInvalid))
		return
	}
	c.Set("username", username)
	c.Set("role", role)
	c.Set("id", id)
	c.Set("use_access_token", useAccessToken)

	//userCache, err := model.GetUserCache(id.(int))
	//if err != nil {
	//	c.JSON(http.StatusOK, gin.H{
	//		"success": false,
	//		"message": err.Error(),
	//	})
	//	c.Abort()
	//	return
	//}
	//userCache.WriteContext(c)

	c.Next()
}

func TryUserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		id := session.Get("id")
		if id != nil {
			userID := common.ParseIntFromAny(id)
			current := model.CoreDB() == nil
			if !current {
				var err error
				current, err = sessionVersionCurrent(session, userID)
				if err != nil {
					c.Header("Retry-After", "1")
					httperr.Abort(c, types.Err(types.ErrorCodeGatewayTemporarilyUnavailable))
					return
				}
			}
			if current {
				c.Set("id", id)
			}
		}
		c.Next()
	}
}

// sessionVersionCurrent reads the authoritative user column. Revocation must
// not depend on Redis freshness or on the success of a cache invalidation.
func sessionVersionCurrent(session sessions.Session, userID int) (bool, error) {
	sessionVersion, ok := validSessionVersion(session.Get("session_version"))
	if !ok || userID <= 0 {
		session.Clear()
		_ = session.Save()
		return false, nil
	}
	currentVersion, err := model.GetUserSessionVersion(userID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// A failed query cannot prove revocation. Refuse this request without
		// deleting a cookie that can be checked again after Core recovers.
		return false, model.IdentityAuthorityQueryError(err)
	}
	if err == nil && currentVersion == sessionVersion {
		return true, nil
	}
	session.Clear()
	_ = session.Save()
	return false, nil
}

func validSessionVersion(value any) (int, bool) {
	var version int64
	switch typed := value.(type) {
	case int:
		version = int64(typed)
	case int8:
		version = int64(typed)
	case int16:
		version = int64(typed)
	case int32:
		version = int64(typed)
	case int64:
		version = typed
	case uint:
		if uint64(typed) > uint64(^uint(0)>>1) {
			return 0, false
		}
		version = int64(typed)
	case uint8:
		version = int64(typed)
	case uint16:
		version = int64(typed)
	case uint32:
		version = int64(typed)
	default:
		return 0, false
	}
	if version < 0 || int64(int(version)) != version {
		return 0, false
	}
	return int(version), true
}

func OptionalTokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		if !requestHasTokenCredential(c) {
			c.Next()
			return
		}
		TokenAuth()(c)
	}
}

func RejectEphemeralCredentialOutsideRelay() func(c *gin.Context) {
	return func(c *gin.Context) {
		credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential)
		if !ok || credential == nil {
			c.Next()
			return
		}
		now := common.GetTimestamp()
		model.RecordEphemeralCredentialAudit(model.EphemeralCredentialAudit{
			CredentialID:       credential.ID,
			CredentialPublicID: credential.PublicID,
			EventType:          model.EphemeralCredentialAuditDenied,
			UserID:             common.GetContextKeyInt(c, constant.ContextKeyUserId),
			TokenID:            common.GetContextKeyInt(c, constant.ContextKeyTokenId),
			RequestID:          c.GetString(common.RequestIdKey),
			Reason:             "ephemeral credentials are only valid for scoped relay requests",
			CreatedAt:          now,
		})
		abortWithCode(c, types.ErrorCodeEphemeralCredentialDenied)
	}
}

func requestHasTokenCredential(c *gin.Context) bool {
	if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
		return true
	}
	if strings.TrimSpace(c.GetHeader("x-api-key")) != "" {
		return true
	}
	if strings.TrimSpace(c.GetHeader("x-goog-api-key")) != "" {
		return true
	}
	if strings.TrimSpace(c.GetHeader("X-Token-Id")) != "" {
		return true
	}
	if strings.TrimSpace(c.Query("key")) != "" {
		return true
	}
	if strings.TrimSpace(c.GetHeader("Sec-WebSocket-Protocol")) != "" {
		return true
	}
	return false
}

func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleCommonUser)
	}
}

// UserOrTokenAuth accepts the console's authenticated user session or a model
// API key. The selected mechanism still owns all of its normal validation and
// context projection, including token model/vendor restrictions.
func UserOrTokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		// A console session remains authoritative even if the browser also sends
		// an access-token Authorization header. Only select model-token auth when
		// there is no session identity and the credential has an API-key shape.
		session := sessions.Default(c)
		hasSession := session != nil && session.Get("username") != nil
		authorization := common.NormalizeAPIKey(c.GetHeader("Authorization"))
		hasAPIKey := strings.HasPrefix(authorization, common.APITokenPrefix) ||
			strings.TrimSpace(c.GetHeader("x-api-key")) != "" ||
			strings.TrimSpace(c.GetHeader("x-goog-api-key")) != "" ||
			strings.TrimSpace(c.Query("key")) != ""
		if !hasSession && hasAPIKey {
			TokenAuth()(c)
			return
		}
		UserAuth()(c)
	}
}

func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleRootUser)
	}
}

func TokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authTimer := common.StartHotPathStage(common.HotPathStageAuthAdmission)
		authFinished := false
		defer func() {
			if !authFinished {
				authTimer.FinishFailed(true)
			}
		}()
		// Normalize every documented API-key carrier before validation. Route-
		// specific rewrites below remain for compatibility, but control-plane
		// endpoints such as /api/channel_health do not share those relay paths.
		if strings.TrimSpace(c.Request.Header.Get("Authorization")) == "" {
			for _, candidate := range []string{
				c.Request.Header.Get("x-api-key"),
				c.Request.Header.Get("x-goog-api-key"),
				c.Query("key"),
			} {
				if candidate = strings.TrimSpace(candidate); candidate != "" {
					c.Request.Header.Set("Authorization", "Bearer "+candidate)
					break
				}
			}
		}
		// 先检测是否为ws
		if c.Request.Header.Get("Sec-WebSocket-Protocol") != "" {
			// Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.sk-xxx, openai-beta.realtime-v1
			// read sk from Sec-WebSocket-Protocol
			key := c.Request.Header.Get("Sec-WebSocket-Protocol")
			parts := strings.Split(key, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "openai-insecure-api-key") {
					key = strings.TrimPrefix(part, "openai-insecure-api-key.")
					break
				}
			}
			c.Request.Header.Set("Authorization", "Bearer "+key)
		}
		// 检查path包含/v1/messages
		if strings.Contains(c.Request.URL.Path, "/v1/messages") {
			anthropicKey := c.Request.Header.Get("x-api-key")
			if anthropicKey != "" {
				c.Request.Header.Set("Authorization", "Bearer "+anthropicKey)
			}
		}
		// gemini api 从query中获取key
		if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models") ||
			strings.HasPrefix(c.Request.URL.Path, "/v1beta/openai/models") ||
			strings.HasPrefix(c.Request.URL.Path, "/v1/models") {
			skKey := c.Query("key")
			if skKey != "" {
				c.Request.Header.Set("Authorization", "Bearer "+skKey)
			}
			// 从x-goog-api-key header中获取key
			xGoogKey := c.Request.Header.Get("x-goog-api-key")
			if xGoogKey != "" {
				c.Request.Header.Set("Authorization", "Bearer "+xGoogKey)
			}
		}
		key := common.NormalizeAPIKey(c.Request.Header.Get("Authorization"))
		parts := []string{key}
		if strings.HasPrefix(key, common.APITokenPrefix) && len(key) > len(common.APITokenPrefix)+48 {
			suffixStart := len(common.APITokenPrefix) + 48
			if key[suffixStart] == '-' {
				parts = []string{key[:suffixStart], key[suffixStart+1:]}
				key = parts[0]
			}
		}
		token := (*model.Token)(nil)
		userCache := (*model.UserBase)(nil)
		ephemeralCredential := (*model.EphemeralCredential)(nil)
		identitySnapshot := (*model.IdentitySnapshot)(nil)
		var identitySnapshotErr error
		var err error
		internalIdentity, internalRequest := common.InternalRelayIdentityFromContext(c.Request.Context())
		if internalRequest {
			if call := common.InternalRelayCallStateFromContext(c.Request.Context()); call != nil && call.Reservation != nil {
				c.Set(common.RequestIdKey, call.Reservation.RequestID)
				c.Header(common.RequestIdKey, call.Reservation.RequestID)
			}
			key = ""
			parts = nil
			token, err = requestpath.Current().TokenByID(internalIdentity.TokenID)
			if err == nil {
				err = model.ValidateTokenState(token, "internal relay")
			}
			if err == nil && token.UserId != internalIdentity.UserID {
				err = model.ErrInvalidAPIKey
			}
			if err == nil && internalIdentity.EphemeralID > 0 {
				credential, credentialErr := requestpath.Current().EphemeralByID(c.Request.Context(), internalIdentity.EphemeralID)
				err = credentialErr
				if err == nil && (credential.TokenID != token.Id || credential.UserID != token.UserId || credential.RevokedAt > 0 || credential.ExpiresAt <= time.Now().Unix()) {
					err = model.ErrInvalidAPIKey
				}
				if err == nil {
					ephemeralCredential = credential
					common.SetContextKey(c, constant.ContextKeyEphemeralCredential, credential)
				}
			}
			common.SetContextKey(c, constant.ContextKeySessionID, internalIdentity.SessionID)
			common.SetContextKey(c, constant.ContextKeySessionSource, internalIdentity.SessionSource)
			common.SetContextKey(c, constant.ContextKeyAgentID, internalIdentity.AgentID)
			c.Set("parent_request_id", internalIdentity.ParentRequestID)
		} else if key != "" && model.IsEphemeralCredentialKey(key) {
			ephemeralCredential, token, err = requestpath.Current().EphemeralCredential(key)
			// An inactive/exhausted parent is still a verified credential;
			// never treat its ephemeral carrier as an ordinary API key.
			if token != nil && (err == nil || errors.Is(err, model.ErrTokenBudgetExhausted) || errors.Is(err, model.ErrTokenAccessInactive)) {
				common.SetContextKey(c, constant.ContextKeyEphemeralCredential, ephemeralCredential)
				parts = []string{ephemeralCredential.PublicID}
				key = ""
			}
		} else if key == "" {
			if tokenByID, handled, tokenErr := resolveTokenByIDHeader(c); handled {
				token = tokenByID
				err = tokenErr
			} else {
				token, err = requestpath.Current().LegacyToken(key)
			}
		} else if model.IdentitySnapshotsEnabled() {
			identitySnapshot, identitySnapshotErr = requestpath.Current().Identity(c.Request.Context(), key)
			if identitySnapshot == nil || errors.Is(identitySnapshotErr, model.ErrInvalidAPIKey) {
				if errors.Is(identitySnapshotErr, model.ErrInvalidAPIKey) {
					abortWithCode(c, types.ErrorCodeInvalidAPIKey)
				} else {
					c.Header("Retry-After", "1")
					abortWithCodeStatus(c, http.StatusServiceUnavailable, types.ErrorCodeGatewayTemporarilyUnavailable)
				}
				return
			}
			token = identitySnapshot.Token
			userCache = identitySnapshot.Actor
			c.Set(model.IdentitySnapshotContextKey, identitySnapshot)
		} else {
			token, err = requestpath.Current().LegacyToken(key)
		}
		if errors.Is(err, model.ErrIdentityAuthorityUnavailable) {
			c.Header("Retry-After", "1")
			abortWithCodeStatus(c, http.StatusServiceUnavailable, types.ErrorCodeGatewayTemporarilyUnavailable)
			return
		}
		// Only operational-state failures carry a verified credential. Unknown
		// credentials and internal-only keys must never expose project identity.
		if err != nil && !errors.Is(err, model.ErrTokenBudgetExhausted) && !errors.Is(err, model.ErrTokenAccessInactive) {
			abortWithCode(c, types.ErrorCodeInvalidAPIKey)
			return
		}
		if key != "" && identitySnapshot == nil {
			token, err = requestpath.Current().Credential(token.Id, key, time.Now())
			if err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					abortWithCode(c, types.ErrorCodeQueryDataError)
					return
				}
				abortWithCode(c, types.ErrorCodeInvalidAPIKey)
				return
			}
		}
		if model.IdentitySnapshotsEnabled() && identitySnapshot == nil && err == nil {
			var ephemeralID uint
			if ephemeralCredential != nil {
				ephemeralID = ephemeralCredential.ID
			}
			identitySnapshot, identitySnapshotErr = requestpath.Current().VerifiedIdentity(c.Request.Context(), token.Id, ephemeralID)
			if identitySnapshot == nil {
				if errors.Is(identitySnapshotErr, model.ErrInvalidAPIKey) {
					abortWithCode(c, types.ErrorCodeInvalidAPIKey)
				} else {
					c.Header("Retry-After", "1")
					abortWithCodeStatus(c, http.StatusServiceUnavailable, types.ErrorCodeGatewayTemporarilyUnavailable)
				}
				return
			}
			token, userCache = identitySnapshot.Token, identitySnapshot.Actor
			c.Set(model.IdentitySnapshotContextKey, identitySnapshot)
		}
		// Authenticate the credential before reading its body. Unknown keys
		// must not make the gateway buffer or wait for an unauthenticated upload.
		// Verified operational rejections still retain the requested model.
		peekRequestedModel(c)
		if c.IsAborted() {
			return
		}
		var principal model.ProjectPrincipal
		var projectErr error
		if identitySnapshot != nil {
			principal, projectErr = identitySnapshot.Principal, identitySnapshotErr
		} else {
			principal, projectErr = requestpath.Current().Principal(token.Id, time.Now())
		}
		if internalRequest && projectErr == nil && (principal.ProjectID != internalIdentity.ProjectID || principal.ActorUserID != internalIdentity.ActorUserID || principal.PayerUserID != internalIdentity.PayerUserID) {
			projectErr = model.ErrProjectForbidden
		}
		if principal.ProjectID > 0 {
			setProjectPrincipalContext(c, principal)
			token.ProjectID = principal.ProjectID
			c.Set("token_id", token.Id)
			c.Set("authenticated_token_owner_id", token.UserId)
			c.Set("token_name", token.Name)
		}
		if projectErr != nil {
			switch {
			case errors.Is(projectErr, model.ErrProjectAccessSuspended), errors.Is(projectErr, model.ErrProjectForbidden):
				abortWithCode(c, types.ErrorCodeProjectAccessSuspended)
			case errors.Is(projectErr, model.ErrTokenAccessInactive):
				// The Key's own state stopped it. Say which state, instead of
				// telling a member who disabled their own Key to contact a
				// project admin.
				abortTokenInactive(c, projectErr)
			case errors.Is(projectErr, model.ErrTokenBudgetExhausted):
				abortWithCode(c, types.ErrorCodeKeyBudgetExceeded)
			default:
				abortWithCode(c, types.ErrorCodeQueryDataError)
			}
			return
		}

		clientIp := common.GetRealClientIP(c)
		allowIpsMap := token.GetIpLimitsMap()
		if len(allowIpsMap) != 0 {
			if _, ok := allowIpsMap[clientIp]; !ok {
				abortWithCode(c, types.ErrorCodeTokenIpNotAllowed)
				return
			}
		}
		blackIpsMap := token.GetIPBlacklistMap()
		if len(blackIpsMap) != 0 {
			if _, blocked := blackIpsMap[clientIp]; blocked {
				abortWithCode(c, types.ErrorCodeTokenIpDenied)
				return
			}
		}

		if userCache == nil {
			userCache, err = requestpath.Current().User(token.UserId)
			if err != nil {
				abortWithCode(c, types.ErrorCodeQueryDataError)
				return
			}
		}
		userEnabled := userCache.Status == common.UserStatusEnabled
		if !userEnabled {
			abortWithCode(c, types.ErrorCodeUserBanned)
			return
		}

		userCache.WriteContext(c)
		payer := userCache
		if identitySnapshot != nil {
			payer = identitySnapshot.Payer
		} else if principal.PayerUserID != userCache.Id {
			payer, err = requestpath.Current().User(principal.PayerUserID)
			if err != nil {
				abortWithCode(c, types.ErrorCodeQueryDataError)
				return
			}
		}
		// An address opened to chosen accounts refuses everyone else's Keys
		// here, before any billing: hiding it from the console alone would not
		// stop anyone who learned the hostname.
		if registered, allowed := model.AccessAddressAllowed(c.Request.Host, payer.Id); registered && !allowed {
			abortWithCode(c, types.ErrorCodeAddressNotEnabled)
			return
		}
		common.SetContextKey(c, constant.ContextKeyUserQuota, payer.Quota)
		common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, payer.HasToppedUp)
		common.SetContextKey(c, constant.ContextKeyUserPricePlanID, payer.PricePlanID)
		common.SetContextKey(c, constant.ContextKeyUserRPMLimit, payer.RPMLimit)
		common.SetContextKey(c, constant.ContextKeyUserTPMLimit, payer.TPMLimit)

		routeGroup := model.NormalizeChannelLane(strings.TrimSpace(token.Group))
		if !model.IsValidChannelLane(routeGroup) {
			routeGroup = model.ChannelLaneDirect
		}
		common.SetContextKey(c, constant.ContextKeyUsingGroup, routeGroup)

		err = SetupContextForToken(c, token, parts...)
		if err != nil {
			return
		}
		applyProjectMemberLimits(c, principal)
		applyEffortCap(c, token.ReasoningEffortCap, principal.MemberEffortCap)
		c.Set("decision_question_generation_settings", token.DecisionQuestionGeneration)
		if internalRequest {
			common.SetContextKey(c, constant.ContextKeyUsingGroup, internalIdentity.ForceLane)
			c.Set("force_lane", internalIdentity.ForceLane)
		}
		// Stop the authentication clock before the relay runs. A deferred-only
		// timer would incorrectly include the upstream and settlement waits.
		authTimer.Finish(nil)
		authFinished = true
		c.Next()
	}
}

func SetupContextForToken(c *gin.Context, token *model.Token, parts ...string) error {
	if token == nil {
		return fmt.Errorf("token is nil")
	}
	if common.GetContextKeyInt(c, constant.ContextKeyProjectID) == 0 {
		c.Set("id", token.UserId)
	}
	c.Set("token_id", token.Id)
	c.Set("authenticated_token_owner_id", token.UserId)
	c.Set("token_key", token.Key)
	c.Set("token_name", token.Name)
	c.Set("token_unlimited_quota", token.UnlimitedQuota)
	common.SetContextKey(c, constant.ContextKeyTokenUsageAlertPercent, token.UsageAlertPercent)
	common.SetContextKey(c, constant.ContextKeyTokenProduct, token.Product)
	common.SetContextKey(c, constant.ContextKeyTokenMetadata, string(token.Metadata))
	if !token.UnlimitedQuota {
		c.Set("token_quota", token.RemainQuota.Int64())
	}
	common.SetContextKey(c, constant.ContextKeyTokenRateLimitsEnabled, token.RateLimitsEnabled)
	common.SetContextKey(c, constant.ContextKeyTokenRPMLimit, token.RPMLimit)
	common.SetContextKey(c, constant.ContextKeyTokenTPMLimit, token.TPMLimit)
	if token.ModelLimitsEnabled {
		c.Set("token_model_limit_enabled", true)
		c.Set("token_model_limit", token.GetModelLimitsMap())
		common.SetContextKey(c, constant.ContextKeyTokenModelScopeVendors, token.GetModelScopeVendorSet())
	} else {
		c.Set("token_model_limit_enabled", false)
	}
	c.Set("token_group", model.NormalizeChannelLane(token.Group))
	common.SetContextKey(c, constant.ContextKeyTokenVendorGroups, token.VendorGroups.Normalize())
	common.SetContextKey(c, constant.ContextKeyTokenModelLanes, token.ModelLanes.Normalize())
	common.SetContextKey(c, constant.ContextKeyTokenVendorLimitEnabled, token.VendorLimitsEnabled)
	if token.VendorLimitsEnabled && len(token.VendorLimits) > 0 {
		common.SetContextKey(c, constant.ContextKeyTokenVendorLimits, token.VendorLimits)
	}
	common.SetContextKey(c, constant.ContextKeyTokenSearchLimitEnabled, token.SearchLimitsEnabled)
	common.SetContextKey(c, constant.ContextKeyTokenSearchProviders, token.SearchProviders)
	common.SetContextKey(c, constant.ContextKeyTokenSearchDomainAllow, token.SearchDomainAllowlist)
	if len(parts) > 1 {
		if model.IsAdmin(c.GetInt("id")) {
			c.Set("specific_channel_id", parts[1])
		} else {
			abortWithCode(c, types.ErrorCodeChannelSelectionDenied)
			return fmt.Errorf("regular users are not allowed to select a specific channel")
		}
	}
	return nil
}

// setProjectPrincipalContext uses only the database identity, never client
// project selectors. SetupContextForToken must preserve this actor.
func setProjectPrincipalContext(c *gin.Context, principal model.ProjectPrincipal) {
	common.SetContextKey(c, constant.ContextKeyProjectID, principal.ProjectID)
	common.SetContextKey(c, constant.ContextKeyPayerUserID, principal.PayerUserID)
	common.SetContextKey(c, constant.ContextKeyActorUserID, principal.ActorUserID)
	c.Set("id", principal.ActorUserID)
}

// abortTokenInactive tells the caller why a Key stopped working. A bare
// ErrTokenAccessInactive (the state is not known) stays invalid_api_key.
func abortTokenInactive(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrTokenExpired):
		abortWithCode(c, types.ErrorCodeTokenExpired)
	case errors.Is(err, model.ErrTokenDisabled):
		abortWithCode(c, types.ErrorCodeTokenDisabled)
	default:
		abortWithCode(c, types.ErrorCodeInvalidAPIKey)
	}
}
