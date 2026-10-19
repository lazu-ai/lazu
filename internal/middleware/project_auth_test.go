package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/lazu-ai/lazu/internal/ratelimit"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestProjectMemberContextNeverInheritsOwnerChannelPrivilege(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	owner := model.User{Id: 981, Username: "project-owner", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	member := model.User{Id: 982, Username: "project-member", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	for _, u := range []*model.User{&owner, &member} {
		if err := model.CoreDB().Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/models", nil)
	principal := model.ProjectPrincipal{ProjectID: 10, PayerUserID: owner.Id, ActorUserID: member.Id, TokenID: 30}
	setProjectPrincipalContext(c, principal)
	token := &model.Token{Id: 30, UserId: member.Id, ProjectID: 10, UnlimitedQuota: true}
	if err := SetupContextForToken(c, token, "credential", "99"); err == nil {
		t.Fatal("member key inherited the payer's channel selection")
	}
	if c.GetInt("id") != member.Id || common.GetContextKeyInt(c, constant.ContextKeyPayerUserID) != owner.Id || c.GetString("specific_channel_id") != "" {
		t.Fatal("project principal was overwritten")
	}
}

func TestProjectRateLimitsAggregateMembersAgainstPayer(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	limiter := ratelimit.NewService()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setProjectPrincipalContext(c, model.ProjectPrincipal{ProjectID: 10, PayerUserID: 20, ActorUserID: 39, TokenID: 30})
	request := requestRateLimitInputFromContext(c, 60, 1, 0)
	tokens := tokenRateLimitInputFromContext(c, 1, 60)
	if request.UserID != 20 || tokens.UserID != 20 || c.GetInt("id") != 39 {
		t.Fatalf("wrong project identities: %+v %+v", request, tokens)
	}
	result, err := limiter.PreflightRequest(context.Background(), request)
	if err != nil || !result.Allowed() {
		t.Fatalf("first request: %+v %v", result, err)
	}
	setProjectPrincipalContext(c, model.ProjectPrincipal{ProjectID: 10, PayerUserID: 20, ActorUserID: 40, TokenID: 31})
	result, err = limiter.PreflightRequest(context.Background(), requestRateLimitInputFromContext(c, 60, 1, 0))
	if err != nil || result.Decision != ratelimit.DecisionTotalRPM || c.GetInt("id") != 40 {
		t.Fatalf("member bypassed payer cap: %+v %v", result, err)
	}
	tokens.AccountTPMLimit = 1
	result, err = limiter.CheckAndRecordTPM(context.Background(), tokens)
	if err != nil || !result.Allowed() {
		t.Fatalf("first TPM: %+v %v", result, err)
	}
	memberTokens := tokenRateLimitInputFromContext(c, 1, 60)
	memberTokens.AccountTPMLimit = 1
	result, err = limiter.CheckAndRecordTPM(context.Background(), memberTokens)
	if err != nil || result.Decision != ratelimit.DecisionAccountTPM {
		t.Fatalf("member bypassed payer TPM: %+v %v", result, err)
	}
	legacy, _ := gin.CreateTestContext(httptest.NewRecorder())
	legacy.Set("id", 50)
	common.SetContextKey(legacy, constant.ContextKeyPayerUserID, 20)
	if rateLimitUserIDFromContext(legacy) != 50 {
		t.Fatal("payer without project changed legacy identity")
	}
}
