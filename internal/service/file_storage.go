package service

import (
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/storage"
)

// FileStorageTTLSeconds is how long an uploaded file or generated artifact
// is kept before the cleanup job removes it. Storage is not billed.
const FileStorageTTLSeconds = int64(30 * 24 * 60 * 60)

var (
	userFileStorageLock   sync.Mutex
	userFileStorageClient storage.ObjectStorage
	userFileStorageErr    error
)

func EnsureUserFileStorageClient() (storage.ObjectStorage, error) {
	userFileStorageLock.Lock()
	defer userFileStorageLock.Unlock()
	if userFileStorageClient != nil || userFileStorageErr != nil {
		return userFileStorageClient, userFileStorageErr
	}
	if !common.AppConfig.ArtifactStorage.Enable {
		userFileStorageErr = fmt.Errorf("artifact_storage is disabled")
		return nil, userFileStorageErr
	}
	artCfg := common.AppConfig.ArtifactStorage
	client, err := storage.NewClient(artCfg.S3)
	if err != nil {
		userFileStorageErr = err
		return nil, err
	}
	userFileStorageClient = client
	return userFileStorageClient, nil
}

func SetUserFileStorageClientForTest(client storage.ObjectStorage) func() {
	userFileStorageLock.Lock()
	previousClient := userFileStorageClient
	previousErr := userFileStorageErr
	userFileStorageClient = client
	userFileStorageErr = nil
	userFileStorageLock.Unlock()
	return func() {
		userFileStorageLock.Lock()
		userFileStorageClient = previousClient
		userFileStorageErr = previousErr
		userFileStorageLock.Unlock()
	}
}

func ResetUserFileStorageClient() {
	userFileStorageLock.Lock()
	userFileStorageClient = nil
	userFileStorageErr = nil
	userFileStorageLock.Unlock()
}

func BuildUserFileStorageKey(userPublicID string, purpose string, now time.Time) string {
	basePath := strings.Trim(common.AppConfig.ArtifactStorage.BasePath, "/")
	deployment := basePath
	if deployment == "" {
		deployment = "lazu"
	}
	parts := make([]string, 0, 5)
	parts = append(parts, deployment)
	parts = append(parts, "files", filePurposeShort(purpose), userPublicID, now.Format("2006-01"), uuid.NewString())
	return path.Join(parts...)
}

func filePurposeShort(purpose string) string {
	switch strings.TrimSpace(purpose) {
	case "vision":
		return "img"
	case "user_data":
		return "doc"
	default:
		return "misc"
	}
}

func DefaultFileExpiresAt(now int64) int64 {
	return now + FileStorageTTLSeconds
}

func NowUnix() int64 {
	return time.Now().Unix()
}
