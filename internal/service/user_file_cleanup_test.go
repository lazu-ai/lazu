package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/storage"
	"github.com/lazu-ai/lazu/pkg/billing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type cleanupStorage struct {
	deleteErr error
	deleted   []string
}

func (s *cleanupStorage) Put(_ context.Context, _ string, _ io.Reader, _ string) error {
	return nil
}

func (s *cleanupStorage) PutFromFile(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *cleanupStorage) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (s *cleanupStorage) Head(_ context.Context, _ string) (storage.ObjectMeta, error) {
	return storage.ObjectMeta{Exists: true}, nil
}

func (s *cleanupStorage) Delete(_ context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *cleanupStorage) ListByPrefix(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func setupUserFileCleanupTest(t *testing.T) *gorm.DB {
	t.Helper()
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
	user := model.User{
		Id:               1,
		Username:         "alice",
		Password:         "password123",
		Quota:            billing.USD(1),
		Status:           common.UserStatusEnabled,
		StorageBytesUsed: 100,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	expiresAt := int64(100)
	file := model.File{
		ID:         "file-expired",
		UserID:     1,
		Filename:   "expired.pdf",
		Purpose:    "user_data",
		MimeType:   "application/pdf",
		Bytes:      100,
		StorageKey: "files/1/expired",
		Status:     model.FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  1,
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
	})
	return db
}

func TestCleanupExpiredUserFilesKeepsRecordWhenObjectDeleteFails(t *testing.T) {
	db := setupUserFileCleanupTest(t)
	restoreStorage := SetUserFileStorageClientForTest(&cleanupStorage{deleteErr: errors.New("boom")})
	defer restoreStorage()

	stats := CleanupExpiredUserFiles(200)
	if stats.Scanned != 1 || stats.Deleted != 0 || stats.Failed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	var count int64
	if err := db.Model(&model.File{}).Where("id = ?", "file-expired").Count(&count).Error; err != nil {
		t.Fatalf("count file: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected expired file record to remain, got count=%d", count)
	}
	var user model.User
	if err := db.Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed != 100 {
		t.Fatalf("expected storage bytes to remain 100, got %d", user.StorageBytesUsed)
	}
}

func TestDeleteMediaJobArtifactDeletesObjectAndAccountingRecord(t *testing.T) {
	db := setupUserFileCleanupTest(t)
	fakeStorage := &cleanupStorage{}
	restoreStorage := SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()

	var file model.File
	if err := db.First(&file, "id = ?", "file-expired").Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if err := DeleteMediaJobArtifact(context.Background(), &file); err != nil {
		t.Fatalf("DeleteMediaJobArtifact error: %v", err)
	}
	if len(fakeStorage.deleted) != 1 || fakeStorage.deleted[0] != file.StorageKey {
		t.Fatalf("unexpected deleted objects: %+v", fakeStorage.deleted)
	}
	var count int64
	if err := db.Model(&model.File{}).Where("id = ?", file.ID).Count(&count).Error; err != nil {
		t.Fatalf("count file: %v", err)
	}
	if count != 0 {
		t.Fatalf("artifact file record still exists: count=%d", count)
	}
	var user model.User
	if err := db.Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed != 0 {
		t.Fatalf("storage accounting was not rolled back: %d", user.StorageBytesUsed)
	}
}

func TestDeleteMediaJobArtifactKeepsRecordWhenObjectDeleteFails(t *testing.T) {
	db := setupUserFileCleanupTest(t)
	restoreStorage := SetUserFileStorageClientForTest(&cleanupStorage{deleteErr: errors.New("boom")})
	defer restoreStorage()

	var file model.File
	if err := db.First(&file, "id = ?", "file-expired").Error; err != nil {
		t.Fatalf("load file: %v", err)
	}
	if err := DeleteMediaJobArtifact(context.Background(), &file); err == nil {
		t.Fatal("expected object deletion failure")
	}
	var count int64
	if err := db.Model(&model.File{}).Where("id = ?", file.ID).Count(&count).Error; err != nil {
		t.Fatalf("count file: %v", err)
	}
	if count != 1 {
		t.Fatalf("artifact record should remain recoverable, count=%d", count)
	}
}

func TestCleanupExpiredUserFilesDeletesRecordAfterObjectDelete(t *testing.T) {
	db := setupUserFileCleanupTest(t)
	fakeStorage := &cleanupStorage{}
	restoreStorage := SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()

	stats := CleanupExpiredUserFiles(200)
	if stats.Scanned != 1 || stats.Deleted != 1 || stats.Failed != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(fakeStorage.deleted) != 1 || fakeStorage.deleted[0] != "files/1/expired" {
		t.Fatalf("unexpected deleted keys: %#v", fakeStorage.deleted)
	}
	var count int64
	if err := db.Model(&model.File{}).Where("id = ?", "file-expired").Count(&count).Error; err != nil {
		t.Fatalf("count file: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected expired file record deleted, got count=%d", count)
	}
	var user model.User
	if err := db.Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed != 0 {
		t.Fatalf("expected storage bytes decremented, got %d", user.StorageBytesUsed)
	}
}
