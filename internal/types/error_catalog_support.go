package types

import "net/http"

// Support reports from the console (POST /api/user/support/report).
const (
	ErrorCodeSupportReportInvalid     ErrorCode = "support_report_invalid"
	ErrorCodeSupportUnavailable       ErrorCode = "support_unavailable"
	ErrorCodeSupportReportRateLimited ErrorCode = "support_report_rate_limited"
	ErrorCodeSupportReportFailed      ErrorCode = "support_report_failed"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		ErrorCodeSupportReportInvalid: {
			ID: "LZ-7416", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The report was not accepted: {reason|describe the problem in 2 to 4000 characters}.",
		},
		ErrorCodeSupportUnavailable: {
			ID: "LZ-7417", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This deployment has no support address to send reports to.",
		},
		ErrorCodeSupportReportRateLimited: {
			ID: "LZ-7418", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
			Template: "Too many reports from this account in the last hour. Try again later.",
		},
		ErrorCodeSupportReportFailed: {
			ID: "LZ-7419", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "The report could not be sent. Try again, or write to support directly.",
		},
	})
}
