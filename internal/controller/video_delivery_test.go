package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/storage"
	"gorm.io/gorm"
)

type signingVideoStorage struct {
	objects    map[string][]byte
	signedAt   time.Duration
	signedName string
}

func (s *signingVideoStorage) Put(context.Context, string, io.Reader, string) error { return nil }
func (s *signingVideoStorage) PutFromFile(context.Context, string, string) error    { return nil }
func (s *signingVideoStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}
func (s *signingVideoStorage) Head(context.Context, string) (storage.ObjectMeta, error) {
	return storage.ObjectMeta{Exists: true}, nil
}
func (s *signingVideoStorage) Delete(context.Context, string) error { return nil }
func (s *signingVideoStorage) ListByPrefix(context.Context, string) ([]storage.ObjectInfo, error) {
	return nil, nil
}
func (s *signingVideoStorage) PresignGet(_ context.Context, key string, ttl time.Duration, filename string, _ string) (string, error) {
	s.signedAt = ttl
	s.signedName = filename
	return "https://bucket.example/" + key + "?signed=1", nil
}

func setupVideoDeliveryTest(t *testing.T) (*gin.Engine, *model.Token, *gorm.DB, *signingVideoStorage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	store := &signingVideoStorage{objects: map[string][]byte{"videos/clip.mp4": []byte("mp4-bytes")}}
	t.Cleanup(service.SetUserFileStorageClientForTest(store))
	engine := gin.New()
	engine.GET("/v1/videos/:id", middleware.TokenAuth(), GetVideoJob)
	engine.GET("/v1/videos/:id/content", middleware.TokenAuth(), GetVideoContent)
	return engine, token, db, store
}

func seedVideoDeliveryJob(t *testing.T, db *gorm.DB, token *model.Token, publicID string, status string, fileExpiresAt int64) {
	t.Helper()
	file := &model.File{
		ID: "file-lazu-" + publicID, UserID: token.UserId, TokenID: &token.Id,
		Filename: "9998-provider-video_task_01ABC.mp4", Purpose: "media_artifact", MimeType: "video/mp4", Bytes: 9,
		StorageKey: "videos/clip.mp4", Status: model.FileStatusProcessed, ExpiresAt: &fileExpiresAt,
		CreatedAt: time.Now().Unix(),
	}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	job := &model.MediaJob{
		PublicID: publicID, UserID: token.UserId, TokenID: &token.Id, Kind: model.MediaJobKindVideo,
		ModelName: "seedance-2.5", Status: status,
	}
	if status == model.MediaJobStatusSucceeded {
		job.ArtifactFileID = file.ID
		job.CompletedAt = time.Now().Unix()
	}
	if err := db.Create(job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
}

func getVideoDelivery(engine *gin.Engine, token *model.Token, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestGetVideoJobHandsOutASignedLinkToTheStoredFile(t *testing.T) {
	engine, token, db, store := setupVideoDeliveryTest(t)
	fileExpiresAt := time.Now().Add(30 * time.Minute).Unix()
	seedVideoDeliveryJob(t, db, token, "mjob_done", model.MediaJobStatusSucceeded, fileExpiresAt)

	w := getVideoDelivery(engine, token, "/v1/videos/mjob_done")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Artifact struct {
			FileID       string `json:"file_id"`
			URL          string `json:"url"`
			URLExpiresAt int64  `json:"url_expires_at"`
			MimeType     string `json:"mime_type"`
			Bytes        int64  `json:"bytes"`
			ExpiresAt    int64  `json:"expires_at"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	a := body.Artifact
	if a.URL != "https://bucket.example/videos/clip.mp4?signed=1" || a.MimeType != "video/mp4" || a.Bytes != 9 || a.ExpiresAt != fileExpiresAt {
		t.Fatalf("artifact = %+v", a)
	}
	// The file goes before the hour is up, so the link ends with it.
	if store.signedName != "mjob_done.mp4" {
		t.Fatalf("link names the file %q, want the job id", store.signedName)
	}
	if a.URLExpiresAt != fileExpiresAt || store.signedAt > 30*time.Minute {
		t.Fatalf("link outlives the file: url_expires_at=%d file=%d ttl=%s", a.URLExpiresAt, fileExpiresAt, store.signedAt)
	}
}

func TestGetVideoContentStreamsOnlyFinishedVideos(t *testing.T) {
	engine, token, db, _ := setupVideoDeliveryTest(t)
	future := time.Now().Add(72 * time.Hour).Unix()
	seedVideoDeliveryJob(t, db, token, "mjob_ready", model.MediaJobStatusSucceeded, future)
	seedVideoDeliveryJob(t, db, token, "mjob_busy", model.MediaJobStatusRunning, future)

	w := getVideoDelivery(engine, token, "/v1/videos/mjob_ready/content")
	if w.Code != http.StatusOK || w.Body.String() != "mp4-bytes" || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content: status %d type %q body %q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename=mjob_ready.mp4` {
		t.Fatalf("content disposition = %q", got)
	}

	w = getVideoDelivery(engine, token, "/v1/videos/mjob_busy/content")
	if w.Code != http.StatusConflict || !bytes.Contains(w.Body.Bytes(), []byte("video_not_ready")) {
		t.Fatalf("unfinished content: status %d body %s", w.Code, w.Body.String())
	}
}

func TestGetVideoJobWaitReturnsAsSoonAsTheJobFinishes(t *testing.T) {
	engine, token, db, _ := setupVideoDeliveryTest(t)
	previousInterval := videoWaitInterval
	videoWaitInterval = 10 * time.Millisecond
	t.Cleanup(func() { videoWaitInterval = previousInterval })
	seedVideoDeliveryJob(t, db, token, "mjob_wait", model.MediaJobStatusRunning, time.Now().Add(time.Hour).Unix())

	go func() {
		time.Sleep(100 * time.Millisecond)
		db.Model(&model.MediaJob{}).Where("public_id = ?", "mjob_wait").Updates(map[string]any{
			"status": model.MediaJobStatusSucceeded, "artifact_file_id": "file-lazu-mjob_wait",
		})
	}()
	started := time.Now()
	w := getVideoDelivery(engine, token, "/v1/videos/mjob_wait?wait=30")
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"succeeded"`)) {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("wait held the request for %s after the job finished", elapsed)
	}

	w = getVideoDelivery(engine, token, "/v1/videos/mjob_wait?wait=61")
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("invalid_wait")) {
		t.Fatalf("wait=61: status %d body=%s", w.Code, w.Body.String())
	}
}

func TestSignFileLinkRefusesAnExpiredFile(t *testing.T) {
	store := &signingVideoStorage{}
	t.Cleanup(service.SetUserFileStorageClientForTest(store))
	past := time.Now().Add(-time.Minute).Unix()
	if _, ok := service.SignFileLink(context.Background(), &model.File{StorageKey: "k", ExpiresAt: &past}, time.Hour, time.Now()); ok {
		t.Fatal("signed a link to an expired file")
	}
}
