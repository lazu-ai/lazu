package files

import (
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	uploadMultipartMemory = int64(32 << 20)
	uploadBodyMaxOverhead = int64(1 << 20)
	maxUploadBodyBytes    = maxUserDataBytes + uploadBodyMaxOverhead
)

func Upload(c *gin.Context) {
	form, err := parseUploadMultipartForm(c)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeCode(c, types.ErrorCodeFileTooLarge, types.D{"field": "file", "limit": "512 MB"})
			return
		}
		writeCode(c, types.ErrorCodeInvalidRequest, types.D{"field": "file", "reason": "the body is not valid multipart form data"})
		return
	}
	defer form.RemoveAll()

	if c.GetBool("console_file_request") {
		if !prepareConsoleFileAccess(c, firstFormValue(form, "project_id")) {
			return
		}
	}
	purpose := firstFormValue(form, "purpose")
	if err := validatePurpose(purpose); err != nil {
		writeError(c, err)
		return
	}

	fileHeaders := form.File["file"]
	if len(fileHeaders) == 0 {
		writeCode(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "file"})
		return
	}
	header := fileHeaders[0]
	file, err := header.Open()
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeInvalidFile, types.D{"field": "file", "reason": "the file could not be read"}))
		return
	}
	defer file.Close()

	filename := sanitizeFilename(header.Filename)
	mimeType, err := validateFileForPurpose(purpose, filename, header.Size, file)
	if err != nil {
		writeError(c, err)
		return
	}

	storageClient, err := service.EnsureUserFileStorageClient()
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeStorageNotConfigured))
		return
	}

	now := service.NowUnix()
	expiresAt := service.DefaultFileExpiresAt(now)
	fileID, err := generateFileID()
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
		return
	}
	principal := model.RequestResourcePrincipal(c)
	if principal.ProjectID > 0 {
		held, err := model.ProjectPaymentHeld(principal.ProjectID)
		if err != nil {
			writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
			return
		}
		if held {
			writeCode(c, types.ErrorCodeProjectAccessSuspended)
			return
		}
	}
	storageOwner := c.GetInt("id")
	if principal.ProjectID > 0 {
		storageOwner = principal.PayerUserID
	}
	user, err := model.GetUserById(storageOwner, false)
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
		return
	}
	storageKey := service.BuildUserFileStorageKey(user.PublicID, purpose, time.Unix(now, 0).UTC())
	if _, err := file.Seek(0, 0); err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeInvalidFile, types.D{"field": "file", "reason": "the file could not be read"}))
		return
	}
	if err := storageClient.Put(c.Request.Context(), storageKey, file, mimeType); err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeFileStorageFailed, types.D{"operation": "upload"}))
		return
	}

	tokenID := c.GetInt("token_id")
	var tokenIDPtr *int
	if tokenID > 0 {
		tokenIDPtr = &tokenID
	}
	row := &model.File{
		ProjectID: principal.ProjectID, PayerUserID: principal.PayerUserID, ActorUserID: principal.ActorUserID,
		ID:         fileID,
		UserID:     c.GetInt("id"),
		TokenID:    tokenIDPtr,
		Filename:   filename,
		Purpose:    purpose,
		MimeType:   mimeType,
		Bytes:      header.Size,
		StorageKey: storageKey,
		Status:     model.FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  now,
	}
	if err := model.CreateFileWithAccounting(model.CreateFileAccountingInput{File: row, EnforceQuota: true}); err != nil {
		if deleteErr := storageClient.Delete(c.Request.Context(), storageKey); deleteErr != nil {
			logger.LogWarn(c, fmt.Sprintf("failed to roll back stored file %s: %v", storageKey, deleteErr))
		}
		writeError(c, fileAccountingError(err))
		return
	}

	c.JSON(http.StatusCreated, row.ToDTO())
}

func parseUploadMultipartForm(c *gin.Context) (*multipart.Form, error) {
	if c.Request.ContentLength > maxUploadBodyBytes {
		return nil, &http.MaxBytesError{Limit: maxUploadBodyBytes}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBodyBytes)
	if err := c.Request.ParseMultipartForm(uploadMultipartMemory); err != nil {
		return nil, err
	}
	if c.Request.MultipartForm == nil {
		return nil, fmt.Errorf("multipart form is empty")
	}
	return c.Request.MultipartForm, nil
}

func List(c *gin.Context) {
	userID := c.GetInt("id")
	purpose := strings.TrimSpace(c.Query("purpose"))
	if purpose != "" {
		if err := validatePurpose(purpose); err != nil {
			writeError(c, err)
			return
		}
	}
	limit := parseLimit(c.Query("limit"))
	order := c.DefaultQuery("order", "desc")
	rows, err := model.ListFilesByOwner(userID, purpose, limit, order, model.RequestResourcePrincipal(c))
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
		return
	}
	data := make([]dto.FileObject, 0, len(rows))
	for _, row := range rows {
		data = append(data, row.ToDTO())
	}
	c.JSON(http.StatusOK, dto.FileListResponse{Object: "list", Data: data})
}

func Retrieve(c *gin.Context) {
	file, ok := loadOwnedFile(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, file.ToDTO())
}

