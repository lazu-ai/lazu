package types

import "net/http"

// API keys (LZ-7201–7299) and usage, logs, reports and spend budgets
// (LZ-7701–7799).
const (
	ErrorCodeKeyNameTooLong    ErrorCode = "key_name_too_long"
	ErrorCodeKeyEnableBlocked  ErrorCode = "key_enable_blocked"
	ErrorCodeKeyLimitReached   ErrorCode = "key_limit_reached"
	ErrorCodeSecretUnavailable ErrorCode = "secret_unavailable"
	ErrorCodeKeyNotFound       ErrorCode = "key_not_found"
	// ErrorCodeKeyInactive is a Key whose state stops it without saying
	// which state. Callers see invalid_api_key; expired, disabled and
	// exhausted Keys carry their own public codes.
	ErrorCodeKeyInactive ErrorCode = "key_inactive"

	ErrorCodeUsageRangeInvalid         ErrorCode = "usage_range_invalid"
	ErrorCodeUsageRangeTooLong         ErrorCode = "usage_range_too_long"
	ErrorCodeInvalidUsageSummaryWindow ErrorCode = "invalid_usage_summary_window"
	ErrorCodeRequestNotFound           ErrorCode = "request_not_found"
	ErrorCodeLogSessionNotFound        ErrorCode = "log_session_not_found"
	ErrorCodeUsageExportFormatInvalid  ErrorCode = "usage_export_format_unsupported"
	ErrorCodeLogCleanupCutoffTooRecent ErrorCode = "log_cleanup_cutoff_too_recent"
	ErrorCodeSpendBudgetNotFound       ErrorCode = "spend_budget_not_found"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		// ── LZ-72xx · API keys ─────────────────────────────────────────
		ErrorCodeKeyNameTooLong: {
			ID: "LZ-7201", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "API key names are limited to {max_length|30} characters.",
		},
		ErrorCodeKeyEnableBlocked: {
			ID: "LZ-7202", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This API key cannot be enabled because it has expired or used up its allowance. Extend its expiry or raise its allowance first.",
		},
		ErrorCodeKeyLimitReached: {
			ID: "LZ-7203", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "Each member can hold at most {limit|10} API keys in a project. Delete a key that is no longer used, then try again.",
		},
		ErrorCodeSecretUnavailable: {
			ID: "LZ-7204", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This API key was stored as a digest only, so its full secret cannot be shown. Create a new key if you need the full value.",
		},
		ErrorCodeKeyNotFound: {
			ID: "LZ-7205", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "The API key was not found. It may have been deleted.",
		},
		ErrorCodeKeyInactive: {
			Public: ErrorCodeInvalidAPIKey, HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication,
			Template: "API key is inactive",
		},

		// ── LZ-77xx · usage, logs, reports, spend budgets ───────────────
		ErrorCodeUsageRangeInvalid: {
			ID: "LZ-7701", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The time range is invalid: the end is earlier than the start.",
		},
		ErrorCodeUsageRangeTooLong: {
			ID: "LZ-7702", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The time range is longer than {max_days|30} days. Choose a shorter range.",
		},
		ErrorCodeInvalidUsageSummaryWindow: {
			ID: "LZ-7703", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Invalid usage summary window: {reason|check since, until and granularity}.",
		},
		ErrorCodeRequestNotFound: {
			ID: "LZ-7704", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "No request {request_id|with this ID} was found for this API key.",
		},
		ErrorCodeLogSessionNotFound: {
			ID: "LZ-7705", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "The session was not found in the logs you can read.",
		},
		ErrorCodeUsageExportFormatInvalid: {
			ID: "LZ-7706", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Export format {format|requested} is not supported. Use csv or xlsx.",
		},
		ErrorCodeLogCleanupCutoffTooRecent: {
			ID: "LZ-7707", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Logs from the last {min_days|few} days are kept for model ranking. Choose an earlier cutoff.",
		},
		ErrorCodeSpendBudgetNotFound: {
			ID: "LZ-7708", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "The spend budget was not found. It may have been deleted.",
		},
	})
}
