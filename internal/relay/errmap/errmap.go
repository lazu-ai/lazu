package errmap

import (
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"
)

type Mapper func(status int, body []byte) (types.ErrorCode, []any)

// MapByChannelType dispatches to the provider-specific mapper based on the
// upstream channel type. Anchored to constant.ChannelType* so it stays in
// sync with the channel catalog. Unknown / not-yet-mapped channel types
// fall back to MapDefault — that's intentional: MapDefault is body-pattern
// based and covers the vendor-agnostic cases (rate_limit, timeout, etc.).
//
// Adding a new provider mapper: implement Map<Provider>(status, body)
// in a sibling file, then add a case here.
func MapByChannelType(channelType int, status int, body []byte) (types.ErrorCode, []any) {
	switch channelType {
	case constant.ChannelTypeAnthropic:
		return MapAnthropic(status, body)
	case constant.ChannelTypeOpenAI,
		constant.ChannelTypeCustom:
		return MapOpenAI(status, body)
	case constant.ChannelTypeGemini:
		return MapGemini(status, body)
	case constant.ChannelTypeXai:
		return MapXAI(status, body)
	default:
		return MapDefault(status, body)
	}
}

func GuessFromStatus(status int) types.ErrorCode {
	switch {
	case status == http.StatusTooManyRequests:
		return types.ErrorCodeUpstreamRateLimited
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout || status == 524:
		return types.ErrorCodeUpstreamTimeout
	case status == http.StatusServiceUnavailable || status == 529:
		return types.ErrorCodeUpstreamOverloaded
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return types.ErrorCodeChannelInvalidKey
	// A 402 from a provider means Lazu's own account there ran dry; it is
	// never the caller's balance, which the gateway checks before relaying.
	case status == http.StatusPaymentRequired:
		return types.ErrorCodeUpstreamBillingFailed
	case status == http.StatusBadRequest:
		return types.ErrorCodeBadRequestBody
	default:
		return types.ErrorCodeUpstreamError
	}
}

func MapDefault(status int, body []byte) (types.ErrorCode, []any) {
	bodyText := strings.ToLower(string(body))
	switch {
	// The provider account Lazu relays through is out of credit. Checked
	// before rate limits because OpenAI reports it on a 429 as well.
	case strings.Contains(bodyText, "insufficient_quota") ||
		strings.Contains(bodyText, "exceeded your current quota") ||
		strings.Contains(bodyText, "credit balance") ||
		strings.Contains(bodyText, "insufficient balance") ||
		strings.Contains(bodyText, "insufficient_balance") ||
		strings.Contains(bodyText, "余额不足"):
		return types.ErrorCodeUpstreamBillingFailed, nil
	case strings.Contains(bodyText, "rate_limit") ||
		strings.Contains(bodyText, "rate limit") ||
		strings.Contains(bodyText, "too many requests") ||
		strings.Contains(bodyText, "quota exceeded"):
		return types.ErrorCodeUpstreamRateLimited, nil
	case strings.Contains(bodyText, "timeout") ||
		strings.Contains(bodyText, "deadline exceeded"):
		return types.ErrorCodeUpstreamTimeout, nil
	case strings.Contains(bodyText, "overloaded") ||
		strings.Contains(bodyText, "server overloaded") ||
		strings.Contains(bodyText, "temporarily unavailable"):
		return types.ErrorCodeUpstreamOverloaded, nil
	case strings.Contains(bodyText, "invalid_api_key") ||
		strings.Contains(bodyText, "invalid api key") ||
		strings.Contains(bodyText, "unauthorized"):
		return types.ErrorCodeChannelInvalidKey, nil
	case strings.Contains(bodyText, "content filter") ||
		strings.Contains(bodyText, "content_filter") ||
		strings.Contains(bodyText, "safety") ||
		strings.Contains(bodyText, "blocked"):
		return types.ErrorCodePromptBlocked, nil
	case strings.Contains(bodyText, "context length") ||
		strings.Contains(bodyText, "context window") ||
		strings.Contains(bodyText, "maximum context") ||
		strings.Contains(bodyText, "prompt is too long"):
		return types.ErrorCodeContextLengthExceeded, nil
	default:
		return GuessFromStatus(status), nil
	}
}
