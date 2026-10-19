package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

// The existing SQLite authority fixture exercises TokenAuth's common timing
// boundary. Modern identity and generation fences have separate model tests.
func authStageMetricsFixture(t *testing.T) authorityFixture {
	t.Helper()
	fixture := newAuthorityFixture(t)
	model.EnsureColumnNamesInitialized()
	previousMetrics, previousMemoryCache := common.HotPathMetricsEnabled, common.MemoryCacheEnabled
	common.HotPathMetricsEnabled, common.MemoryCacheEnabled = true, false
	t.Cleanup(func() {
		common.HotPathMetricsEnabled, common.MemoryCacheEnabled = previousMetrics, previousMemoryCache
	})
	return fixture
}

func authAdmissionStage() common.HotPathStageStats {
	return common.SnapshotHotPathMetrics().Stages[common.HotPathStageAuthAdmission]
}

func TestTokenAuthAdmissionMetricFinishesBeforeDownstream(t *testing.T) {
	fixture := authStageMetricsFixture(t)
	before := authAdmissionStage()
	type observation struct {
		stage                                common.HotPathStageStats
		tokenID, actorID, payerID, projectID int
	}
	entered := make(chan observation, 1)
	release := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		resume()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("authenticated request did not finish after downstream was released")
		}
	})
	router := gin.New()
	router.POST("/v1/responses", TokenAuth(), func(c *gin.Context) {
		entered <- observation{
			stage: authAdmissionStage(), tokenID: c.GetInt("token_id"),
			actorID:   common.GetContextKeyInt(c, constant.ContextKeyActorUserID),
			payerID:   common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
			projectID: common.GetContextKeyInt(c, constant.ContextKeyProjectID),
		}
		<-release
		// A downstream abort must not turn successful authentication into a
		// failed observation when TokenAuth's deferred cleanup runs.
		c.AbortWithStatus(http.StatusServiceUnavailable)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	response := httptest.NewRecorder()
	go func() {
		defer close(finished)
		router.ServeHTTP(response, request)
	}()
	var observed observation
	select {
	case observed = <-entered:
	case <-finished:
		t.Fatalf("authentication rejected the fixture: status=%d body=%s", response.Code, response.Body.String())
	case <-time.After(10 * time.Second):
		t.Fatal("authenticated request did not enter downstream")
	}
	if observed.tokenID != fixture.token.Id || observed.actorID != fixture.user.Id || observed.payerID != fixture.user.Id || observed.projectID != fixture.token.ProjectID {
		t.Fatalf("authentication context was not preserved: %+v", observed)
	}
	if observed.stage.Count != before.Count+1 || observed.stage.ErrorCount != before.ErrorCount {
		t.Fatalf("authentication stage was not finished before downstream blocked: before=%+v observed=%+v", before, observed.stage)
	}
	select {
	case <-finished:
		t.Fatal("request finished while downstream was still gated")
	default:
	}
	resume()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("request did not finish after downstream was released")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("downstream abort was not preserved: status=%d", response.Code)
	}
	if after := authAdmissionStage(); after != observed.stage {
		t.Fatalf("downstream completion changed the finished authentication stage: during=%+v after=%+v", observed.stage, after)
	}
}

func TestTokenAuthAdmissionMetricCountsEachRejectionOnce(t *testing.T) {
	for _, test := range []struct {
		name             string
		authorityFailure bool
		wantStatus       int
	}{
		{name: "missing_credential", wantStatus: http.StatusUnauthorized},
		{name: "authority_unavailable", authorityFailure: true, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := authStageMetricsFixture(t)
			var headers map[string]string
			if test.authorityFailure {
				injectAuthoritySQLFailure(t, fixture.db, "tokens")
				headers = map[string]string{"Authorization": "Bearer " + fixture.key}
			}
			before := authAdmissionStage()
			response, _ := performAuthRequest(t, TokenAuth(), nil, headers)
			if response.Code != test.wantStatus {
				t.Fatalf("rejection contract changed: status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			after := authAdmissionStage()
			if after.Count != before.Count+1 || after.ErrorCount != before.ErrorCount+1 {
				t.Fatalf("rejection must record one failed authentication stage: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestTokenAuthAdmissionMetricDisabledPreservesCountersAndBehavior(t *testing.T) {
	fixture := authStageMetricsFixture(t)
	headers := map[string]string{"Authorization": "Bearer " + fixture.token.Key}
	// Seed a real successful observation so this test also catches mutation of
	// an existing stage when it is run by itself.
	response, payload := performAuthRequest(t, TokenAuth(), nil, headers)
	if response.Code != http.StatusOK || payload["token_id"] != float64(fixture.token.Id) {
		t.Fatalf("could not seed successful authentication: status=%d payload=%v", response.Code, payload)
	}
	before := authAdmissionStage()
	common.HotPathMetricsEnabled = false
	response, payload = performAuthRequest(t, TokenAuth(), nil, headers)
	if response.Code != http.StatusOK || payload["token_id"] != float64(fixture.token.Id) || payload["id"] != float64(fixture.user.Id) {
		t.Fatalf("disabled metrics changed successful authentication: status=%d payload=%v", response.Code, payload)
	}
	response, _ = performAuthRequest(t, TokenAuth(), nil, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled metrics changed credential rejection: status=%d body=%s", response.Code, response.Body.String())
	}
	if after := authAdmissionStage(); after != before {
		t.Fatalf("disabled metrics mutated authentication observations: before=%+v after=%+v", before, after)
	}
}
