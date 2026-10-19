package files

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxDereferenceBytes = int64(64 * 1024 * 1024)

func DereferenceFileIDs() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := common.GetRequestBody(c)
		if err != nil {
			httperr.AbortOpenAI(c, types.NewRequestBodyError(err))
			return
		}
		rewritten, changed, err := DereferenceFileIDsInBody(c, body)
		if err != nil {
			// Catalog failures (file_not_found, file_expired, ...) keep their
			// code; anything else is logged and answered as internal_error.
			httperr.AbortOpenAI(c, err)
			return
		}
		if changed {
			c.Set(common.KeyRequestBody, rewritten)
			c.Request.Body = io.NopCloser(bytes.NewReader(rewritten))
		} else {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}
		c.Next()
	}
}

func DereferenceFileIDsInBody(c *gin.Context, body []byte) ([]byte, bool, error) {
	// Most requests carry no uploaded file references. Avoid another whole-body
	// decode; include escaped keys so the fast path cannot bypass dereferencing.
	if !bytes.Contains(body, []byte("file_id")) && !bytes.Contains(body, []byte(`\u`)) {
		return body, false, nil
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, false, nil
	}
	changed, err := rewriteValue(c, req)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return body, false, nil
	}
	rewritten, err := json.Marshal(req)
	if err != nil {
		return nil, false, err
	}
	return rewritten, true, nil
}

func rewriteValue(c *gin.Context, value any) (bool, error) {
	switch typed := value.(type) {
	case []any:
		changed := false
		for _, item := range typed {
			itemChanged, err := rewriteValue(c, item)
			if err != nil {
				return false, err
			}
			changed = changed || itemChanged
		}
		return changed, nil
	case map[string]any:
		changed, err := rewriteFileReference(c, typed)
		if err != nil {
			return false, err
		}
		for _, child := range typed {
			childChanged, err := rewriteValue(c, child)
			if err != nil {
				return false, err
			}
			changed = changed || childChanged
		}
		return changed, nil
	default:
		return false, nil
	}
}

func rewriteFileReference(c *gin.Context, item map[string]any) (bool, error) {
	itemType, _ := item["type"].(string)
	if itemType != "input_file" && itemType != "input_image" {
		return false, nil
	}
	fileID, _ := item["file_id"].(string)
	if fileID == "" {
		return false, nil
	}
	if !strings.HasPrefix(fileID, "file-") {
		return false, nil
	}

	file, err := model.GetFileByIDAndOwner(fileID, c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, types.Err(types.ErrorCodeFileNotFound, types.D{"field": "file_id", "file_id": fileID})
		}
		return false, err
	}
	if file.ExpiresAt != nil && *file.ExpiresAt < service.NowUnix() {
		return false, types.Err(types.ErrorCodeFileExpired, types.D{"field": "file_id", "file_id": fileID})
	}
	if file.Bytes > maxDereferenceBytes {
		return false, types.Err(types.ErrorCodeFileTooLarge, types.D{
			"field":   "file_id",
			"file_id": fileID,
			"limit":   fmt.Sprintf("%d MB inline", maxDereferenceBytes/(1024*1024)),
		})
	}
	dataURL, err := getDataURLCached(c, file)
	if err != nil {
		return false, err
	}

	delete(item, "file_id")
	switch itemType {
	case "input_file":
		item["filename"] = file.Filename
		item["file_data"] = dataURL
	case "input_image":
		item["image_url"] = dataURL
	}
	return true, nil
}

func getDataURLCached(c *gin.Context, file *model.File) (string, error) {
	if file.Bytes <= dataURLCacheMaxFile {
		if value, ok := globalDataURLCache.get(file.StorageKey); ok {
			return value, nil
		}
	}
	value, err := buildDataURL(c, file)
	if err != nil {
		return "", types.WrapErr(err, types.ErrorCodeFileDereferenceFailed, types.D{"field": "file_id", "file_id": file.ID})
	}
	if file.Bytes <= dataURLCacheMaxFile {
		globalDataURLCache.set(file.StorageKey, value)
	}
	return value, nil
}

func buildDataURL(c *gin.Context, file *model.File) (string, error) {
	storageClient, err := service.EnsureUserFileStorageClient()
	if err != nil {
		return "", err
	}
	body, err := storageClient.Get(c.Request.Context(), file.StorageKey)
	if err != nil {
		return "", err
	}
	defer body.Close()

	var buf bytes.Buffer
	buf.WriteString("data:")
	buf.WriteString(file.MimeType)
	buf.WriteString(";base64,")
	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	if _, err := io.Copy(encoder, body); err != nil {
		_ = encoder.Close()
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
