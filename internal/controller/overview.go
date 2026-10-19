package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// overviewRanges are the console's presets: the bucket each is drawn in and
// how far back it reaches.
var overviewRanges = map[string]struct {
	seconds int64
	bucket  string
}{
	"24h": {24 * 3600, model.OverviewBucketHour},
	"7d":  {7 * 86400, model.OverviewBucketDay},
	"30d": {30 * 86400, model.OverviewBucketDay},
	"90d": {90 * 86400, model.OverviewBucketWeek},
}

// GetOverview serves the console home: GET /api/data/overview
// ?range=24h|7d|30d|90d (or start/end) &tz=<minutes east of UTC>
// &scope=platform|self. Platform scope needs an admin.
func GetOverview(c *gin.Context) {
	now := time.Now().Unix()
	tzMinutes, _ := strconv.ParseInt(c.DefaultQuery("tz", "0"), 10, 64)
	if tzMinutes < -14*60 || tzMinutes > 14*60 {
		tzMinutes = 0
	}
	req := model.OverviewRequest{Now: now, TZOffset: tzMinutes * 60, ScopeUserID: c.GetInt("id")}
	if preset, ok := overviewRanges[c.DefaultQuery("range", "24h")]; ok && c.Query("start") == "" {
		req.End, req.Start, req.Bucket = now, now-preset.seconds, preset.bucket
	} else {
		start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
		end, _ := strconv.ParseInt(c.Query("end"), 10, 64)
		if start <= 0 || end <= start || end-start > 400*86400 {
			httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"field": "start"})
			return
		}
		req.Start, req.End = start, end
		switch length := end - start; {
		case length <= 2*86400:
			req.Bucket = model.OverviewBucketHour
		case length <= 45*86400:
			req.Bucket = model.OverviewBucketDay
		default:
			req.Bucket = model.OverviewBucketWeek
		}
	}
	if c.Query("scope") == "platform" {
		if c.GetInt("role") < common.RoleRootUser {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		req.Platform = true
	}
	overview, err := model.BuildOverviewContext(c.Request.Context(), req)
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}
