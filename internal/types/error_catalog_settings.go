package types

import "net/http"

// Platform settings, announcements, email, feedback and status pages.
// Numbers: LZ-7401–7499.
const (
	// ErrorCodeSettingInvalid covers every rejected setting value. Details:
	// field (the setting key), rule (what the value broke), and when they
	// apply index (1-based list item), item (the item's attribute) and limit.
	ErrorCodeSettingInvalid                     ErrorCode = "setting_invalid"
	ErrorCodeContentLimitExceeded               ErrorCode = "content_limit_exceeded"
	ErrorCodeAnnouncementNotFound               ErrorCode = "announcement_not_found"
	ErrorCodeEmailTestFailed                    ErrorCode = "email_test_failed"
	ErrorCodeEmailTestRecipientMissing          ErrorCode = "email_test_recipient_missing"
	ErrorCodeEmailRecipientsRequired            ErrorCode = "email_recipients_required"
	ErrorCodeAnnouncementEmailContentMissing    ErrorCode = "announcement_email_content_missing"
	ErrorCodeTranslationTokenMissing            ErrorCode = "translation_token_missing"
	ErrorCodeFeedbackRequestNotFound            ErrorCode = "feedback_request_not_found"
	ErrorCodeDatabaseUnreachable                ErrorCode = "database_unreachable"
	ErrorCodeEmailDomainNotAllowed              ErrorCode = "email_domain_not_allowed"
	ErrorCodeEmailAliasNotAllowed               ErrorCode = "email_alias_not_allowed"
	ErrorCodePasswordInvalid                    ErrorCode = "password_invalid"
	ErrorCodePasswordResetLinkInvalid           ErrorCode = "password_reset_link_invalid"
	ErrorCodePasswordResetClientUpgradeRequired ErrorCode = "password_reset_client_upgrade_required"
	ErrorCodeUUIDCollision                      ErrorCode = "uuid_collision"
	ErrorCodeTelegramNotConfigured              ErrorCode = "telegram_not_configured"
	ErrorCodeTelegramSendFailed                 ErrorCode = "telegram_send_failed"
	ErrorCodeTelegramMuteInvalid                ErrorCode = "telegram_mute_invalid"
	ErrorCodeTelegramLinkUnavailable            ErrorCode = "telegram_link_unavailable"
	ErrorCodeTelegramNotLinked                  ErrorCode = "telegram_not_linked"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		"mail_not_found":          {ID: "LZ-7421", HTTPStatus: http.StatusNotFound, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "The archived mail was not found."},
		"mail_resend_failed":      {ID: "LZ-7422", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "The archived mail could not be resent. Check the delivery details."},
		"admin_email_missing":     {ID: "LZ-7423", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "Add an email address to your account before sending a preview."},
		"mail_regenerate_failed":  {ID: "LZ-7424", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "The mail could not be regenerated. Regenerate drafts from the usage digest page."},
		"digest_settings_invalid": {ID: "LZ-7425", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "The digest settings are invalid."},
		"mail_send_failed":        {ID: "LZ-7426", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator, Template: "The mail could not be sent. Check the delivery details."},
		ErrorCodeSettingInvalid: {
			ID: "LZ-7401", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The value of setting {field|this setting} is invalid ({rule|check the value}).",
		},
		ErrorCodeContentLimitExceeded: {
			ID: "LZ-7402", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "{field|This list} can hold at most {limit|the allowed number of} items.",
		},
		ErrorCodeAnnouncementNotFound: {
			ID: "LZ-7403", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "The announcement was not found.",
		},
		ErrorCodeEmailTestFailed: {
			ID: "LZ-7404", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The test email could not be sent. Check the SMTP settings; the mail server's reply is in the server log.",
		},
		ErrorCodeEmailTestRecipientMissing: {
			ID: "LZ-7405", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Your account has no email address. Add one, or pass email in the request.",
		},
		ErrorCodeEmailRecipientsRequired: {
			ID: "LZ-7406", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "No recipients were chosen. Pass user_ids or set send_all.",
		},
		ErrorCodeAnnouncementEmailContentMissing: {
			ID: "LZ-7407", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "There is no announcement to send. Provide a title and content, or publish an announcement first.",
		},
		ErrorCodeTranslationTokenMissing: {
			ID: "LZ-7408", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Translation calls a model with one of your own API keys, and you have no usable key. Create or enable one.",
		},
		ErrorCodeFeedbackRequestNotFound: {
			ID: "LZ-7409", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "No request {request_id|with this ID} was found for your account.",
		},
		ErrorCodeDatabaseUnreachable: {
			ID: "LZ-7410", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The server cannot reach its database.",
		},
		ErrorCodeEmailDomainNotAllowed: {
			ID: "LZ-7411", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "This email domain is not on the allowed list.",
		},
		ErrorCodeEmailAliasNotAllowed: {
			ID: "LZ-7412", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Email addresses with '+' or '.' in the local part are not accepted.",
		},
		ErrorCodePasswordInvalid: {
			ID: "LZ-7413", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The password does not meet the rules ({rule|length}, limit {limit|see the reference}).",
		},
		ErrorCodePasswordResetLinkInvalid: {
			ID: "LZ-7414", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The password reset link is invalid or has expired. Request a new one.",
		},
		ErrorCodePasswordResetClientUpgradeRequired: {
			ID: "LZ-7415", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "This page is out of date. Reload it and set the password again; the reset link has not been used.",
		},
		ErrorCodeTelegramNotConfigured: {
			ID: "LZ-7431", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Telegram is not set up: {reason|set telegram.bot_token and telegram.ops_chat in the deployment config}.",
		},
		ErrorCodeTelegramSendFailed: {
			ID: "LZ-7432", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "Telegram did not accept the message: {reason|no reply}. Check that the bot is a member of the chat and the chat id is right.",
		},
		ErrorCodeTelegramMuteInvalid: {
			ID: "LZ-7433", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "minutes must be a whole number from 0 to 10080 (one week); 0 lifts the mute.",
		},
		ErrorCodeTelegramLinkUnavailable: {
			ID: "LZ-7434", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "Telegram accounts cannot be linked on this deployment: its bot does not receive messages.",
		},
		ErrorCodeTelegramNotLinked: {
			ID: "LZ-7435", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "No Telegram account is linked. Link one first.",
		},
		ErrorCodeUUIDCollision: {
			Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
			Template: "A generated UUID collided with an existing one",
		},
	})
}
