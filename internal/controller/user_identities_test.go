package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func configureIdentityTestProviders(t *testing.T) {
	t.Helper()
	previousPasswordLogin := common.PasswordLoginEnabled
	previousBreakGlass := common.OIDCBreakGlassPasswordEnabled
	previousGoogleID := common.GoogleOAuthClientID
	previousGoogleSecret := common.GoogleOAuthClientSecret
	previousGitHubID := common.GitHubOAuthClientID
	previousGitHubSecret := common.GitHubOAuthClientSecret
	common.PasswordLoginEnabled = false
	common.OIDCBreakGlassPasswordEnabled = false
	common.GoogleOAuthClientID = "google-client"
	common.GoogleOAuthClientSecret = "placeholder-google-secret"
	common.GitHubOAuthClientID = ""
	common.GitHubOAuthClientSecret = ""
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPasswordLogin
		common.OIDCBreakGlassPasswordEnabled = previousBreakGlass
		common.GoogleOAuthClientID = previousGoogleID
		common.GoogleOAuthClientSecret = previousGoogleSecret
		common.GitHubOAuthClientID = previousGitHubID
		common.GitHubOAuthClientSecret = previousGitHubSecret
	})
}

func TestExternalIdentityUnlinkAlwaysFailsClosed(t *testing.T) {
	setupOIDCTestDB(t)
	configureIdentityTestProviders(t)
	gin.SetMode(gin.TestMode)
	common.PasswordLoginEnabled = true

	user := model.User{
		Username: "unlink-guard",
		Password: "password123",
		Email:    "unlink@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	identities := []model.UserIdentity{
		{UserID: user.Id, Provider: googleOAuthProvider, Subject: "google-unlink", Email: user.Email},
		{UserID: user.Id, Provider: githubOAuthProvider, Subject: "github-unlink", Email: user.Email},
		{
			UserID: user.Id, Provider: oidcIdentityProvider,
			Subject: oidcNamespacedSubject(common.OIDCIssuerURL, "oidc-unlink-subject"), Email: user.Email,
		},
	}
	if err := model.CoreDB().Create(&identities).Error; err != nil {
		t.Fatalf("seed identities: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id) })
	engine.GET("/identities", GetSelfIdentities)
	engine.DELETE("/identities/:provider", DeleteSelfIdentity)

	listRecorder := httptest.NewRecorder()
	engine.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/identities", nil))
	var listResponse struct {
		Data struct {
			Identities []linkedIdentity `json:"identities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode identities: %v", err)
	}
	if len(listResponse.Data.Identities) != len(identities) {
		t.Fatalf("identity count = %d, want %d", len(listResponse.Data.Identities), len(identities))
	}
	for _, identity := range listResponse.Data.Identities {
		if identity.Removable {
			t.Fatalf("identity %s must remain read-only: %+v", identity.Provider, identity)
		}
		deleteRecorder := httptest.NewRecorder()
		engine.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/identities/"+identity.Provider, nil))
		requireUserAPIError(t, deleteRecorder, http.StatusForbidden, types.ErrorCodeIdentityUnlinkUnavailable)
	}
	var count int64
	if err := model.CoreDB().Model(&model.UserIdentity{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if count != int64(len(identities)) {
		t.Fatalf("an identity was removed despite fail-closed unlink, count=%d", count)
	}
}

func containsJSONMessage(payload []byte, want string) bool {
	var response struct {
		Message string `json:"message"`
	}
	return json.Unmarshal(payload, &response) == nil && response.Message == want
}
