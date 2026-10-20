package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestInFlightMediaJobsListsOnlyUnsettledJobs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	user := model.User{Id: 602, Username: "in-flight-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	create := func(requestID string) *model.MediaJob {
		job, err := model.CreateMediaJob(model.CreateMediaJobInput{
			UserID: user.Id, ModelName: "seedance-2.5", Kind: model.MediaJobKindVideo,
			RequestID: requestID, Now: now,
		})
		if err != nil {
			t.Fatalf("create job: %v", err)
		}
		return job
	}
	waiting := create("req-in-flight-waiting")
	finished := create("req-in-flight-finished")
	if err := model.MarkMediaJobFailed(finished.ID, "video_provider_failed", "failed", now); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/in_flight", nil)
	c.Set("id", user.Id)
	GetUserInFlightMediaJobs(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || len(body.Data) != 1 || body.Data[0].ID != waiting.PublicID || body.Data[0].Status != model.MediaJobStatusPending {
		t.Fatalf("in-flight list = %s", recorder.Body.String())
	}
}
