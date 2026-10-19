package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/storage"
)

const (
	ossURLContextKey    = "oss_url_values"
	ossDownloadCacheKey = "oss_url_download_cache"
)

type OSSURLFile struct {
	Base64Data string
	MimeType   string
	FileName   string
	LocalPath  string
	Size       int64
	Persisted  bool
	Bytes      []byte
}

type ossCacheMeta struct {
	URL         string `json:"url"`
	FileName    string `json:"file_name"`
	LocalPath   string `json:"local_path"`
	MimeType    string `json:"mime_type"`
	ObjectKey   string `json:"object_key"`
	CreatedAt   int64  `json:"created_at"`
	UploadedAt  int64  `json:"uploaded_at"`
	UploadError string `json:"upload_error,omitempty"`
	Size        int64  `json:"size_bytes"`
	Model       string `json:"model_name"`
}

var (
	ossCacheCleanerOnce sync.Once
	ossStorageClient    storage.ObjectStorage
	ossStorageErr       error
	ossStorageLock      sync.Mutex
)

// InitOSSURLCache starts the janitor routine once when OSS URL caching is enabled.
func InitOSSURLCache() {
	if !common.AppConfig.OSSURLCache.Enable {
		return
	}
	ossCacheCleanerOnce.Do(func() {
		common.SafeGo("oss-url-cache-cleaner", func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				cleanupOSSCache()
			}
		})
	})
}

func cleanupOSSCache() {
	cacheDir := common.GetOSSURLCacheDir()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	ttl := time.Duration(common.AppConfig.OSSURLCache.CacheTTLMinute) * time.Minute
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		metaPath := filepath.Join(cacheDir, entry.Name())
		meta, err := readCacheMeta(metaPath)
		if err != nil {
			os.Remove(metaPath)
			continue
		}
		created := time.Unix(meta.CreatedAt, 0)
		if now.Before(created.Add(ttl)) {
			continue
		}
		if meta.UploadedAt == 0 {
			// still waiting for upload confirmation
			continue
		}
		if meta.LocalPath != "" {
			_ = os.Remove(meta.LocalPath)
		}
		_ = os.Remove(metaPath)
	}
}

func readCacheMeta(path string) (*ossCacheMeta, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta ossCacheMeta
	if err := json.Unmarshal(bytes, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeCacheMeta(meta *ossCacheMeta) error {
	if meta == nil || meta.LocalPath == "" {
		return fmt.Errorf("invalid cache meta")
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPathFor(meta.LocalPath), data, 0o600)
}

func metaPathFor(localPath string) string {
	return localPath + ".meta"
}

func downloadAndStageOSSFile(requestID string, rawURL string, modelName string) (*OSSURLFile, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("oss_url is empty")
	}
	ctx := ctxWithRequestID(requestID)
	resp, err := DoDownloadRequest(rawURL, "oss_url")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxSize := constant.MaxFileDownloadMB * 1024 * 1024
	reader := io.LimitReader(resp.Body, int64(maxSize+1))
	fileBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(fileBytes) > maxSize {
		return nil, fmt.Errorf("file size exceeds maximum allowed size: %dMB", constant.MaxFileDownloadMB)
	}

	mimeType := sanitizeMimeType(resp.Header.Get("Content-Type"), rawURL)
	base64Data := base64.StdEncoding.EncodeToString(fileBytes)
	fileName := guessFileName(rawURL, mimeType)

	downloaded := &OSSURLFile{
		Base64Data: base64Data,
		MimeType:   mimeType,
		FileName:   fileName,
		Size:       int64(len(fileBytes)),
		Bytes:      fileBytes,
	}

	localPath, meta, err := persistOSSFile(fileBytes, fileName, rawURL, mimeType, modelName)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to persist OSS_URL file: %v", err))
		return downloaded, nil
	}
	downloaded.LocalPath = localPath
	downloaded.Persisted = true
	if !shouldPersistOSSFile() {
		meta.UploadedAt = meta.CreatedAt
		_ = writeCacheMeta(meta)
		return downloaded, nil
	}
	if err := writeCacheMeta(meta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to write OSS_URL meta: %v", err))
	} else {
		enqueueOSSUpload(requestID, meta)
	}

	return downloaded, nil
}

func sanitizeMimeType(header string, rawURL string) string {
	mimeType := strings.TrimSpace(header)
	if mimeType == "" || mimeType == "application/octet-stream" {
		if guessed, err := detectMimeFromURL(rawURL); err == nil && guessed != "" {
			return guessed
		}
	}
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = mimeType[:idx]
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return mimeType
}

func detectMimeFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("no filename")
	}
	last := parts[len(parts)-1]
	if dot := strings.LastIndex(last, "."); dot != -1 && dot+1 < len(last) {
		ext := strings.ToLower(last[dot+1:])
		if mt := GetMimeTypeByExtension(ext); mt != "application/octet-stream" {
			return mt, nil
		}
	}
	return "", fmt.Errorf("unable to detect mime")
}

