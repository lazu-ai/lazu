package controller

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/service/uploader"
	"github.com/lazu-ai/lazu/internal/types"
)

// UploadRequest configures an immediate object-storage upload.
type UploadRequest struct {
	ForceReupload bool `json:"force_reupload"` // 强制重新上传（即使远端已存在且大小相同）
	IncludeToday  bool `json:"include_today"`  // 包含今天的文件
	IncludeRecent bool `json:"include_recent"` // 包含最近 10 分钟内修改的文件
}

// UploadNow triggers an immediate one-shot object upload with email notification. Admin only.
func UploadNow(c *gin.Context) {
	uid := c.GetInt("id")

	// Parse options from request body (optional)
	var req UploadRequest
	_ = c.ShouldBindJSON(&req) // Ignore error, use defaults if not provided

	common.SysLog(fmt.Sprintf("manual object upload triggered by user=%d (force=%v, includeToday=%v, includeRecent=%v)",
		uid, req.ForceReupload, req.IncludeToday, req.IncludeRecent))

	wsm := service.GetWorkerStatusManager()

	if uploader.IsUploadRunning() || wsm.IsRunning("object-upload") {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"status":  "running",
		})
		return
	}

	// 标记开始
	wsm.StartWorker("object-upload")

	opts := uploader.UploadOptions{
		ForceReupload: req.ForceReupload,
		IncludeToday:  req.IncludeToday,
		IncludeRecent: req.IncludeRecent,
	}

	start := time.Now()
	summary, err := uploader.UploadDataFolderWithOptions(opts)
	dur := time.Since(start)

	uploaded := 0
	failed := 0
	var uploadedFiles []string
	var failedFiles map[string]string

	if summary != nil {
		uploaded = len(summary.Uploaded)
		failed = len(summary.Failed)
		for _, f := range summary.Uploaded {
			uploadedFiles = append(uploadedFiles, f.Key)
		}
		failedFiles = summary.Failed
	}

	// Send email notification (same as scheduled task)
	if emailErr := uploader.SendUploadEmail(summary, err); emailErr != nil {
		common.SysError("failed to send upload email: " + emailErr.Error())
	}

	if err != nil {
		if err.Error() == "upload already running" {
			c.JSON(http.StatusOK, gin.H{"success": true, "status": "running"})
			return
		}
		common.SysError("manual object upload failed: " + err.Error())
		wsm.FailWorker("object-upload", err.Error())
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeUploadFailed))
		return
	}

	// 标记完成
	result := map[string]any{
		"uploaded":       uploaded,
		"failed":         failed,
		"uploaded_files": uploadedFiles,
		"failed_files":   failedFiles,
	}
	wsm.CompleteWorker("object-upload", fmt.Sprintf("Uploaded %d files, %d failed", uploaded, failed), result)

	common.SysLog(fmt.Sprintf("manual object upload completed by user=%d in %s: uploaded=%d failed=%d", uid, dur.Truncate(time.Millisecond), uploaded, failed))
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"status":         "completed",
		"uploaded":       uploaded,
		"failed":         failed,
		"uploaded_files": uploadedFiles,
		"failed_files":   failedFiles,
		"duration":       dur.Truncate(time.Millisecond).String(),
	})
}

// GetPendingFiles 获取待上传的文件列表. Admin only.
func GetPendingFiles(c *gin.Context) {
	files, err := uploader.ListPendingFiles()
	if err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    files,
	})
}

// UploadSingleFileRequest 单文件上传请求
type UploadSingleFileRequest struct {
	Key string `json:"key" binding:"required"` // 文件的相对路径 key
}

// UploadSingleFile uploads one local data file to object storage. Admin only.
func UploadSingleFile(c *gin.Context) {
	var req UploadSingleFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "key"})
		return
	}

	uid := c.GetInt("id")
	common.SysLog(fmt.Sprintf("single file upload triggered by user=%d for key=%s", uid, req.Key))

	result, err := uploader.UploadSingleFile(req.Key)
	if err != nil {
		common.SysError(fmt.Sprintf("single file upload failed for %s: %v", req.Key, err))
		httperr.Write(c, err)
		return
	}

	common.SysLog(fmt.Sprintf("single file upload completed by user=%d: key=%s size=%s", uid, req.Key, result.SizeHuman))
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"key":        result.Key,
		"size_human": result.SizeHuman,
	})
}

// GetWorkerStatuses 获取所有 worker 的状态. Admin only.
func GetWorkerStatuses(c *gin.Context) {
	wsm := service.GetWorkerStatusManager()
	statuses := wsm.GetAllStatuses()

	// 转换为数组格式
	var statusList []*service.WorkerStatus
	for _, status := range statuses {
		statusList = append(statusList, status)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    statusList,
	})
}

// GetWorkerStatus 获取单个 worker 的状态. Admin only.
func GetWorkerStatus(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "name"})
		return
	}

	wsm := service.GetWorkerStatusManager()
	status := wsm.GetStatus(name)
	history := wsm.GetHistory(name)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"current": status,
			"history": history,
		},
	})
}
