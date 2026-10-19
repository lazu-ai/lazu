package types

import "net/http"

// Channels, multi-key channels, channel tests and the channel-health controls.
// Every row here is reached only from operator pages. Numbers: LZ-7300–7399.
const (
	ErrorCodeChannelNotFound             ErrorCode = "channel_not_found"
	ErrorCodeChannelSearchBackendManaged ErrorCode = "channel_search_backend_managed"
	ErrorCodeChannelLaneInvalid          ErrorCode = "channel_lane_invalid"
	ErrorCodeChannelSettingsInvalid      ErrorCode = "channel_settings_invalid"
	ErrorCodeChannelModelNameTooLong     ErrorCode = "channel_model_name_too_long"
	ErrorCodeChannelNotMultiKey          ErrorCode = "channel_not_multi_key"
	ErrorCodeChannelKeyIndexInvalid      ErrorCode = "channel_key_index_invalid"
	ErrorCodeChannelLastKey              ErrorCode = "channel_last_key"
	ErrorCodeChannelKeysNoneEligible     ErrorCode = "channel_keys_none_eligible"
	ErrorCodeChannelBalanceUnsupported   ErrorCode = "channel_balance_unsupported"
	ErrorCodeChannelBalanceQueryFailed   ErrorCode = "channel_balance_query_failed"
	ErrorCodeChannelUpstreamModelsFailed ErrorCode = "channel_upstream_models_failed"
	ErrorCodeChannelTaskRunning          ErrorCode = "channel_task_running"
	ErrorCodeChannelTestFailed           ErrorCode = "channel_test_failed"
	ErrorCodeChannelTestNotSent          ErrorCode = "channel_test_not_sent"
	ErrorCodeChannelRouteNotFound        ErrorCode = "channel_route_not_found"
	ErrorCodeRouteObservationIncomplete  ErrorCode = "route_observation_incomplete"
	ErrorCodeRouteObservationNone        ErrorCode = "route_observation_none"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		ErrorCodeChannelNotFound: {
			ID: "LZ-7301", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Channel {channel_id|requested} was not found. It may have been deleted.",
		},
		ErrorCodeChannelSearchBackendManaged: {
			ID: "LZ-7302", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "This channel is a search backend. Create and edit it on the Search backends page.",
		},
		ErrorCodeChannelLaneInvalid: {
			ID: "LZ-7303", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Lane {lane|given} is not a known lane. A channel belongs to exactly one existing lane.",
		},
		ErrorCodeChannelSettingsInvalid: {
			ID: "LZ-7304", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "The channel's {field|extra settings} could not be read. Check the JSON and its values.",
		},
		ErrorCodeChannelModelNameTooLong: {
			ID: "LZ-7305", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Model name {model|given} is longer than {max|255} characters.",
		},
		ErrorCodeChannelNotMultiKey: {
			ID: "LZ-7306", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Channel {channel_id|requested} does not use multiple keys, so its keys cannot be managed one by one.",
		},
		ErrorCodeChannelKeyIndexInvalid: {
			ID: "LZ-7307", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Key index {key_index|given} is out of range; the channel has {key_count|fewer} keys.",
		},
		ErrorCodeChannelLastKey: {
			ID: "LZ-7308", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A multi-key channel must keep at least one key. Add another key before deleting this one.",
		},
		ErrorCodeChannelKeysNoneEligible: {
			ID: "LZ-7309", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "No key of this channel matches the action {action|requested}.",
		},
		ErrorCodeChannelBalanceUnsupported: {
			ID: "LZ-7310", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Balance lookup is not supported for this channel ({reason|channel_type}).",
		},
		ErrorCodeChannelBalanceQueryFailed: {
			ID: "LZ-7311", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The provider did not return a balance (upstream status {status|none}).",
		},
		ErrorCodeChannelUpstreamModelsFailed: {
			ID: "LZ-7312", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The provider did not return a model list (upstream status {status|none}).",
		},
		ErrorCodeChannelTaskRunning: {
			ID: "LZ-7313", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A {task|channel maintenance} task is already running. Wait for it to finish.",
		},
		ErrorCodeChannelTestFailed: {
			ID: "LZ-7314", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The channel test failed: the provider answered with {cause_code|an error}.",
		},
		ErrorCodeChannelTestNotSent: {
			ID: "LZ-7315", HTTPStatus: http.StatusUnprocessableEntity, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "The test request could not be built for this channel, so nothing was sent to the provider.",
		},
		ErrorCodeChannelRouteNotFound: {
			ID: "LZ-7316", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Channel {channel_id|requested} does not serve model {model|requested} in lane {lane|requested}.",
		},
		ErrorCodeRouteObservationIncomplete: {
			ID: "LZ-7317", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Channel {channel_id|requested} is still under observation for {model|its new models}. It can go live after seven days, once every check has passed.",
		},
		ErrorCodeRouteObservationNone: {
			ID: "LZ-7318", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Channel {channel_id|requested} has no routes under observation.",
		},
	})
}
