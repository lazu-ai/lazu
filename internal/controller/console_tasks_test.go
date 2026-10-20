package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

type consoleTasksBody struct {
	Success bool `json:"success"`
	Data    struct {
		Total   int `json:"total"`
		Summary struct {
			Tasks            int   `json:"tasks"`
			SpendMicrousd    int64 `json:"spend_microusd"`
			Active           int   `json:"active"`
			ActiveHold       int64 `json:"active_hold_microusd"`
			Failed           int   `json:"failed"`
			RefundedMicrousd int64 `json:"refunded_microusd"`
			ExpiringSoon     int   `json:"expiring_soon"`
		} `json:"summary"`
		Items []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			TokenName string `json:"token_name"`
			Prompt    string `json:"prompt"`
			Spec      struct {
				Seconds         int    `json:"seconds"`
				Resolution      string `json:"resolution"`
				ReferenceVideos int    `json:"reference_videos"`
			} `json:"spec"`
			Hold   int64 `json:"hold_microusd"`
			Charge int64 `json:"charge_microusd"`
			Refund int64 `json:"refund_microusd"`
		} `json:"items"`
	} `json:"data"`
}

func seedConsoleTask(t *testing.T, db *gorm.DB, userID int, tokenID *int, requestID string, status string, request string, cost string, artifact string, now time.Time) *model.MediaJob {
	t.Helper()
	input := model.CreateMediaJobInput{
		UserID: userID, ModelName: "seedance-2.5", Kind: model.MediaJobKindVideo,
		RequestID: requestID, Now: now,
	}
	if tokenID != nil {
		input.TokenID = *tokenID
	}
	job, err := model.CreateMediaJob(input)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := db.Model(&model.MediaJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"status": status, "request_snapshot": request, "cost_snapshot": cost, "artifact_file_id": artifact, "created_at": now,
	}).Error; err != nil {
		t.Fatalf("update job: %v", err)
	}
	return job
}

