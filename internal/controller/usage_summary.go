package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

type usageSummaryResponse struct {
	TotalMicrousd billing.MicroUSD     `json:"total_microusd"`
	TotalRequests int64                `json:"total_requests"`
	TotalTokens   int64                `json:"total_tokens"`
	Series        []usageSummaryBucket `json:"series"`
	Window        usageSummaryWindow   `json:"window"`
}

type usageSummaryWindow struct {
	Since       int64  `json:"since"`
	Until       int64  `json:"until"`
	Granularity string `json:"granularity"`
}

type usageSummaryBucket struct {
	Bucket   string           `json:"bucket"`
	Microusd billing.MicroUSD `json:"microusd"`
	Requests int64            `json:"requests"`
	Tokens   int64            `json:"tokens"`
}

const usageSummaryMaxWindowSeconds int64 = 366 * 24 * 3600

func GetUsageSummary(c *gin.Context) {
	tokenID := c.GetInt("token_id")
	userID := c.GetInt("id")
	principal := model.RequestResourcePrincipal(c)
	if tokenID <= 0 || userID <= 0 {
		writeControlPlaneCatalogError(c, types.ErrorCodeInvalidAPIKey)
		return
	}

	since, until, granularity, err := parseUsageSummaryQuery(c)
	if err != nil {
		writeControlPlaneAPIError(c, err)
		return
	}

	var summary model.TokenConsumeSummary
	var series []model.TokenUsageSeriesPoint
	if principal.ProjectID > 0 {
		summary, err = model.GetProjectKeyConsumeSummary(principal, since, until)
	} else {
		summary, err = model.GetUserConsumeSummaryByUserIDInWindow(userID, since, until)
	}
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	if principal.ProjectID > 0 {
		series, err = model.GetProjectKeyUsageSeries(principal, since, until, granularity)
	} else {
		series, err = model.GetUserUsageSeriesByUserIDInWindow(userID, since, until, granularity)
	}
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}

	c.JSON(http.StatusOK, usageSummaryResponse{
		TotalMicrousd: summary.AmountMicrousd,
		TotalRequests: summary.RequestCount,
		TotalTokens:   summary.TotalTokens(),
		Series:        buildUsageSummaryBuckets(series),
		Window: usageSummaryWindow{
			Since:       since,
			Until:       until,
			Granularity: granularity,
		},
	})
}

func parseUsageSummaryQuery(c *gin.Context) (int64, int64, string, error) {
	until, err := parseOptionalUnixQuery(c.Query("until"), common.GetTimestamp())
	if err != nil {
		return 0, 0, "", usageSummaryWindowError("until", "until must be a unix timestamp")
	}
	since, err := parseOptionalUnixQuery(c.Query("since"), until-30*24*3600)
	if err != nil {
		return 0, 0, "", usageSummaryWindowError("since", "since must be a unix timestamp")
	}
	if since < 0 {
		since = 0
	}
	if until < since {
		return 0, 0, "", usageSummaryWindowError("until", "until must be greater than or equal to since")
	}
	if until-since > usageSummaryMaxWindowSeconds {
		return 0, 0, "", usageSummaryWindowError("since", "the window must be 366 days or less")
	}

	granularity := strings.TrimSpace(strings.ToLower(c.Query("granularity")))
	switch granularity {
	case "":
		granularity = "day"
	case "hour", "day", "month":
	default:
		return 0, 0, "", usageSummaryWindowError("granularity", "granularity must be hour, day, or month")
	}
	return since, until, granularity, nil
}

// usageSummaryWindowError names the query parameter that made the window
// invalid; the reason is English and fills the catalog message.
func usageSummaryWindowError(field string, reason string) error {
	return types.Err(types.ErrorCodeInvalidUsageSummaryWindow, types.D{"field": field, "reason": reason})
}

func parseOptionalUnixQuery(raw string, fallback int64) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func buildUsageSummaryBuckets(series []model.TokenUsageSeriesPoint) []usageSummaryBucket {
	buckets := make([]usageSummaryBucket, 0, len(series))
	for _, point := range series {
		buckets = append(buckets, usageSummaryBucket{
			Bucket:   time.Unix(point.Bucket, 0).UTC().Format(time.RFC3339),
			Microusd: point.AmountMicrousd,
			Requests: point.RequestCount,
			Tokens:   point.TotalTokens(),
		})
	}
	return buckets
}
