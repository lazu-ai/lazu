package types

import "net/http"

// Media and video jobs, the Files API, search backends and admin uploads.
// Numbers: LZ-7800–7899.
//
// Media job terminal codes (artifact_too_large, video_artifact_missing, ...)
// are stored on the job and returned in its `error.code`; they never leave
// through an HTTP error, but carry rows so the console and docs can explain
// them.
const (
	// Files API (/v1/files and the console's /api/files).
	ErrorCodePurposeNotSupported   ErrorCode = "purpose_not_supported"
	ErrorCodeInvalidFile           ErrorCode = "invalid_file"
	ErrorCodeFileTooLarge          ErrorCode = "file_too_large"
	ErrorCodeFileNotFound          ErrorCode = "file_not_found"
	ErrorCodeFileExpired           ErrorCode = "file_expired"
	ErrorCodeFileDereferenceFailed ErrorCode = "file_dereference_failed"
	ErrorCodeFileStorageFailed     ErrorCode = "file_storage_failed"
	ErrorCodeStorageNotConfigured  ErrorCode = "storage_not_configured"
	ErrorCodeStorageQuotaExceeded  ErrorCode = "storage_quota_exceeded"

	// Media and video jobs (/v1/media_jobs, /v1/videos).
	ErrorCodeUnsupportedMediaKind      ErrorCode = "unsupported_media_kind"
	ErrorCodeUnsupportedMediaStatus    ErrorCode = "unsupported_media_status"
	ErrorCodeMediaJobNotFound          ErrorCode = "media_job_not_found"
	ErrorCodeArtifactFileNotFound      ErrorCode = "artifact_file_not_found"
	ErrorCodeVideoNotFound             ErrorCode = "video_not_found"
	ErrorCodeInvalidPrecreatedMediaJob ErrorCode = "invalid_precreated_media_job"
	ErrorCodeArtifactTooLarge          ErrorCode = "artifact_too_large"
	ErrorCodeVideoArtifactMissing      ErrorCode = "video_artifact_missing"
	ErrorCodeVideoNotReady             ErrorCode = "video_not_ready"
	ErrorCodeInvalidWait               ErrorCode = "invalid_wait"

	// Operator pages: search backends, data uploads, repairs.
	ErrorCodeSearchBackendNotFound        ErrorCode = "search_backend_not_found"
	ErrorCodeSearchBackendProviderInvalid ErrorCode = "search_backend_provider_invalid"
	ErrorCodeUploadFileNotFound           ErrorCode = "upload_file_not_found"
	ErrorCodeUploadKeyNotAllowed          ErrorCode = "upload_key_not_allowed"
	ErrorCodeUploadFailed                 ErrorCode = "upload_failed"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		// ── LZ-780x · files ───────────────────────────────────────────
		ErrorCodePurposeNotSupported: {
			ID: "LZ-7801", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "File purpose '{purpose|requested}' is not supported. Supported purposes: user_data, vision.",
		},
		ErrorCodeInvalidFile: {
			ID: "LZ-7802", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The uploaded file was not accepted: {reason|check its type and contents}.",
		},
		ErrorCodeFileTooLarge: {
			ID: "LZ-7803", HTTPStatus: http.StatusRequestEntityTooLarge, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The file exceeds the {limit|allowed} size limit.",
		},
		ErrorCodeFileNotFound: {
			ID: "LZ-7804", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "File {file_id|requested} was not found or is not accessible.",
		},
		ErrorCodeFileExpired: {
			ID: "LZ-7805", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "File {file_id|requested} has expired. Upload it again.",
		},
		ErrorCodeFileDereferenceFailed: {
			ID: "LZ-7806", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "File {file_id|referenced} could not be loaded from storage. Retry the request.",
		},
		ErrorCodeFileStorageFailed: {
			ID: "LZ-7807", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "File storage did not complete the {operation|request}. Retry shortly.",
		},
		ErrorCodeStorageNotConfigured: {
			ID: "LZ-7808", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "File storage is not configured on this deployment.",
		},
		ErrorCodeStorageQuotaExceeded: {
			ID: "LZ-7809", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
			Template: "File storage is full: an account may keep {limit|its storage allowance} of files. Delete files you no longer need.",
		},
		// ── LZ-782x · media and video jobs ────────────────────────────
		ErrorCodeUnsupportedMediaKind: {
			ID: "LZ-7821", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Media kind '{kind|requested}' is not supported. Use image or video.",
		},
		ErrorCodeUnsupportedMediaStatus: {
			ID: "LZ-7822", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "Status '{status|requested}' is not supported. Use pending, running, succeeded or failed.",
		},
		ErrorCodeMediaJobNotFound: {
			ID: "LZ-7823", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "Media job {job_id|requested} was not found.",
		},
		ErrorCodeArtifactFileNotFound: {
			ID: "LZ-7824", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "Artifact file {file_id|requested} was not found.",
		},
		ErrorCodeVideoNotFound: {
			ID: "LZ-7825", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
			Template: "Video job {job_id|requested} was not found.",
		},
		ErrorCodeInvalidPrecreatedMediaJob: {
			ID: "LZ-7826", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "The media job named in this request {reason|does not match the request}.",
		},
		ErrorCodeArtifactTooLarge: {
			ID: "LZ-7827", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "The generated file exceeds the maximum size Lazu can store.",
		},
		ErrorCodeVideoArtifactMissing: {
			ID: "LZ-7828", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
			Template: "The video finished without a file to download.",
		},
		ErrorCodeVideoNotReady: {
			ID: "LZ-7829", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
			Template: "Video job {job_id|requested} has no file to download: its status is {status|not succeeded}.",
		},
		ErrorCodeInvalidWait: {
			ID: "LZ-7830", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
			Template: "wait must be a whole number of seconds from 0 to {max|60}.",
		},
		// ── LZ-786x · operator pages ──────────────────────────────────
		ErrorCodeSearchBackendNotFound: {
			ID: "LZ-7861", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "The search backend was not found.",
		},
		ErrorCodeSearchBackendProviderInvalid: {
			ID: "LZ-7862", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Search provider '{provider|requested}' is not supported or does not match the channel type.",
		},
		ErrorCodeUploadFileNotFound: {
			ID: "LZ-7864", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Data file {key|requested} was not found.",
		},
		ErrorCodeUploadKeyNotAllowed: {
			ID: "LZ-7865", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "{key|This path} cannot be uploaded: {reason|choose a regular data file}.",
		},
		ErrorCodeUploadFailed: {
			ID: "LZ-7866", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "Object storage did not accept {key|the file}. Check the storage settings and retry.",
		},
	})
}
