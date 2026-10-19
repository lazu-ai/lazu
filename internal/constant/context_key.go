package constant

type ContextKey string

const (
	ContextKeyProjectID      ContextKey = "project_id"
	ContextKeyPayerUserID    ContextKey = "payer_user_id"
	ContextKeyActorUserID    ContextKey = "actor_user_id"
	ContextKeyTokenCountMeta ContextKey = "token_count_meta"
	ContextKeyPromptTokens   ContextKey = "prompt_tokens"

	ContextKeyAnyProviderAttempted ContextKey = "any_provider_attempted"
	ContextKeyOriginalModel        ContextKey = "original_model"
	// ContextKeyRequestModel carries the model name the caller asked for, set
	// as soon as the request body is parsed — before channel selection, and
	// therefore before ContextKeyOriginalModel exists. Rejections raised in
	// the auth/distribute chain (model_not_allowed, budget limits) need it so
	// their log row is not written with an empty model_name.
	ContextKeyRequestModel     ContextKey = "request_model"
	ContextKeyRequestStartTime ContextKey = "request_start_time"

	/* token related keys */
	ContextKeyTokenUnlimited         ContextKey = "token_unlimited_quota"
	ContextKeyTokenUsageAlertPercent ContextKey = "token_usage_alert_percent"
	ContextKeyTokenKey               ContextKey = "token_key"
	ContextKeyTokenId                ContextKey = "token_id"
	ContextKeyTokenProduct           ContextKey = "token_product"
	ContextKeyTokenMetadata          ContextKey = "token_metadata"
	ContextKeyTokenGroup             ContextKey = "token_group"
	ContextKeyTokenSpecificChannelId ContextKey = "specific_channel_id"
	ContextKeyTokenRateLimitsEnabled ContextKey = "token_rate_limits_enabled"
	ContextKeyTokenRPMLimit          ContextKey = "token_rpm_limit"
	ContextKeyTokenTPMLimit          ContextKey = "token_tpm_limit"
	ContextKeyTokenModelLimitEnabled ContextKey = "token_model_limit_enabled"
	ContextKeyTokenModelLimit        ContextKey = "token_model_limit"
	// ContextKeyMemberModelLimit is the project member's own model allowlist.
	ContextKeyMemberModelLimit ContextKey = "member_model_limit"
	// Reasoning-effort cap for this request and, when the gateway lowered the
	// effort, what the caller asked for and what ran.
	ContextKeyEffortCap ContextKey = "effort_cap"
	// ContextKeyLaneRequested holds the lane a Key asked for when the request
	// was served on the stable lane instead, so the usage log can say so.
	ContextKeyLaneRequested           ContextKey = "lane_requested"
	ContextKeyEffortCapSource         ContextKey = "effort_cap_source"
	ContextKeyEffortRequested         ContextKey = "effort_requested"
	ContextKeyEffortApplied           ContextKey = "effort_applied"
	ContextKeyTokenModelScopeVendors  ContextKey = "token_model_scope_vendors"
	ContextKeyTokenVendorLimits       ContextKey = "token_vendor_limits"
	ContextKeyTokenVendorLimitEnabled ContextKey = "token_vendor_limit_enabled"
	ContextKeyTokenVendorGroups       ContextKey = "token_vendor_groups"
	ContextKeyTokenModelLanes         ContextKey = "token_model_lanes"
	ContextKeyTokenSearchLimitEnabled ContextKey = "token_search_limit_enabled"
	ContextKeyTokenSearchProviders    ContextKey = "token_search_providers"
	ContextKeyTokenSearchDomainAllow  ContextKey = "token_search_domain_allowlist"
	ContextKeyEphemeralCredential     ContextKey = "ephemeral_credential"

	/* channel related keys */
	ContextKeyChannelId                ContextKey = "channel_id"
	ContextKeyChannelName              ContextKey = "channel_name"
	ContextKeyChannelCreateTime        ContextKey = "channel_create_time"
	ContextKeyChannelBaseUrl           ContextKey = "base_url"
	ContextKeyChannelType              ContextKey = "channel_type"
	ContextKeyChannelSetting           ContextKey = "channel_setting"
	ContextKeyChannelOtherSetting      ContextKey = "channel_other_setting"
	ContextKeyChannelParamOverride     ContextKey = "param_override"
	ContextKeyChannelHeaderOverride    ContextKey = "header_override"
	ContextKeyChannelOrganization      ContextKey = "channel_organization"
	ContextKeyChannelAutoBan           ContextKey = "auto_ban"
	ContextKeyChannelModelMapping      ContextKey = "model_mapping"
	ContextKeyChannelStatusCodeMapping ContextKey = "status_code_mapping"
	ContextKeyChannelIsMultiKey        ContextKey = "channel_is_multi_key"
	ContextKeyChannelMultiKeyIndex     ContextKey = "channel_multi_key_index"
	ContextKeyChannelKey               ContextKey = "channel_key"
	ContextKeyChannelAPIVersion        ContextKey = "channel_api_version"

	/* user related keys */
	ContextKeyUserId          ContextKey = "id"
	ContextKeyUserSetting     ContextKey = "user_setting"
	ContextKeyUserQuota       ContextKey = "user_quota"
	ContextKeyUserStatus      ContextKey = "user_status"
	ContextKeyUserEmail       ContextKey = "user_email"
	ContextKeyUsingGroup      ContextKey = "group"
	ContextKeyUserName        ContextKey = "username"
	ContextKeyUserHasToppedUp ContextKey = "user_has_topped_up"
	ContextKeyUserPricePlanID ContextKey = "user_price_plan_id"
	ContextKeyUserRPMLimit    ContextKey = "user_rpm_limit"
	ContextKeyUserTPMLimit    ContextKey = "user_tpm_limit"

	ContextKeyRelayInfo               ContextKey = "relay_info"
	ContextKeyTextgenParsed           ContextKey = "textgen_parsed_request"
	ContextKeyProtocolPlan            ContextKey = "protocol_relay_plan"
	ContextKeyProtocolAdmissionTokens ContextKey = "protocol_admission_prompt_tokens"
	ContextKeyProtocolLocalTokens     ContextKey = "protocol_local_prompt_tokens"
	ContextKeyProtocolTokenDelta      ContextKey = "protocol_admission_token_delta"
	ContextKeyProtocolTokenSkip       ContextKey = "protocol_prompt_token_shadow_skip"
	ContextKeyProtocolStreamTerminal  ContextKey = "protocol_stream_terminal"
	ContextKeyProtocolStreamSettle    ContextKey = "protocol_stream_settle"
	ContextKeyOSSURLApplied           ContextKey = "oss_url_applied"
	ContextKeyIsStream                ContextKey = "is_stream"
	ContextKeyModelLogPayload         ContextKey = "model_log_payload"
	ContextKeySoraRemixSourceVideo    ContextKey = "sora_remix_source_video"
	ContextKeyModelLogWritten         ContextKey = "model_log_written"
	ContextKeyRoutingOutcomeUsage     ContextKey = "routing_outcome_usage"
	ContextKeySuppressRelayTelemetry  ContextKey = "suppress_relay_telemetry"
	// ContextKeyProviderAttempted is set only immediately before an outbound
	// provider call. Health accounting uses it to keep gateway-local failures
	// (quota, pricing, request conversion) out of channel reliability.
	ContextKeyProviderAttempted         ContextKey = "provider_attempted"
	ContextKeyDecisionUpstreamStartedAt ContextKey = "decision_upstream_started_at"

	// ContextKeyUpstreamRetryAfter carries the upstream's Retry-After hint for
	// the attempt that just failed, in seconds. The retry loop prefers it over
	// its own backoff schedule. Reset per attempt so a stale hint from an
	// earlier channel cannot stretch a later wait.
	ContextKeyUpstreamRetryAfter ContextKey = "upstream_retry_after"

	// ContextKeyStreamFirstEventFailed marks a stream whose very first event was
	// an upstream failure, before any byte reached the client. Such an attempt is
	// indistinguishable from a pre-stream error for retry purposes.
	ContextKeyStreamFirstEventFailed ContextKey = "stream_first_event_failed"

	// Request attribution — populated by the distributor middleware and
	// surfaced into the consume log + model-responses jsonl so we can
	// answer "who sent this and was it part of a multi-turn session".
	ContextKeySessionID ContextKey = "session_id"
	ContextKeyAgentID   ContextKey = "agent_id"
	// Task grouping resolved from the agent's own headers; see
	// middleware/agent_session.go.
	ContextKeySessionSource       ContextKey = "session_source"
	ContextKeyThreadID            ContextKey = "thread_id"
	ContextKeyParentThreadID      ContextKey = "parent_thread_id"
	ContextKeyRequestKind         ContextKey = "request_kind"
	ContextKeySessionFirstMessage ContextKey = "session_first_message"
	ContextKeyClientFramework     ContextKey = "client_framework"
	ContextKeyClientVersion       ContextKey = "client_version"
	ContextKeyClientUserAgent     ContextKey = "client_user_agent"
	ContextKeyRealClientIP        ContextKey = "real_client_ip"
	// ISO 3166-1 alpha-2 country code from the CF-IPCountry header. "XX"
	// when Cloudflare could not determine the country (e.g. Tor exit
	// nodes, malformed IPs). Empty when the request bypassed CF.
	ContextKeyClientCountry ContextKey = "client_country"

	ContextKeyRequestErrorMessage       ContextKey = "request_error_message"
	ContextKeyRequestErrorCode          ContextKey = "request_error_code"
	ContextKeyRequestErrorType          ContextKey = "request_error_type"
	ContextKeyRequestAuthKeyFingerprint ContextKey = "request_auth_key_fingerprint"
	ContextKeyRequestAuthKeyHint        ContextKey = "request_auth_key_hint"
	ContextKeyRequestAuthKeySource      ContextKey = "request_auth_key_source"
	ContextKeyRequestAuthKeyPresent     ContextKey = "request_auth_key_present"
)
