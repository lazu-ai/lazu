package types

import "net/http"

// Wallet: top-up orders, payments, invoices and receipts, disputes, the
// payment provider and manual balance adjustments. Numbers: LZ-7101–7199.
//
// Customer rows never name the payment provider; operator rows may.
const (
	ErrorCodeTopupOrderNotFound         ErrorCode = "topup_order_not_found"
	ErrorCodeTopupOrderNotPending       ErrorCode = "topup_order_not_pending"
	ErrorCodeTopupMethodUnsupported     ErrorCode = "topup_method_unsupported"
	ErrorCodeTopupCreditInvalid         ErrorCode = "topup_credit_invalid"
	ErrorCodeTopupSearchKeywordInvalid  ErrorCode = "topup_search_keyword_invalid"
	ErrorCodeInvalidAmount              ErrorCode = "invalid_amount"
	ErrorCodePaymentNotFound            ErrorCode = "payment_not_found"
	ErrorCodeInvoiceUnavailable         ErrorCode = "invoice_unavailable"
	ErrorCodePaymentProviderUnavailable ErrorCode = "payment_provider_unavailable"
	ErrorCodeNoBillingCustomer          ErrorCode = "no_billing_customer"
	ErrorCodeWalletLedgerRangeInvalid   ErrorCode = "wallet_ledger_range_invalid"
	ErrorCodeDisputeStillOpen           ErrorCode = "dispute_still_open"
	ErrorCodeDisputeResolutionFailed    ErrorCode = "dispute_resolution_failed"
	ErrorCodePaymentDocumentReconcile   ErrorCode = "payment_document_reconcile_failed"
	ErrorCodeAutoReloadSettingsInvalid  ErrorCode = "auto_reload_settings_invalid"
	ErrorCodeAutoReloadCardRequired     ErrorCode = "auto_reload_card_required"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		"referral_unavailable":     {ID: "LZ-7121", HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Referral details are temporarily unavailable. Retry later."},
		"referral_code_invalid":    {ID: "LZ-7122", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Use 3\u201320 lowercase letters, digits or hyphens, with no leading or trailing hyphen."},
		"referral_code_reserved":   {ID: "LZ-7123", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This referral name is reserved. Choose another name."},
		"referral_code_taken":      {ID: "LZ-7124", HTTPStatus: http.StatusConflict, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This referral name is already in use."},
		"referral_code_limit":      {ID: "LZ-7125", HTTPStatus: http.StatusConflict, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "The referral name change limit has been reached."},
		"promotion_unavailable":    {ID: "LZ-7126", HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Promotion settings are temporarily unavailable. Retry later."},
		"promotion_config_invalid": {ID: "LZ-7127", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Check the promotion percentages, caps and time windows."},
		"referral_status_invalid":  {ID: "LZ-7128", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This referral status filter is invalid."},
		"referral_search_invalid":  {ID: "LZ-7129", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This referral search is invalid."},
		"referral_not_found":       {ID: "LZ-7130", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "The referral reward was not found."},
		"referral_not_pending":     {ID: "LZ-7131", HTTPStatus: http.StatusConflict, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This referral reward is no longer pending."},
		ErrorCodeTopupOrderNotFound: {
			ID: "LZ-7101", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "No top-up order matches trade number {trade_no|the one given}.",
		},
		ErrorCodeTopupOrderNotPending: {
			ID: "LZ-7102", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "This top-up order is no longer pending, so it cannot be completed manually.",
		},
		ErrorCodeTopupMethodUnsupported: {
			ID: "LZ-7103", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Only Stripe top-up orders can be completed manually.",
		},
		ErrorCodeTopupCreditInvalid: {
			ID: "LZ-7104", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "This top-up order has no creditable amount, so it cannot be completed.",
		},
		ErrorCodeTopupSearchKeywordInvalid: {
			ID: "LZ-7105", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "A top-up search needs at least 2 characters other than %, _ and !, and at most 4 % wildcards.",
		},
		ErrorCodeInvalidAmount: {
			ID: "LZ-7106", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Top-up amount must be a whole number of USD between {min_amount|5} and {max_amount|10000}.",
		},
		ErrorCodePaymentNotFound: {
			ID: "LZ-7107", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "This payment was not found, or it belongs to another account.",
		},
		ErrorCodeInvoiceUnavailable: {
			ID: "LZ-7108", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "The invoice or receipt for this payment is not available yet.",
		},
		ErrorCodePaymentProviderUnavailable: {
			ID: "LZ-7109", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "Payments are temporarily unavailable. Retry later; a retry with the same Idempotency-Key reuses the same order.",
		},
		ErrorCodeNoBillingCustomer: {
			ID: "LZ-7110", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This project has no billing profile yet. One is created with the first payment.",
		},
		ErrorCodeWalletLedgerRangeInvalid: {
			ID: "LZ-7111", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The wallet ledger range must end after it starts, span at most 32 days and use a valid time zone.",
		},
		ErrorCodeDisputeStillOpen: {
			ID: "LZ-7112", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Stripe has not closed this dispute with the outcome you chose. Record the outcome after Stripe does.",
		},
		ErrorCodeDisputeResolutionFailed: {
			ID: "LZ-7113", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "The dispute outcome could not be recorded for this top-up. Check the top-up and dispute ID.",
		},
		ErrorCodePaymentDocumentReconcile: {
			ID: "LZ-7114", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The invoice and receipt of this top-up could not be reconciled with Stripe. Retry later.",
		},
		ErrorCodeAutoReloadSettingsInvalid: {
			ID: "LZ-7115", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Auto-reload amounts are whole USD: the threshold at least 1, each reload between {min_amount|5} and {max_amount|10000}, and the monthly cap at least one reload ({field|amount}).",
		},
		ErrorCodeAutoReloadCardRequired: {
			ID: "LZ-7116", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "Auto-reload needs a saved card. Save one first; nothing is charged while saving.",
		},
	})
}
