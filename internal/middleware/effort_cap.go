package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

// EffortAppliedHeader tells the caller which effort actually ran when the
// gateway lowered it to the Key's cap.
const EffortAppliedHeader = "X-Lazu-Effort-Applied"

// applyProjectMemberLimits stores the member's model allowlist so every model
// check that already reads the Key's allowlist also reads this one.
func applyProjectMemberLimits(c *gin.Context, principal model.ProjectPrincipal) {
	if len(principal.MemberAllowedModels) == 0 {
		return
	}
	allowed := make(map[string]bool, len(principal.MemberAllowedModels))
	for _, name := range principal.MemberAllowedModels {
		if name = strings.TrimSpace(name); name != "" {
			allowed[name] = true
		}
	}
	if len(allowed) > 0 {
		common.SetContextKey(c, constant.ContextKeyMemberModelLimit, allowed)
	}
}

// memberModelAllows reports whether the member's own allowlist, when set,
// includes modelName.
func memberModelAllows(c *gin.Context, modelName string) bool {
	allowed, ok := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyMemberModelLimit)
	if !ok || len(allowed) == 0 {
		return true
	}
	return model.TokenModelScopeAllows(allowed, nil, modelName)
}

// applyEffortCap records policy only. The final upstream body is clamped after
// model mapping, protocol conversion, suffix expansion and channel overrides.
func applyEffortCap(c *gin.Context, keyCap, memberCap string) {
	cap, source := model.EffectiveEffortCap(keyCap, memberCap)
	if cap == "" {
		return
	}
	common.SetContextKey(c, constant.ContextKeyEffortCap, cap)
	common.SetContextKey(c, constant.ContextKeyEffortCapSource, source)
}
