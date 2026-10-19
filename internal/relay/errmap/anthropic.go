package errmap

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/types"
)

func MapAnthropic(status int, body []byte) (types.ErrorCode, []any) {
	bodyText := strings.ToLower(string(body))
	switch {
	case status == 529 || strings.Contains(bodyText, "overloaded_error"):
		return types.ErrorCodeUpstreamOverloaded, nil
	case strings.Contains(bodyText, "rate_limit_error"):
		return types.ErrorCodeUpstreamRateLimited, nil
	case strings.Contains(bodyText, "invalid_api_key"):
		return types.ErrorCodeChannelInvalidKey, nil
	default:
		return MapDefault(status, body)
	}
}
