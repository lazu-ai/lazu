package controller

import (
	"math"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
)

// firstStringAtPaths returns the first non-empty string (or number rendered
// as a string) found at any of the dotted paths. Path parts may index arrays
// ("data.0.task_id").
func firstStringAtPaths(payload map[string]any, paths ...string) string {
	for _, candidate := range paths {
		value := any(payload)
		for _, part := range strings.Split(candidate, ".") {
			switch typed := value.(type) {
			case map[string]any:
				value = typed[part]
			case []any:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(typed) {
					value = nil
				} else {
					value = typed[index]
				}
			default:
				value = nil
			}
			if value == nil {
				break
			}
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if number, ok := value.(float64); ok &&
			!math.IsNaN(number) && !math.IsInf(number, 0) {
			return strconv.FormatFloat(number, 'f', -1, 64)
		}
	}
	return ""
}

// parseVideoProviderSubmission reads a task id and a direct artifact URL off
// any family's submission answer, for callers that only hold the body.
func parseVideoProviderSubmission(body []byte) (string, string) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	upstreamID := firstStringAtPaths(payload,
		"id",
		"name",
		"job_id",
		"task_id",
		"data.id",
		"data.name",
		"data.task_id",
		"data.0.task_id",
		"operation.name",
	)
	artifactURL := firstStringAtPaths(payload,
		"url",
		"video.url",
		"output.url",
		"data.url",
		"response.video.url",
	)
	return upstreamID, artifactURL
}
