package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// consoleTaskPromptMax trims a prompt shown in a table row.
const consoleTaskPromptMax = 300

// consoleTaskPrincipal scopes the Tasks view the way the usage log is scoped:
// a member sees their own jobs, a manager everyone's, and the log's member
// filter narrows a manager to one person.
func consoleTaskPrincipal(access model.ProjectLogAccess) model.ProjectPrincipal {
	principal := model.ProjectPrincipal{
		ProjectID:      access.Project.ID,
		PayerUserID:    access.Project.OwnerUserID,
		ActorUserID:    access.Viewer.UserID,
		ConsoleReadAll: access.Viewer.IsManager() && access.ActorID == nil,
	}
	if access.ActorID != nil {
		principal.ActorUserID = *access.ActorID
	}
	return principal
}

func consoleTaskFilter(c *gin.Context, access model.ProjectLogAccess) model.MediaJobConsoleFilter {
	filter := model.MediaJobConsoleFilter{
		Status:    strings.TrimSpace(c.Query("status")),
		ModelName: c.Query("model_name"),
		TokenID:   access.TokenID,
		Query:     c.Query("q"),
	}
	filter.Start, _ = strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	filter.End, _ = strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	return filter
}

// GetUserTasks lists the project's media jobs for the console's Tasks view,
// with the summary strip for the same range.
func GetUserTasks(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	principal := consoleTaskPrincipal(access)
	filter := consoleTaskFilter(c, access)
	page := common.GetPageQuery(c)
	rows, total, err := model.ListMediaJobsForConsole(principal, filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		logFailure(c, err)
		return
	}
	now := time.Now()
	summary, err := model.SummarizeMediaJobsForConsole(principal, filter, now)
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":     consoleTaskViews(c.Request.Context(), rows, false, now),
		"total":     total,
		"page":      page.GetPage(),
		"page_size": page.GetPageSize(),
		"summary":   summary,
	})
}

// GetUserTask is one job of the Tasks view, with its request.
func GetUserTask(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	job, err := model.GetMediaJobForConsole(consoleTaskPrincipal(access), c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeControlPlaneCatalogError(c, types.ErrorCodeMediaJobNotFound, types.D{"field": "id", "job_id": c.Param("id")})
			return
		}
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, consoleTaskViews(c.Request.Context(), []model.MediaJob{*job}, true, time.Now())[0])
}

func consoleTaskViews(ctx context.Context, jobs []model.MediaJob, includeRequest bool, now time.Time) []dto.ConsoleTaskObject {
	views, stored := mediaJobViews(ctx, jobs, includeRequest, now)
	tokenIDs := make([]int, 0, len(jobs))
	for _, job := range jobs {
		if job.TokenID != nil && *job.TokenID > 0 {
			tokenIDs = append(tokenIDs, *job.TokenID)
		}
	}
	names, _ := model.TokenNamesByID(tokenIDs)
	out := make([]dto.ConsoleTaskObject, len(jobs))
	for i, job := range jobs {
		fields := map[string]any{}
		_ = common.Unmarshal(job.RequestSnapshot, &fields)
		hold := int64(model.MediaJobHoldAmount(job))
		charge := int64(model.MediaJobChargedAmount(job))
		task := dto.ConsoleTaskObject{
			MediaJobObject: views[i],
			Prompt:         consoleTaskPrompt(fields),
			Spec:           consoleTaskSpec(fields),
			HoldMicrousd:   hold,
			ChargeMicrousd: charge,
		}
		switch job.Status {
		case model.MediaJobStatusFailed:
			task.RefundMicrousd = hold
		case model.MediaJobStatusSucceeded:
			if hold > charge {
				task.RefundMicrousd = hold - charge
			}
			task.FileExpired = job.ArtifactFileID != "" && !stored[i]
		}
		if job.TokenID != nil {
			task.TokenName = names[*job.TokenID]
		}
		out[i] = task
	}
	return out
}

func consoleTaskPrompt(fields map[string]any) string {
	prompt, _ := fields["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if runes := []rune(prompt); len(runes) > consoleTaskPromptMax {
		prompt = string(runes[:consoleTaskPromptMax]) + "…"
	}
	return prompt
}

func consoleTaskSpec(fields map[string]any) dto.ConsoleTaskSpec {
	spec := dto.ConsoleTaskSpec{
		Seconds:     consoleTaskInt(fields["seconds"]),
		Resolution:  consoleTaskString(fields, "resolution", "size"),
		AspectRatio: consoleTaskString(fields, "aspect_ratio", "ratio"),
	}
	if audio, ok := fields["generate_audio"].(bool); ok {
		spec.Audio = &audio
	}
	if refs, ok := fields["input_references"].([]any); ok {
		for _, ref := range refs {
			item, _ := ref.(map[string]any)
			switch kind, _ := item["type"].(string); kind {
			case "video":
				spec.ReferenceVideos++
			case "audio":
				spec.ReferenceAudios++
			default:
				spec.ReferenceImages++
			}
		}
	}
	if frames, ok := fields["frame_images"].([]any); ok {
		spec.Frames = len(frames)
	}
	return spec
}

func consoleTaskString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func consoleTaskInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}
