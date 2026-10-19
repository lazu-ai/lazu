package files

import (
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

const (
	PurposeUserData = "user_data"
	PurposeVision   = "vision"

	maxUserDataBytes = int64(512 * 1024 * 1024)
	maxVisionBytes   = int64(20 * 1024 * 1024)
)

var executableExtensions = map[string]struct{}{
	".apk": {}, ".app": {}, ".bat": {}, ".bin": {}, ".cmd": {}, ".com": {},
	".dll": {}, ".dmg": {}, ".exe": {}, ".msi": {}, ".ps1": {}, ".scr": {},
	".sh": {}, ".vbs": {},
}

func validatePurpose(purpose string) error {
	switch purpose {
	case PurposeUserData, PurposeVision:
		return nil
	case "":
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "purpose"})
	default:
		return types.Err(types.ErrorCodePurposeNotSupported, types.D{"field": "purpose", "purpose": purpose})
	}
}

func invalidFile(reason string) error {
	return types.Err(types.ErrorCodeInvalidFile, types.D{"field": "file", "reason": reason})
}

func validateFileForPurpose(purpose, filename string, size int64, file multipart.File) (string, error) {
	if size <= 0 {
		return "", invalidFile("file is empty")
	}
	switch purpose {
	case PurposeUserData:
		if size > maxUserDataBytes {
			return "", types.Err(types.ErrorCodeFileTooLarge, types.D{"field": "file", "limit": "512 MB"})
		}
	case PurposeVision:
		if size > maxVisionBytes {
			return "", types.Err(types.ErrorCodeFileTooLarge, types.D{"field": "file", "limit": "20 MB"})
		}
	}

	mimeType, err := detectMimeType(file)
	if err != nil {
		return "", types.WrapErr(err, types.ErrorCodeInvalidFile, types.D{"field": "file", "reason": "the file could not be read"})
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", types.WrapErr(err, types.ErrorCodeInvalidFile, types.D{"field": "file", "reason": "the file could not be read"})
	}

	switch purpose {
	case PurposeVision:
		if !isAllowedVisionMime(mimeType) {
			return "", invalidFile("vision files must be png, jpeg, gif, or webp")
		}
	case PurposeUserData:
		if isExecutable(filename, mimeType) {
			return "", invalidFile("executable files are not supported")
		}
	}
	return mimeType, nil
}

func detectMimeType(file multipart.File) (string, error) {
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	buf = buf[:n]
	switch {
	case len(buf) >= 8 &&
		buf[0] == 0x89 && string(buf[1:4]) == "PNG" &&
		buf[4] == 0x0d && buf[5] == 0x0a && buf[6] == 0x1a && buf[7] == 0x0a:
		return "image/png", nil
	case len(buf) >= 3 && buf[0] == 0xff && buf[1] == 0xd8 && buf[2] == 0xff:
		return "image/jpeg", nil
	case len(buf) >= 6 && (string(buf[:6]) == "GIF87a" || string(buf[:6]) == "GIF89a"):
		return "image/gif", nil
	case len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP":
		return "image/webp", nil
	default:
		mimeType := http.DetectContentType(buf)
		if mimeType == "" {
			return "application/octet-stream", nil
		}
		return strings.Split(mimeType, ";")[0], nil
	}
}

func isAllowedVisionMime(mimeType string) bool {
	switch mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func isExecutable(filename string, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	if _, blocked := executableExtensions[ext]; blocked {
		return true
	}
	switch mimeType {
	case "application/x-msdownload", "application/x-executable", "application/x-sh":
		return true
	default:
		return false
	}
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	runes := make([]rune, 0, len(name))
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			continue
		}
		runes = append(runes, r)
		if len(runes) >= 255 {
			break
		}
	}
	if len(runes) == 0 {
		return "file"
	}
	return string(runes)
}

func firstFormValue(form *multipart.Form, key string) string {
	if form == nil {
		return ""
	}
	values := form.Value[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// writeError answers a Files API request in the OpenAI envelope. The console
// routes (/api/files) share these handlers and the console reads the same
// envelope.
func writeError(c *gin.Context, err error) {
	httperr.WriteOpenAI(c, err)
}

func writeCode(c *gin.Context, code types.ErrorCode, details ...types.D) {
	httperr.WriteOpenAI(c, types.Err(code, details...))
}

func generateFileID() (string, error) {
	return common.GenerateFileID(), nil
}
