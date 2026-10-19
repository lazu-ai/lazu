package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
)

func TestAccountRateLimitFromContextUsesAccountOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, true)
	common.SetContextKey(c, constant.ContextKeyUserRPMLimit, 7)
	common.SetContextKey(c, constant.ContextKeyUserTPMLimit, 7000)

	rpm, tpm := accountRateLimitFromContext(c)
	if rpm != 7 || tpm != 7000 {
		t.Fatalf("expected per-account overrides 7/7000, got %d/%d", rpm, tpm)
	}
}

func TestPublicTokenPayloadNeverReturnsStoredSecret(t *testing.T) {
	payload := publicTokenPayload(nil, &model.Token{
		PublicID: "tok_public",
		Key:      "stored-secret-value",
		Name:     "production",
	})

	if _, exposed := payload["key"]; exposed {
		t.Fatal("stored API Key secret must not be returned after creation")
	}
	if payload["masked_key"] != "************alue" {
		t.Fatalf("expected masked identifier, got %v", payload["masked_key"])
	}
}

func TestAccountRateLimitFromContextKeepsExplicitUnpaid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, false)

	rpm, tpm := accountRateLimitFromContext(c)
	if rpm != setting.RateLimitUnpaidRPM || tpm != setting.RateLimitUnpaidTPM {
		t.Fatalf("expected unpaid ceiling %d/%d, got %d/%d", setting.RateLimitUnpaidRPM, setting.RateLimitUnpaidTPM, rpm, tpm)
	}
	payload := tokenRateLimitPayloadForAccount(&model.Token{}, rpm, tpm)
	if _, ok := payload["tier"]; ok {
		t.Fatalf("rate limit payload still carries a tier: %v", payload)
	}
}

// publicTokenPayload is the base every key projection is built from, and the
// project payload replaces its numeric `status` with the contract string.
// status_code is what keeps the number reachable on both shapes.
func TestPublicTokenPayloadCarriesNumericStatusCode(t *testing.T) {
	payload := publicTokenPayload(nil, &model.Token{PublicID: "tok_status", Status: common.TokenStatusDisabled})
	if payload["status"] != common.TokenStatusDisabled {
		t.Fatalf("base payload changed the numeric status: %v", payload["status"])
	}
	if payload["status_code"] != common.TokenStatusDisabled {
		t.Fatalf("base payload is missing status_code: %v", payload["status_code"])
	}
}
