package files

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/storage"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeObjectStorage struct {
	objects map[string][]byte
}

func (s *fakeObjectStorage) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = data
	return nil
}

func (s *fakeObjectStorage) PutFromFile(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *fakeObjectStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}

func (s *fakeObjectStorage) Head(_ context.Context, key string) (storage.ObjectMeta, error) {
	data, ok := s.objects[key]
	if !ok {
		return storage.ObjectMeta{}, nil
	}
	return storage.ObjectMeta{Exists: true, ContentLength: int64(len(data))}, nil
}

func (s *fakeObjectStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStorage) ListByPrefix(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func setupDereferenceTest(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	previousDB := model.CoreDB()
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.User{}, &model.File{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user := model.User{Id: 1, Username: "alice", Password: "password123", Quota: billing.USD(1), Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	fakeStorage := &fakeObjectStorage{objects: map[string][]byte{"files/1/pdf": []byte("%PDF-1.7\nhello")}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	now := service.NowUnix()
	expiresAt := now + 60
	row := model.File{
		ID:         "file-pdf",
		UserID:     1,
		Filename:   "paper.pdf",
		Purpose:    "user_data",
		MimeType:   "application/pdf",
		Bytes:      int64(len(fakeStorage.objects["files/1/pdf"])),
		StorageKey: "files/1/pdf",
		Status:     model.FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	t.Cleanup(func() {
		restoreStorage()
		model.SetCoreDB(previousDB)
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("id", 1)
	return c
}

func TestDereferenceFileIDsInBodyRewritesNestedResponsesInput(t *testing.T) {
	c := setupDereferenceTest(t)
	body := []byte(`{"model":"gpt-4.1","input":[{"role":"user","content":[{"type":"input_text","text":"read this"},{"type":"input_file","file_id":"file-pdf"}]}]}`)
	rewritten, changed, err := DereferenceFileIDsInBody(c, body)
	if err != nil {
		t.Fatalf("dereference: %v", err)
	}
	if !changed {
		t.Fatal("expected body to be rewritten")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(rewritten, &decoded); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	input := decoded["input"].([]interface{})
	message := input[0].(map[string]interface{})
	content := message["content"].([]interface{})
	fileItem := content[1].(map[string]interface{})
	if _, exists := fileItem["file_id"]; exists {
		t.Fatalf("file_id should be removed: %#v", fileItem)
	}
	if fileItem["filename"] != "paper.pdf" {
		t.Fatalf("unexpected filename: %#v", fileItem)
	}
	dataURL, _ := fileItem["file_data"].(string)
	if dataURL != "data:application/pdf;base64,JVBERi0xLjcKaGVsbG8=" {
		t.Fatalf("unexpected data URL: %s", dataURL)
	}
}

func TestDereferenceFileIDsInBodyReturnsNotFound(t *testing.T) {
	c := setupDereferenceTest(t)
	body := []byte(`{"model":"gpt-4.1","input":[{"type":"input_file","file_id":"file-missing"}]}`)
	_, _, err := DereferenceFileIDsInBody(c, body)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if code := types.ErrCode(err); code != types.ErrorCodeFileNotFound {
		t.Fatalf("unexpected code %q: %v", code, err)
	}
}

func TestDereferenceFileIDsAnswersMissingFileInOpenAIEnvelope(t *testing.T) {
	setupDereferenceTest(t)
	body := []byte(`{"model":"gpt-4.1","input":[{"type":"input_file","file_id":"file-missing"}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 1)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	DereferenceFileIDs()(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Error struct {
			Code  string `json:"code"`
			Param string `json:"param"`
			Lazu  string `json:"lazu"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v body=%s", err, recorder.Body.String())
	}
	if envelope.Error.Code != "file_not_found" || envelope.Error.Param != "file_id" || envelope.Error.Lazu == "" {
		t.Fatalf("unexpected error envelope: %s", recorder.Body.String())
	}
	if !c.IsAborted() {
		t.Fatal("expected the chain to stop")
	}
}

func TestDereferenceFileIDsInBodyRejectsOversizedFile(t *testing.T) {
	c := setupDereferenceTest(t)
	expiresAt := service.NowUnix() + 60
	row := model.File{
		ID:         "file-huge",
		UserID:     1,
		Filename:   "huge.pdf",
		Purpose:    "user_data",
		MimeType:   "application/pdf",
		Bytes:      maxDereferenceBytes + 1,
		StorageKey: "files/1/huge",
		Status:     model.FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  service.NowUnix(),
	}
	if err := model.CoreDB().Create(&row).Error; err != nil {
		t.Fatalf("create huge file: %v", err)
	}

	body := []byte(`{"model":"gpt-4.1","input":[{"type":"input_file","file_id":"file-huge"}]}`)
	_, _, err := DereferenceFileIDsInBody(c, body)
	if err == nil {
		t.Fatal("expected oversized file error")
	}
	if code := types.ErrCode(err); code != types.ErrorCodeFileTooLarge {
		t.Fatalf("unexpected code %q: %v", code, err)
	}
}