func Delete(c *gin.Context) {
	file, ok := loadOwnedFile(c)
	if !ok {
		return
	}
	if file.ProjectID > 0 {
		principal := model.RequestResourcePrincipal(c)
		if !mayDeleteProjectFile(file, principal) {
			writeCode(c, types.ErrorCodeAccessDenied)
			return
		}
	}
	storageClient, err := service.EnsureUserFileStorageClient()
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeStorageNotConfigured))
		return
	}
	if err := storageClient.Delete(c.Request.Context(), file.StorageKey); err != nil {
		logger.LogWarn(c, fmt.Sprintf("file delete storage cleanup failed for %s: %v", file.ID, err))
		writeError(c, types.WrapErr(err, types.ErrorCodeFileStorageFailed, types.D{"operation": "delete"}))
		return
	}
	if err := model.DeleteFileWithAccounting(file); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeCode(c, types.ErrorCodeFileNotFound, types.D{"field": "id", "file_id": file.ID})
			return
		}
		writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
		return
	}
	c.JSON(http.StatusOK, dto.FileDeleteResponse{ID: file.ID, Object: model.FileObjectType, Deleted: true})
}

func Content(c *gin.Context) {
	file, ok := loadOwnedFile(c)
	if !ok {
		return
	}
	storageClient, err := service.EnsureUserFileStorageClient()
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeStorageNotConfigured))
		return
	}
	body, err := storageClient.Get(c.Request.Context(), file.StorageKey)
	if err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeFileStorageFailed, types.D{"operation": "download"}))
		return
	}
	defer body.Close()
	headers := map[string]string{
		"Content-Disposition": mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}),
	}
	c.DataFromReader(http.StatusOK, file.Bytes, file.MimeType, body, headers)
}

func ConsoleList(c *gin.Context) {
	if !prepareConsoleFileAccess(c, "") {
		return
	}
	List(c)
}

func ConsoleDelete(c *gin.Context) {
	if !prepareConsoleFileAccess(c, "") {
		return
	}
	Delete(c)
}

func ConsoleContent(c *gin.Context) {
	if !prepareConsoleFileAccess(c, "") {
		return
	}
	Content(c)
}

// mayDeleteProjectFile mirrors the resource read scope: only the principal that
// produced a row may delete it. Reading or managing someone's Key never grants
// the right to delete what it produced, so a manager's console read is not
// enough, and a Key may only delete within its own logical Key.
func mayDeleteProjectFile(file *model.File, principal model.ProjectPrincipal) bool {
	if file == nil || principal.ProjectID != file.ProjectID {
		return false
	}
	fileToken := 0
	if file.TokenID != nil {
		fileToken = *file.TokenID
	}
	if principal.TokenID > 0 {
		// An API-Key deletion is bounded by the logical Key that wrote the row.
		// Rotation reuses the tokens row, so a rotated credential still deletes
		// its own uploads, while a sibling Key of the same member and a console
		// upload that carries no token id both stay untouchable.
		if fileToken != principal.TokenID {
			return false
		}
		return principal.ActorUserID > 0 && file.ActorUserID == principal.ActorUserID
	}
	// A console principal carries no token id: the original actor deletes its
	// own rows whichever Key produced them.
	return principal.ActorUserID > 0 && file.ActorUserID == principal.ActorUserID
}

func loadOwnedFile(c *gin.Context) (*model.File, bool) {
	fileID := strings.TrimSpace(c.Param("id"))
	file, err := model.GetFileByIDAndOwner(fileID, c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeCode(c, types.ErrorCodeFileNotFound, types.D{"field": "id", "file_id": fileID})
			return nil, false
		}
		writeError(c, types.WrapErr(err, types.ErrorCodeInternalError))
		return nil, false
	}
	return file, true
}

func parseLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 10000
	}
	if limit > 10000 {
		return 10000
	}
	return limit
}

// fileAccountingError names the failure of recording a new file. A full
// storage allowance and a suspended project are the caller's to fix; anything
// else stays on the server as internal_error.
func fileAccountingError(err error) error {
	switch {
	case types.ErrCode(err) != "":
		return err
	case errors.Is(err, model.ErrProjectAccessSuspended):
		return types.WrapErr(err, types.ErrorCodeProjectAccessSuspended)
	}
	return types.WrapErr(err, types.ErrorCodeInternalError)
}

// consoleProjectError names why the console's project selector could not be
// resolved.
func consoleProjectError(err error) error {
	switch {
	case types.ErrCode(err) != "":
		return err
	case errors.Is(err, gorm.ErrRecordNotFound):
		return types.Err(types.ErrorCodeProjectNotFound)
	case errors.Is(err, model.ErrProjectFeatureUnavailable):
		return types.Err(types.ErrorCodeFeatureUnavailable)
	case err.Error() == string(types.ErrorCodeProjectContextMismatch):
		return types.Err(types.ErrorCodeProjectContextMismatch)
	}
	return types.WrapErr(err, types.ErrorCodeInternalError)
}

func ConsoleUpload(c *gin.Context) { c.Set("console_file_request", true); Upload(c) }
func prepareConsoleFileAccess(c *gin.Context, bodyProjectID string) bool {
	pc, err := middleware.ResolveConsoleProject(c, bodyProjectID)
	if err != nil {
		writeError(c, consoleProjectError(err))
		return false
	}
	if pc.Member.Status != model.ProjectMemberActive {
		writeCode(c, types.ErrorCodeProjectAccessSuspended)
		return false
	}
	if _, err := model.NewProjectLogAccess(pc.Project, pc.Member); err != nil {
		writeError(c, types.WrapErr(err, types.ErrorCodeProjectAccessSuspended))
		return false
	}
	principal := model.ProjectPrincipal{ProjectID: pc.Project.ID, PayerUserID: pc.Project.OwnerUserID, ActorUserID: pc.Member.UserID, ConsoleReadAll: pc.Member.IsManager()}
	c.Set("console_resource_principal", principal)
	return true
}
