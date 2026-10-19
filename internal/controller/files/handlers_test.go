package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/storage"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeObjectStorage struct {
	objects   map[string]fakeStoredObject
	deleteErr error
}

type fakeStoredObject struct {
	contentType string
	data        []byte
}

func newFakeObjectStorage() *fakeObjectStorage {
	return &fakeObjectStorage{objects: make(map[string]fakeStoredObject)}
}

func (s *fakeObjectStorage) Put(_ context.Context, key string, r io.Reader, contentType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = fakeStoredObject{contentType: contentType, data: data}
	return nil
}

func (s *fakeObjectStorage) PutFromFile(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *fakeObjectStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key].data)), nil
}

func (s *fakeObjectStorage) Head(_ context.Context, key string) (storage.ObjectMeta, error) {
	item, ok := s.objects[key]
	if !ok {
		return storage.ObjectMeta{}, nil
	}
	return storage.ObjectMeta{Exists: true, ContentLength: int64(len(item.data))}, nil
}

func (s *fakeObjectStorage) Delete(_ context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStorage) ListByPrefix(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func setupFilesControllerTest(t *testing.T) (*gin.Engine, *fakeObjectStorage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	previousDB := model.CoreDB()
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.File{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user := model.User{Id: 1, Username: "alice", Password: "password123", Quota: billing.USD(1), Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	fakeStorage := newFakeObjectStorage()
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	previousBasePath := common.AppConfig.ArtifactStorage.BasePath
	common.AppConfig.ArtifactStorage.BasePath = "user-files"
	t.Cleanup(func() {
		restoreStorage()
		common.AppConfig.ArtifactStorage.BasePath = previousBasePath
		model.SetCoreDB(previousDB)
	})

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("token_id", 7)
		c.Next()
	})
	router.POST("/v1/files", Upload)
	router.GET("/v1/files", List)
	router.GET("/v1/files/:id", Retrieve)
	router.GET("/v1/files/:id/content", Content)
	router.DELETE("/v1/files/:id", Delete)
	return router, fakeStorage
}

func TestUploadListContentAndDeleteFile(t *testing.T) {
	router, fakeStorage := setupFilesControllerTest(t)

	body, contentType := multipartBody(t, "paper.pdf", "user_data", []byte("%PDF-1.7\nhello"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	fileID, _ := created["id"].(string)
	if fileID == "" {
		t.Fatalf("missing file id in response: %#v", created)
	}
	if created["object"] != "file" || created["purpose"] != "user_data" {
		t.Fatalf("unexpected file object: %#v", created)
	}
	if len(fakeStorage.objects) != 1 {
		t.Fatalf("expected one stored object, got %d", len(fakeStorage.objects))
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/files", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var list struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if list.Object != "list" || len(list.Data) != 1 || list.Data[0]["id"] != fileID {
		t.Fatalf("unexpected list response: %#v", list)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/files/"+fileID+"/content", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "%PDF-1.7\nhello" {
		t.Fatalf("unexpected content %q", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("unexpected content type %q", got)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/files/"+fileID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	if len(fakeStorage.objects) != 0 {
		t.Fatalf("expected storage cleanup, got %d objects", len(fakeStorage.objects))
	}
	var user model.User
	if err := model.CoreDB().Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed != 0 {
		t.Fatalf("expected storage bytes to be released, got %d", user.StorageBytesUsed)
	}
}

func TestDeleteFileStopsWhenStorageDeleteFails(t *testing.T) {
	router, fakeStorage := setupFilesControllerTest(t)

	body, contentType := multipartBody(t, "paper.pdf", "user_data", []byte("%PDF-1.7\nhello"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	fileID := created["id"].(string)
	fakeStorage.deleteErr = errors.New("delete failed")

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/files/"+fileID, nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	if len(fakeStorage.objects) != 1 {
		t.Fatalf("expected stored object to remain, got %d objects", len(fakeStorage.objects))
	}
	var count int64
	if err := model.CoreDB().Model(&model.File{}).Where("id = ?", fileID).Count(&count).Error; err != nil {
		t.Fatalf("count file: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected file record to remain, got count=%d", count)
	}
	var user model.User
	if err := model.CoreDB().Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed == 0 {
		t.Fatal("expected storage bytes to remain charged")
	}
}

func TestUploadRejectsUnsupportedPurpose(t *testing.T) {
	router, _ := setupFilesControllerTest(t)
	body, contentType := multipartBody(t, "batch.jsonl", "batch", []byte("{}\n"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("purpose_not_supported")) {
		t.Fatalf("expected purpose_not_supported, body=%s", w.Body.String())
	}
}

func TestUploadRejectsNonImageVisionFile(t *testing.T) {
	router, _ := setupFilesControllerTest(t)
	body, contentType := multipartBody(t, "paper.pdf", "vision", []byte("%PDF-1.7\nhello"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"code":"invalid_file"`)) || !bytes.Contains(w.Body.Bytes(), []byte("vision files must be png")) {
		t.Fatalf("expected invalid_file with the vision mime reason, body=%s", w.Body.String())
	}
}

func TestUploadRejectsBodyOverCapBeforeParsing(t *testing.T) {
	router, fakeStorage := setupFilesControllerTest(t)
	body, contentType := multipartBody(t, "paper.pdf", "user_data", []byte("%PDF-1.7\nhello"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = maxUploadBodyBytes + 1
	router.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("file_too_large")) {
		t.Fatalf("expected file_too_large, body=%s", w.Body.String())
	}
	if len(fakeStorage.objects) != 0 {
		t.Fatalf("expected no stored objects, got %d", len(fakeStorage.objects))
	}
}

func TestRetrieveReturns404ForOtherOwner(t *testing.T) {
	router, _ := setupFilesControllerTest(t)
	expiresAt := service.DefaultFileExpiresAt(service.NowUnix())
	row := model.File{
		ID:         "file-owner2",
		UserID:     2,
		Filename:   "other.pdf",
		Purpose:    "user_data",
		MimeType:   "application/pdf",
		Bytes:      4,
		StorageKey: "other",
		Status:     model.FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  service.NowUnix(),
	}
	if err := model.CoreDB().Create(&row).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/files/file-owner2", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
			Lazu string `json:"lazu"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error.Code != "file_not_found" || envelope.Error.Type != "not_found_error" || envelope.Error.Lazu != "LZ-7804" {
		t.Fatalf("unexpected error envelope: %s", w.Body.String())
	}
}

func TestDeleteStorageFailureIsFileStorageFailed(t *testing.T) {
	router, fakeStorage := setupFilesControllerTest(t)
	body, contentType := multipartBody(t, "paper.pdf", "user_data", []byte("%PDF-1.7\nhello"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	fakeStorage.deleteErr = errors.New("bucket unreachable")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/files/"+created["id"].(string), nil))
	if w.Code != http.StatusBadGateway || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"file_storage_failed"`)) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("bucket unreachable")) {
		t.Fatalf("storage cause leaked to the client: %s", w.Body.String())
	}
}

func multipartBody(t *testing.T, filename string, purpose string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("purpose", purpose); err != nil {
		t.Fatalf("write purpose: %v", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body, writer.FormDataContentType()
}

func TestProjectSuspendedMemberCannotListOrReadFiles(t *testing.T) {
	router, _ := setupFilesControllerTest(t)
	t.Setenv("PROJECTS_ENABLED", "true")
	if err := model.CoreDB().AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	project := model.Project{OwnerUserID: 2, Name: "files project"}
	if err := model.CoreDB().Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	member := model.ProjectMember{ProjectID: project.ID, UserID: 1, Role: model.ProjectRoleMember, Status: model.ProjectMemberSuspended}
	if err := model.CoreDB().Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	router.GET("/api/files", ConsoleList)
	router.GET("/api/files/:id/content", ConsoleContent)
	for _, path := range []string{"/api/files", "/api/files/file-private/content"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path+"?project_id="+project.PublicID, nil))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	if err := model.CoreDB().Model(&member).Update("status", model.ProjectMemberActive).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/files?project_id="+project.PublicID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("active list: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestConsoleFileReadDoesNotGrantDeletion(t *testing.T) {
	_, storage := setupFilesControllerTest(t)
	db := model.CoreDB()
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: 9, OwnerUserID: 1, Name: "Files"}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, member := range []model.ProjectMember{{ProjectID: 9, UserID: 1, Role: model.ProjectRoleOwner, Status: model.ProjectMemberActive}, {ProjectID: 9, UserID: 2, Role: model.ProjectRoleAdmin, Status: model.ProjectMemberActive}, {ProjectID: 9, UserID: 3, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}} {
		if err := db.Create(&member).Error; err != nil {
			t.Fatal(err)
		}
	}
	key := 7
	if err := db.Create(&model.Token{Id: key, ProjectID: 9, UserId: 3, Key: "member-delete-test"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, file := range []model.File{{ID: "personal", ProjectID: 9, PayerUserID: 1, UserID: 3, ActorUserID: 3, StorageKey: "personal"}, {ID: "second", ProjectID: 9, PayerUserID: 1, UserID: 3, ActorUserID: 3, TokenID: &key, StorageKey: "second"}} {
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		storage.objects[file.StorageKey] = fakeStoredObject{}
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	request := func(actor int, method, id string) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("id", actor) })
		r.GET("/files/:id", func(c *gin.Context) {
			if prepareConsoleFileAccess(c, "") {
				Retrieve(c)
			}
		})
		r.DELETE("/files/:id", ConsoleDelete)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/files/"+id+"?project_id="+project.PublicID, nil))
		return w
	}
	for _, actor := range []int{1, 2} {
		for _, id := range []string{"personal", "second"} {
			if w := request(actor, "GET", id); w.Code != 200 {
				t.Fatalf("manager cannot read: %s", w.Body.String())
			}
			if w := request(actor, "DELETE", id); w.Code != 403 {
				t.Fatalf("manager deleted other's file: %d %s", w.Code, w.Body.String())
			}
		}
	}
	if len(storage.objects) != 2 {
		t.Fatal("denied deletion removed storage")
	}
	if w := request(3, "DELETE", "personal"); w.Code != 200 {
		t.Fatalf("actor delete failed: %s", w.Body.String())
	}
	// An actorless principal owns nothing; only the original actor may delete.
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 0)
		c.Set("token_id", 7)
		c.Set("console_resource_principal", model.ProjectPrincipal{ProjectID: 9, PayerUserID: 1, TokenID: 7})
	})
	r.DELETE("/files/:id", Delete)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/files/second", nil))
	if w.Code == 200 {
		t.Fatalf("actorless principal deleted a member file: %s", w.Body.String())
	}
	if _, ok := storage.objects["second"]; !ok {
		t.Fatal("denied deletion removed storage")
	}
}

// Two Keys of one member are two logical Keys. Neither may delete the other's
// uploads, and neither may reach a console upload that carries no token id.
// The console principal has no token id and keeps deleting its own rows.
func TestKeyDeletionIsBoundedByItsOwnLogicalKey(t *testing.T) {
	_, storage := setupFilesControllerTest(t)
	db := model.CoreDB()
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: 12, OwnerUserID: 1, Name: "Sibling keys"}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, member := range []model.ProjectMember{
		{ProjectID: project.ID, UserID: 1, Role: model.ProjectRoleOwner, Status: model.ProjectMemberActive},
		{ProjectID: project.ID, UserID: 3, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive},
	} {
		if err := db.Create(&member).Error; err != nil {
			t.Fatal(err)
		}
	}
	keyA, keyB := 71, 72
	for _, id := range []int{keyA, keyB} {
		if err := db.Create(&model.Token{Id: id, ProjectID: project.ID, UserId: 3, Key: fmt.Sprintf("sibling-key-%d", id)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []model.File{
		{ID: "key-a-file", ProjectID: project.ID, PayerUserID: 1, UserID: 3, ActorUserID: 3, TokenID: &keyA, StorageKey: "key-a-file"},
		{ID: "console-file", ProjectID: project.ID, PayerUserID: 1, UserID: 3, ActorUserID: 3, StorageKey: "console-file"},
	} {
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		storage.objects[file.StorageKey] = fakeStoredObject{}
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	apiDelete := func(tokenID int, id string) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("id", 3)
			c.Set("token_id", tokenID)
			common.SetContextKey(c, constant.ContextKeyProjectID, project.ID)
			common.SetContextKey(c, constant.ContextKeyPayerUserID, 1)
		})
		r.DELETE("/v1/files/:id", Delete)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/files/"+id, nil))
		return w
	}
	if w := apiDelete(keyB, "key-a-file"); w.Code == http.StatusOK {
		t.Fatalf("a sibling Key deleted another Key's file: %s", w.Body.String())
	}
	if w := apiDelete(keyA, "console-file"); w.Code == http.StatusOK {
		t.Fatalf("a Key deleted a console upload it never produced: %s", w.Body.String())
	}
	for _, key := range []string{"key-a-file", "console-file"} {
		if _, ok := storage.objects[key]; !ok {
			t.Fatalf("denied deletion removed %s from storage", key)
		}
	}
	// The delete predicate is the authorization, not just a mirror of the read
	// scope: assert it directly so a loosened predicate cannot hide behind the
	// scope's 404.
	consoleFile := model.File{ID: "console-file", ProjectID: project.ID, ActorUserID: 3}
	keyAFile := model.File{ID: "key-a-file", ProjectID: project.ID, ActorUserID: 3, TokenID: &keyA}
	actorlessFile := model.File{ID: "actorless", ProjectID: project.ID, ActorUserID: 0, TokenID: &keyA}
	for _, tc := range []struct {
		name      string
		file      model.File
		principal model.ProjectPrincipal
		want      bool
	}{
		{"sibling key", keyAFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3, TokenID: keyB}, false},
		{"own key", keyAFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3, TokenID: keyA}, true},
		{"key on console upload", consoleFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3, TokenID: keyA}, false},
		{"console on own upload", consoleFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3}, true},
		{"console on own key upload", keyAFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3}, true},
		{"console of another member", keyAFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 2}, false},
		// An actorless row is nobody's: the project migration attributes every
		// Key, so no credential inherits a row that carries no actor.
		{"own key on an actorless row", actorlessFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 3, TokenID: keyA}, false},
		{"sibling key on an actorless row", actorlessFile, model.ProjectPrincipal{ProjectID: project.ID, ActorUserID: 1, TokenID: keyB}, false},
		{"other project", keyAFile, model.ProjectPrincipal{ProjectID: 99, ActorUserID: 3, TokenID: keyA}, false},
	} {
		file := tc.file
		if got := mayDeleteProjectFile(&file, tc.principal); got != tc.want {
			t.Fatalf("%s: mayDeleteProjectFile=%v want %v", tc.name, got, tc.want)
		}
	}
	// The owning Key still cleans up after itself.
	if w := apiDelete(keyA, "key-a-file"); w.Code != http.StatusOK {
		t.Fatalf("owning Key could not delete its own file: %d %s", w.Code, w.Body.String())
	}
	if _, ok := storage.objects["key-a-file"]; ok {
		t.Fatal("deleted file left in storage")
	}
}

func TestUploadOverStorageQuotaIsRefusedAndRolledBack(t *testing.T) {
	router, fakeStorage := setupFilesControllerTest(t)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap[model.FileStorageQuotaBytesOption]
	common.OptionMap[model.FileStorageQuotaBytesOption] = "20"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[model.FileStorageQuotaBytesOption] = previous
		} else {
			delete(common.OptionMap, model.FileStorageQuotaBytesOption)
		}
	})

	body, contentType := multipartBody(t, "paper.pdf", "user_data", []byte("%PDF-1.7\nthis file is larger than twenty bytes"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("upload over quota status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.Error.Code != "storage_quota_exceeded" {
		t.Fatalf("upload over quota body=%s err=%v", w.Body.String(), err)
	}
	if len(fakeStorage.objects) != 0 {
		t.Fatalf("refused upload left %d stored objects", len(fakeStorage.objects))
	}
	var user model.User
	if err := model.CoreDB().Select("quota", "storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.StorageBytesUsed != 0 || user.Quota != billing.USD(1) {
		t.Fatalf("refused upload changed bytes=%d quota=%d", user.StorageBytesUsed, user.Quota)
	}
}
