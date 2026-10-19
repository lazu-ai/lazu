package types

import "net/http"

// Team projects, project wallets and invitations. Numbers: LZ-7600–7699.
//
// Several codes predate the catalog and keep their names because the project
// console branches on them (undo_expired, resend_too_soon, already_member,
// ...). Key limits and Key secrets are keys-domain rows.
const (
	ErrorCodeFeatureUnavailable     ErrorCode = "feature_unavailable"
	ErrorCodeProjectContextMismatch ErrorCode = "project_context_mismatch"
	ErrorCodeProjectNotFound        ErrorCode = "project_not_found"

	ErrorCodeProjectMemberNotFound     ErrorCode = "project_member_not_found"
	ErrorCodeInviteNotFound            ErrorCode = "invite_not_found"
	ErrorCodeBudgetRequestNotFound     ErrorCode = "budget_request_not_found"
	ErrorCodeUndoExpired               ErrorCode = "undo_expired"
	ErrorCodeInvalidBudget             ErrorCode = "invalid_budget"
	ErrorCodeInvalidEffortCap          ErrorCode = "invalid_effort_cap"
	ErrorCodeInvalidSettings           ErrorCode = "invalid_settings"
	ErrorCodeCannotEditOwnBudget       ErrorCode = "cannot_edit_own_budget"
	ErrorCodeCannotChangeOwner         ErrorCode = "cannot_change_owner"
	ErrorCodeConfirmMismatch           ErrorCode = "confirm_mismatch"
	ErrorCodeResendTooSoon             ErrorCode = "resend_too_soon"
	ErrorCodeRequestPending            ErrorCode = "request_pending"
	ErrorCodeInvalidPagination         ErrorCode = "invalid_pagination"
	ErrorCodeAlreadyMember             ErrorCode = "already_member"
	ErrorCodeAlreadyInvited            ErrorCode = "already_invited"
	ErrorCodeInviteExpired             ErrorCode = "invite_expired"
	ErrorCodeInviteRevoked             ErrorCode = "invite_revoked"
	ErrorCodeInviteEmailMismatch       ErrorCode = "invite_email_mismatch"
	ErrorCodeInviteEmailUnverified     ErrorCode = "invite_email_unverified"
	ErrorCodeInviteeFeatureUnavailable ErrorCode = "invitee_feature_unavailable"
	ErrorCodeInviteEmailInvalid        ErrorCode = "invite_email_invalid"
	ErrorCodeInviteRoleInvalid         ErrorCode = "invite_role_invalid"
	ErrorCodeProjectNameInvalid        ErrorCode = "project_name_invalid"
	ErrorCodeProjectDeletionDependency ErrorCode = "project_deletion_dependency"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		ErrorCodeFeatureUnavailable: {
			ID: "LZ-7601", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Team projects are not enabled for this account.",
		},
		ErrorCodeProjectContextMismatch: {
			ID: "LZ-7602", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The project in the URL and the project in the request do not match.",
		},
		ErrorCodeProjectNotFound: {
			ID: "LZ-7603", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "The project was not found, or you are no longer a member.",
		},
		ErrorCodeProjectMemberNotFound: {
			ID: "LZ-7604", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "This person is not a member of the project.",
		},
		ErrorCodeInviteNotFound: {
			ID: "LZ-7605", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "This invitation was not found. The link may be incomplete.",
		},
		ErrorCodeBudgetRequestNotFound: {
			ID: "LZ-7606", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "This budget request was not found.",
		},
		ErrorCodeUndoExpired: {
			ID: "LZ-7607", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "The undo window for this removal has passed. Invite the person again.",
		},
		ErrorCodeInvalidBudget: {
			ID: "LZ-7608", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The budget must be a non-negative whole amount, or null for no limit, and the warning threshold between 1 and 99 percent.",
		},
		ErrorCodeInvalidEffortCap: {
			ID: "LZ-7609", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The reasoning effort cap is not a known level.",
		},
		ErrorCodeInvalidSettings: {
			ID: "LZ-7610", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Project setting {field|value} is not valid.",
		},
		ErrorCodeCannotEditOwnBudget: {
			ID: "LZ-7611", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "You cannot change your own budget.",
		},
		ErrorCodeCannotChangeOwner: {
			ID: "LZ-7612", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "The project owner's role and status cannot be changed.",
		},
		ErrorCodeConfirmMismatch: {
			ID: "LZ-7613", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The project name you typed does not match the project.",
		},
		ErrorCodeResendTooSoon: {
			ID: "LZ-7614", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
			Template: "This invitation was just sent. Resend it after {retry_after_seconds|60} seconds.",
		},
		ErrorCodeRequestPending: {
			ID: "LZ-7615", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "A budget request is already pending.",
		},
		ErrorCodeInvalidPagination: {
			ID: "LZ-7616", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "page must start at 1 and page_size must be between 1 and 100.",
		},
		ErrorCodeAlreadyMember: {
			ID: "LZ-7617", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This person is already a member of the project.",
		},
		ErrorCodeAlreadyInvited: {
			ID: "LZ-7618", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This address already has a pending invitation.",
		},
		ErrorCodeInviteExpired: {
			ID: "LZ-7619", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This invitation has expired.",
		},
		ErrorCodeInviteRevoked: {
			ID: "LZ-7620", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This invitation was revoked.",
		},
		ErrorCodeInviteEmailMismatch: {
			ID: "LZ-7621", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "This invitation was sent to a different e-mail address than the signed-in account.",
		},
		ErrorCodeInviteEmailUnverified: {
			ID: "LZ-7622", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "Verify the account's e-mail address before accepting the invitation.",
		},
		ErrorCodeInviteeFeatureUnavailable: {
			ID: "LZ-7623", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
			Template: "The invited account cannot join a team project yet.",
		},
		ErrorCodeInviteEmailInvalid: {
			ID: "LZ-7624", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The invitation e-mail address is not valid.",
		},
		ErrorCodeInviteRoleInvalid: {
			ID: "LZ-7625", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "An invitation role must be admin or member.",
		},
		ErrorCodeProjectNameInvalid: {
			ID: "LZ-7626", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The project name is empty or too long.",
		},
		ErrorCodeProjectDeletionDependency: {
			ID: "LZ-7627", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "This account still owns or belongs to a team project with members, payments or Keys that depend on it.",
		},
	})
}
