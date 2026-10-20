package controller

import (
	"context"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// videoWaitMax caps ?wait on GET /v1/videos/{id}. It stays well under the
// edge proxy's 100-second read timeout.
const videoWaitMax = 60

// videoWaitInterval is how often a waiting read looks at the job again. The
// worker polls the provider every 10 seconds, so a finished job is seen within
// a couple of seconds of the worker recording it.
var videoWaitInterval = 2 * time.Second

// videoJobViews renders video jobs for the API. A stored artifact carries a
// fresh signed link, its type, size and the time the file is deleted, so a
// finished job can be played or downloaded without another call.
func videoJobViews(ctx context.Context, jobs []model.MediaJob, includeRequest bool, now time.Time) []dto.MediaJobObject {
	views, _ := mediaJobViews(ctx, jobs, includeRequest, now)
	for i := range views {
		views[i].Object = "video"
	}
	return views
}

// mediaJobViews renders media jobs with their stored files described and
// signed. stored[i] says whether job i's file still exists; a finished job
// whose file is gone has passed its storage period.
func mediaJobViews(ctx context.Context, jobs []model.MediaJob, includeRequest bool, now time.Time) (views []dto.MediaJobObject, stored []bool) {
	views = make([]dto.MediaJobObject, len(jobs))
	stored = make([]bool, len(jobs))
	fileIDs := make([]string, 0, len(jobs))
	for i, job := range jobs {
		views[i] = job.ToDTO(includeRequest)
		if views[i].Artifact != nil && views[i].Artifact.FileID != "" {
			fileIDs = append(fileIDs, views[i].Artifact.FileID)
		}
	}
	if len(fileIDs) == 0 {
		return views, stored
	}
	files, err := model.GetFilesByIDs(fileIDs)
	if err != nil {
		return views, stored
	}
	for i := range views {
		artifact := views[i].Artifact
		if artifact == nil || artifact.FileID == "" {
			continue
		}
		file, ok := files[artifact.FileID]
		if !ok || file.ProjectID != jobs[i].ProjectID {
			continue
		}
		if file.ExpiresAt != nil && *file.ExpiresAt > 0 && *file.ExpiresAt <= now.Unix() {
			continue
		}
		stored[i] = true
		artifact.MimeType = file.MimeType
		artifact.Bytes = file.Bytes
		if file.ExpiresAt != nil {
			artifact.ExpiresAt = *file.ExpiresAt
		}
		file.Filename = videoDownloadFilename(jobs[i], file)
		if link, ok := service.SignFileLink(ctx, &file, service.FileLinkTTL, now); ok {
			artifact.URL = link.URL
			artifact.URLExpiresAt = link.ExpiresAt
		}
	}
	return views, stored
}

// videoDownloadFilename names a downloaded video after its job. The stored
// file keeps the provider's own name, which means nothing to the customer.
func videoDownloadFilename(job model.MediaJob, file model.File) string {
	ext := path.Ext(file.Filename)
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(file.MimeType); len(exts) > 0 {
			ext = exts[0]
		} else {
			ext = ".mp4"
		}
	}
	return job.PublicID + ext
}

func videoJobView(ctx context.Context, job model.MediaJob, includeRequest bool, now time.Time) dto.MediaJobObject {
	return videoJobViews(ctx, []model.MediaJob{job}, includeRequest, now)[0]
}

func videoJobFinished(job *model.MediaJob) bool {
	return job.Status == model.MediaJobStatusSucceeded || job.Status == model.MediaJobStatusFailed
}

// parseVideoWait reads ?wait, the seconds a read may hold the connection open
// for an unfinished job. Absent means 0: answer at once.
func parseVideoWait(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("wait"))
	if raw == "" {
		return 0, true
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 || seconds > videoWaitMax {
		writeControlPlaneReadError(c, types.ErrorCodeInvalidWait, types.D{"field": "wait", "max": strconv.Itoa(videoWaitMax)})
		return 0, false
	}
	return seconds, true
}

// waitForVideoJob re-reads an unfinished job until it finishes, the wait runs
// out or the client goes away, and returns the latest copy.
func waitForVideoJob(ctx context.Context, job *model.MediaJob, wait time.Duration, reload func() (*model.MediaJob, error)) *model.MediaJob {
	if wait <= 0 || videoJobFinished(job) {
		return job
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(videoWaitInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return job
		case <-timer.C:
			return job
		case <-ticker.C:
			latest, err := reload()
			if err != nil || latest == nil {
				continue
			}
			job = latest
			if videoJobFinished(job) {
				return job
			}
		}
	}
}

// GetVideoContent streams a finished video's file: GET /v1/videos/{id}/content.
func GetVideoContent(c *gin.Context) {
	job, ok := loadVideoJob(c)
	if !ok {
		return
	}
	if job.Status != model.MediaJobStatusSucceeded || job.ArtifactFileID == "" {
		writeControlPlaneReadError(c, types.ErrorCodeVideoNotReady, types.D{"field": "id", "job_id": job.PublicID, "status": job.Status})
		return
	}
	file, err := model.GetFileByIDAndOwner(job.ArtifactFileID, job.UserID, job.ResourcePrincipal())
	if err != nil {
		writeMediaJobArtifactLookupError(c, err, job.ArtifactFileID)
		return
	}
	if file.ExpiresAt != nil && *file.ExpiresAt > 0 && *file.ExpiresAt <= time.Now().Unix() {
		writeControlPlaneCatalogError(c, types.ErrorCodeFileExpired, types.D{"field": "id", "file_id": file.ID})
		return
	}
	storageClient, err := service.EnsureUserFileStorageClient()
	if err != nil {
		writeControlPlaneCatalogError(c, types.ErrorCodeStorageNotConfigured, nil)
		return
	}
	body, err := storageClient.Get(c.Request.Context(), file.StorageKey)
	if err != nil {
		writeControlPlaneCatalogError(c, types.ErrorCodeFileStorageFailed, types.D{"operation": "download"})
		return
	}
	defer body.Close()
	headers := map[string]string{
		"Content-Disposition": mime.FormatMediaType("attachment", map[string]string{"filename": videoDownloadFilename(*job, *file)}),
	}
	c.DataFromReader(http.StatusOK, file.Bytes, file.MimeType, body, headers)
}

// loadVideoJob finds the caller's video job named in the path, writing the
// error itself when there is none.
func loadVideoJob(c *gin.Context) (*model.MediaJob, bool) {
	job, err := model.GetMediaJobByPublicIDAndUser(c.Param("id"), c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		writeMediaJobLookupError(c, err, c.Param("id"))
		return nil, false
	}
	if job.Kind != model.MediaJobKindVideo {
		writeControlPlaneCatalogError(c, types.ErrorCodeVideoNotFound, types.D{"field": "id", "job_id": c.Param("id")})
		return nil, false
	}
	if !authorizeEphemeralVideoJobRead(c, job) {
		return nil, false
	}
	return job, true
}