func guessFileName(rawURL string, mimeType string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		if base := filepath.Base(parsed.Path); base != "" && base != "." {
			return sanitizeFileName(base)
		}
	}
	ext := extensionFromMime(mimeType)
	return fmt.Sprintf("oss-file-%d%s", time.Now().UnixNano(), ext)
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	name = strings.ReplaceAll(name, "..", "")
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}
	name = strings.Map(repl, name)
	if len(name) > 80 {
		ext := filepath.Ext(name)
		name = name[:80-len(ext)] + ext
	}
	return name
}

func extensionFromMime(mime string) string {
	if mime == "" {
		return ""
	}
	if strings.HasPrefix(mime, "image/") {
		return "." + strings.TrimPrefix(mime, "image/")
	}
	if strings.HasPrefix(mime, "audio/") {
		return "." + strings.TrimPrefix(mime, "audio/")
	}
	if strings.HasPrefix(mime, "video/") {
		return "." + strings.TrimPrefix(mime, "video/")
	}
	switch mime {
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	}
	return ""
}

func persistOSSFile(data []byte, fileName, rawURL, mimeType, modelName string) (string, *ossCacheMeta, error) {
	cacheDir := common.GetOSSURLCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", nil, err
	}
	safeName := sanitizeFileName(fileName)
	hash := shortHash(rawURL + time.Now().String())
	finalName := fmt.Sprintf("%d_%s_%s", time.Now().UnixNano(), hash[:8], safeName)
	localPath := filepath.Join(cacheDir, finalName)
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return "", nil, err
	}
	createdAt := time.Now().Unix()
	meta := &ossCacheMeta{
		URL:       rawURL,
		FileName:  finalName,
		LocalPath: localPath,
		MimeType:  mimeType,
		CreatedAt: createdAt,
		Size:      int64(len(data)),
		Model:     sanitizeModelName(modelName),
	}
	meta.ObjectKey = buildObjectKey(finalName, createdAt, meta.Model)
	return localPath, meta, nil
}

func shortHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func sanitizeModelName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "unknown"
	}
	replacer := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '-'
		}
	}
	name = strings.Map(replacer, name)
	name = strings.Trim(name, "-")
	if name == "" {
		return "unknown"
	}
	return name
}

func buildObjectKey(fileName string, createdAt int64, model string) string {
	prefix := strings.Trim(common.AppConfig.OSSURLCache.RequestFilePath, "/")
	if prefix == "" {
		prefix = "request_file"
	}
	if model == "" {
		model = "unknown"
	}
	dateSeg := time.Unix(createdAt, 0).UTC().Format("2006-01")
	parts := []string{prefix, model, dateSeg, fileName}
	return path.Join(parts...)
}

func enqueueOSSUpload(requestID string, meta *ossCacheMeta) {
	if meta == nil {
		return
	}
	common.SafeGo("oss-url-cache-upload", func() {
		ctx := ctxWithRequestID(requestID)
		client, err := ensureOSSStorageClient()
		if err != nil {
			meta.UploadError = err.Error()
			_ = writeCacheMeta(meta)
			logger.LogWarn(ctx, fmt.Sprintf("OSS_URL upload skipped: %v", err))
			return
		}
		file, err := os.Open(meta.LocalPath)
		if err != nil {
			meta.UploadError = err.Error()
			_ = writeCacheMeta(meta)
			logger.LogError(ctx, fmt.Sprintf("OSS_URL upload open failed: %v", err))
			return
		}
		defer file.Close()
		err = client.Put(context.Background(), meta.ObjectKey, file, meta.MimeType)
		if err != nil {
			meta.UploadError = err.Error()
			_ = writeCacheMeta(meta)
			logger.LogError(ctx, fmt.Sprintf("OSS_URL upload failed: %v", err))
			return
		}
		meta.UploadError = ""
		meta.UploadedAt = time.Now().Unix()
		_ = writeCacheMeta(meta)
		logger.LogInfo(ctx, fmt.Sprintf("OSS_URL upload completed: %s", meta.ObjectKey))
	})
}

func ensureOSSStorageClient() (storage.ObjectStorage, error) {
	ossStorageLock.Lock()
	defer ossStorageLock.Unlock()
	if ossStorageClient != nil || ossStorageErr != nil {
		return ossStorageClient, ossStorageErr
	}
	client, err := storage.NewClient(ossURLStorageConfig())
	if err != nil {
		ossStorageErr = err
		return nil, err
	}
	ossStorageClient = client
	return ossStorageClient, nil
}

func ossURLStorageConfig() storage.S3Config {
	// Prefer OSSURLCache's own config, fallback to ArtifactStorage config
	ossCfg := common.AppConfig.OSSURLCache
	if ossCfg.S3.IsConfigured() {
		return ossCfg.S3
	}
	return common.AppConfig.ArtifactStorage.S3
}

func shouldPersistOSSFile() bool {
	cfg := common.AppConfig.OSSURLCache
	if !cfg.Enable {
		return false
	}
	return ossURLStorageConfig().IsConfigured()
}

func ctxWithRequestID(requestID string) context.Context {
	if requestID == "" {
		requestID = "OSS_URL"
	}
	return context.WithValue(context.Background(), common.RequestIdKey, requestID)
}