func getConsoleTasks(t *testing.T, userID int, query string) consoleTasksBody {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/tasks?"+query, nil)
	c.Set("id", userID)
	GetUserTasks(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body consoleTasksBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestConsoleTasksReadLikeTheUsageLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	user := model.User{Id: 603, Username: "tasks-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{Id: 61, UserId: user.Id, Key: "consoletasktoken", Name: "video-prod", Status: 1, ExpiredTime: -1}
	if err := token.Insert(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(6 * time.Hour).Unix()
	if err := db.Create(&model.File{ID: "file-lazu-task-done", UserID: user.Id, Filename: "a.mp4", Purpose: "media_artifact", MimeType: "video/mp4", Bytes: 9, StorageKey: "k", Status: model.FileStatusProcessed, ExpiresAt: &expires, CreatedAt: now.Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	seedConsoleTask(t, db, user.Id, &token.Id, "req-task-running", model.MediaJobStatusRunning,
		`{"prompt":"sunset over the sea","seconds":5,"resolution":"720p","_billing_reserved_quota_microusd":1155000}`,
		`{"customer_charge_microusd":1155000}`, "", now.Add(-time.Minute))
	seedConsoleTask(t, db, user.Id, &token.Id, "req-task-done", model.MediaJobStatusSucceeded,
		`{"prompt":"paper boat on a pond","seconds":4,"resolution":"480p","input_references":[{"type":"video","url":"https://x/v.mp4"}],"_billing_reserved_quota_microusd":3502000}`,
		`{"customer_charge_microusd":491000}`, "file-lazu-task-done", now.Add(-2*time.Hour))
	seedConsoleTask(t, db, user.Id, &token.Id, "req-task-failed", model.MediaJobStatusFailed,
		`{"prompt":"dancer turns on the beat","seconds":10,"resolution":"720p","_billing_reserved_quota_microusd":2310000}`,
		`{"customer_charge_microusd":2310000}`, "", now.Add(-3*time.Hour))

	all := getConsoleTasks(t, user.Id, "")
	s := all.Data.Summary
	if all.Data.Total != 3 || s.Tasks != 3 || s.Active != 1 || s.ActiveHold != 1155000 || s.Failed != 1 ||
		s.SpendMicrousd != 491000 || s.RefundedMicrousd != 2310000+3011000 || s.ExpiringSoon != 1 {
		t.Fatalf("summary = %+v total=%d", s, all.Data.Total)
	}
	if all.Data.Items[0].Status != model.MediaJobStatusRunning {
		t.Fatalf("not newest first: %+v", all.Data.Items)
	}
	done := all.Data.Items[1]
	if done.TokenName != "video-prod" || done.Prompt != "paper boat on a pond" || done.Spec.Seconds != 4 ||
		done.Spec.Resolution != "480p" || done.Spec.ReferenceVideos != 1 || done.Hold != 3502000 || done.Charge != 491000 || done.Refund != 3011000 {
		t.Fatalf("succeeded row = %+v", done)
	}
	if failed := all.Data.Items[2]; failed.Charge != 0 || failed.Refund != 2310000 {
		t.Fatalf("failed row = %+v", failed)
	}

	active := getConsoleTasks(t, user.Id, "status=active")
	if active.Data.Total != 1 || active.Data.Summary.Tasks != 3 {
		t.Fatalf("active filter: total=%d summary=%+v", active.Data.Total, active.Data.Summary)
	}
	found := getConsoleTasks(t, user.Id, "q=BOAT")
	if found.Data.Total != 1 || found.Data.Items[0].Prompt != "paper boat on a pond" {
		t.Fatalf("search by prompt: %+v", found.Data.Items)
	}
	byRequest := getConsoleTasks(t, user.Id, "q=req-task-failed")
	if byRequest.Data.Total != 1 || byRequest.Data.Items[0].Status != model.MediaJobStatusFailed {
		t.Fatalf("search by request id: %+v", byRequest.Data.Items)
	}
}

func TestConsoleTaskDetailIncludesTheRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	user := model.User{Id: 604, Username: "task-detail-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	job := seedConsoleTask(t, db, user.Id, nil, "req-task-detail", model.MediaJobStatusRunning,
		`{"prompt":"a red fox in snow","seconds":4}`, `{}`, "", time.Now().UTC())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/tasks/"+job.PublicID, nil)
	c.Params = gin.Params{{Key: "id", Value: job.PublicID}}
	c.Set("id", user.Id)
	GetUserTask(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data struct {
			ID      string          `json:"id"`
			Request json.RawMessage `json:"request"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ID != job.PublicID || len(body.Data.Request) == 0 {
		t.Fatalf("detail = %s", recorder.Body.String())
	}

	other := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(other)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/tasks/mjob_missing", nil)
	c2.Params = gin.Params{{Key: "id", Value: "mjob_missing"}}
	c2.Set("id", user.Id)
	GetUserTask(c2)
	if other.Code != http.StatusNotFound {
		t.Fatalf("missing job: status %d", other.Code)
	}
}

func TestConsoleTaskSpecReadsTheProviderShape(t *testing.T) {
	// A tasks-preset provider body as stored on the job.
	tasks := consoleTaskSpec(map[string]any{
		"duration": float64(4), "resolution": "480p", "size": "adaptive",
		"video_urls": []any{"https://x/v.mp4"},
		"image_with_roles": []any{
			map[string]any{"url": "https://x/a.png", "role": "first_frame"},
			map[string]any{"url": "https://x/b.png", "role": "last_frame"},
		},
	})
	if tasks.Seconds != 4 || tasks.Resolution != "480p" || tasks.AspectRatio != "adaptive" || tasks.ReferenceVideos != 1 || tasks.Frames != 2 {
		t.Fatalf("tasks shape = %+v", tasks)
	}
	// Ark keeps everything in a content list, and the prompt with it.
	ark := map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "a fox"},
		map[string]any{"type": "image_url", "role": "reference_image"},
		map[string]any{"type": "audio_url"},
	}}
	if spec := consoleTaskSpec(ark); spec.ReferenceImages != 1 || spec.ReferenceAudios != 1 {
		t.Fatalf("ark shape = %+v", spec)
	}
	if prompt := consoleTaskPrompt(ark); prompt != "a fox" {
		t.Fatalf("ark prompt = %q", prompt)
	}
	if spec := consoleTaskSpec(map[string]any{"size": "1280x720"}); spec.Resolution != "1280x720" || spec.AspectRatio != "" {
		t.Fatalf("pixel size = %+v", spec)
	}
}

func TestConsoleTaskRequestHidesRoutingLanes(t *testing.T) {
	job := model.MediaJob{RequestSnapshot: model.JSONValue(`{"prompt":"x","channel_group":"direct","final_group":"direct","_billing_reserved_quota_microusd":1}`)}
	request := string(job.ToDTO(true).Request)
	if request != `{"prompt":"x"}` {
		t.Fatalf("public request = %s", request)
	}
}

func TestAdminTasksListEveryAccountWithItsUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	now := time.Now().UTC()
	for _, user := range []model.User{{Id: 701, Username: "alice"}, {Id: 702, Username: "bob"}} {
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		seedConsoleTask(t, db, user.Id, nil, "req-admin-"+user.Username, model.MediaJobStatusRunning,
			`{"prompt":"clip by `+user.Username+`","seconds":4}`, `{}`, "", now)
	}
	get := func(query string) (body struct {
		Data struct {
			Total int `json:"total"`
			Items []struct {
				UserID   int    `json:"user_id"`
				Username string `json:"username"`
			} `json:"items"`
		} `json:"data"`
	}) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/log/tasks?"+query, nil)
		GetAllTasks(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status %d body=%s", recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	all := get("")
	names := map[string]int{}
	for _, item := range all.Data.Items {
		names[item.Username] = item.UserID
	}
	if all.Data.Total != 2 || names["alice"] != 701 || names["bob"] != 702 {
		t.Fatalf("admin list = %+v", all.Data)
	}
	if one := get("user_id=702"); one.Data.Total != 1 || one.Data.Items[0].Username != "bob" {
		t.Fatalf("user filter = %+v", one.Data)
	}
}
