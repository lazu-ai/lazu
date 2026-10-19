package types

import "net/http"

// Users and sign-in. Numbers: LZ-2106–2199 (sessions and access tokens),
// LZ-2201–2299 (sign-in, registration, email codes, password reset, OAuth and
// OIDC), LZ-7001–7099 (user administration, profile, settings, identities,
// usernames).
const (
	ErrorCodePasswordLoginDisabled             ErrorCode = "password_login_disabled"
	ErrorCodeInvalidCredentials                ErrorCode = "invalid_credentials"
	ErrorCodeRegistrationClosed                ErrorCode = "registration_closed"
	ErrorCodePasswordRegistrationDisabled      ErrorCode = "password_registration_disabled"
	ErrorCodeEmailVerificationRequired         ErrorCode = "email_verification_required"
	ErrorCodeVerificationCodeInvalid           ErrorCode = "verification_code_invalid"
	ErrorCodeVerificationCodeAttemptsExhausted ErrorCode = "verification_code_attempts_exhausted"
	ErrorCodeEmailCodeAuthDisabled             ErrorCode = "email_code_auth_disabled"
	ErrorCodeEmailTransportUnavailable         ErrorCode = "email_transport_unavailable"
	ErrorCodeOriginalPasswordIncorrect         ErrorCode = "original_password_incorrect"
	ErrorCodeOAuthProviderNotConfigured        ErrorCode = "oauth_provider_not_configured"
	ErrorCodeOAuthCancelled                    ErrorCode = "oauth_cancelled"
	ErrorCodeOAuthProviderError                ErrorCode = "oauth_provider_error"
	ErrorCodeOAuthStateInvalid                 ErrorCode = "oauth_state_invalid"
	ErrorCodeOAuthExchangeFailed               ErrorCode = "oauth_exchange_failed"
	ErrorCodeOAuthIdentityInvalid              ErrorCode = "oauth_identity_invalid"
	ErrorCodeOAuthLoginFailed                  ErrorCode = "oauth_login_failed"
	ErrorCodeOAuthEmailUnverified              ErrorCode = "oauth_email_unverified"
	ErrorCodeOAuthEmailConflict                ErrorCode = "oauth_email_conflict"
	ErrorCodeOAuthIdentityConflict             ErrorCode = "oauth_identity_conflict"

	ErrorCodeUserNotFound                    ErrorCode = "user_not_found"
	ErrorCodeUserExists                      ErrorCode = "user_exists"
	ErrorCodeUserManageForbidden             ErrorCode = "user_manage_forbidden"
	ErrorCodeUserRoleElevationForbidden      ErrorCode = "user_role_elevation_forbidden"
	ErrorCodeRootUserProtected               ErrorCode = "root_user_protected"
	ErrorCodeUserRoleUnchanged               ErrorCode = "user_role_unchanged"
	ErrorCodeBalanceAdjustmentReasonRequired ErrorCode = "balance_adjustment_reason_required"
	ErrorCodeBalanceAdjustmentAmountZero     ErrorCode = "balance_adjustment_amount_zero"
	ErrorCodeUsernameInvalid                 ErrorCode = "username_invalid"
	ErrorCodeUsernameUnavailable             ErrorCode = "username_unavailable"
	ErrorCodeUsernameChangeCooldown          ErrorCode = "username_change_cooldown"
	ErrorCodeIdentityUnlinkUnavailable       ErrorCode = "identity_unlink_unavailable"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		// ── LZ-22xx · sign-in, registration, email codes, reset, OAuth ──
		ErrorCodePasswordLoginDisabled: {
			ID: "LZ-2201", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Password sign-in is turned off on this deployment. Use another sign-in method.",
		},
		ErrorCodeInvalidCredentials: {
			ID: "LZ-2202", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
			Template: "The username or password is incorrect, or the account is disabled.",
		},
		ErrorCodeRegistrationClosed: {
			ID: "LZ-2203", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "New account registration is closed on this deployment.",
		},
		ErrorCodePasswordRegistrationDisabled: {
			ID: "LZ-2204", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Registration with a password is turned off. Sign up with an email code or a linked account.",
		},
		ErrorCodeEmailVerificationRequired: {
			ID: "LZ-2205", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "This deployment requires a verified email address. Enter the address and its verification code.",
		},
		ErrorCodeVerificationCodeInvalid: {
			ID: "LZ-2206", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The verification code is incorrect or has expired.",
		},
		ErrorCodeVerificationCodeAttemptsExhausted: {
			ID: "LZ-2207", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
			Template: "Too many incorrect codes; this code no longer works. Request a new code.",
		},
		ErrorCodeEmailCodeAuthDisabled: {
			ID: "LZ-2210", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Email code sign-in is turned off on this deployment.",
		},
		ErrorCodeEmailTransportUnavailable: {
			ID: "LZ-2211", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "Email delivery is not configured, so email codes cannot be sent.",
		},
		ErrorCodeOriginalPasswordIncorrect: {
			ID: "LZ-2215", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The current password is incorrect.",
		},
		ErrorCodeOAuthProviderNotConfigured: {
			ID: "LZ-2216", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "This sign-in method is not configured or is unavailable.",
		},
		ErrorCodeOAuthCancelled: {
			ID: "LZ-2217", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Authorization was cancelled at the sign-in provider.",
		},
		ErrorCodeOAuthProviderError: {
			ID: "LZ-2218", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "The sign-in provider returned an error.",
		},
		ErrorCodeOAuthStateInvalid: {
			ID: "LZ-2219", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The sign-in attempt expired or was started in another browser. Start again.",
		},
		ErrorCodeOAuthExchangeFailed: {
			ID: "LZ-2220", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "The sign-in provider did not confirm this sign-in.",
		},
		ErrorCodeOAuthIdentityInvalid: {
			ID: "LZ-2221", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
			Template: "The identity returned by the sign-in provider could not be verified.",
		},
		ErrorCodeOAuthLoginFailed: {
			ID: "LZ-2222", HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "Sign-in did not complete.",
		},
		ErrorCodeOAuthEmailUnverified: {
			ID: "LZ-2223", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "The provider account has no verified email address.",
		},
		ErrorCodeOAuthEmailConflict: {
			ID: "LZ-2224", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This email address matches more than one account.",
		},
		ErrorCodeOAuthIdentityConflict: {
			ID: "LZ-2225", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This provider account is already linked to another user.",
		},

		// ── LZ-70xx · users, profile, settings, identities, usernames ──
		ErrorCodeUserNotFound: {
			ID: "LZ-7001", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "The user was not found.",
		},
		ErrorCodeUserExists: {
			ID: "LZ-7002", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "The username or email is already registered, or belonged to a deleted account.",
		},
		ErrorCodeUserManageForbidden: {
			ID: "LZ-7003", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "You cannot {action|manage} an account whose role is equal to or higher than yours.",
		},
		ErrorCodeUserRoleElevationForbidden: {
			ID: "LZ-7004", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "You cannot give an account a role equal to or higher than your own.",
		},
		ErrorCodeRootUserProtected: {
			ID: "LZ-7005", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "An administrator account cannot be {action|changed this way}.",
		},
		ErrorCodeUserRoleUnchanged: {
			ID: "LZ-7006", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This account already has the {role|requested} role.",
		},
		ErrorCodeBalanceAdjustmentReasonRequired: {
			ID: "LZ-7007", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "A balance adjustment needs a reason.",
		},
		ErrorCodeBalanceAdjustmentAmountZero: {
			ID: "LZ-7008", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "A balance adjustment amount cannot be zero.",
		},
		ErrorCodeUsernameInvalid: {
			ID: "LZ-7009", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "A username is 2 to 20 letters, digits, underscores or hyphens, starting with a letter or digit.",
		},
		ErrorCodeUsernameUnavailable: {
			ID: "LZ-7010", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This username is {reason|taken}. Choose another.",
		},
		ErrorCodeUsernameChangeCooldown: {
			ID: "LZ-7011", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
			Template: "A username can be changed once every {cooldown_days|30} days.",
		},
		ErrorCodeIdentityUnlinkUnavailable: {
			ID: "LZ-7012", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Linked sign-in methods cannot be removed yet.",
		},
	})
}
