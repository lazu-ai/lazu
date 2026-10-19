package types

import "net/http"

// Models, model operations, vendors, pricing, lanes and tiers.
// Numbers: LZ-7501–7599.
//
// Everything here except the catalog reads is behind admin or root auth, so
// most rows are operator rows: the console that shows them is the operator's
// own, and it may name lanes, sources and tiers.
const (
	// Model metadata, vendors and prefill groups.
	ErrorCodeModelNameTaken         ErrorCode = "model_name_taken"
	ErrorCodeVendorNameTaken        ErrorCode = "vendor_name_taken"
	ErrorCodePrefillGroupNameTaken  ErrorCode = "prefill_group_name_taken"
	ErrorCodeModelSyncNotConfigured ErrorCode = "model_sync_not_configured"
	ErrorCodeModelSyncSourceFailed  ErrorCode = "model_sync_source_unavailable"

	// Lanes (档位).
	ErrorCodeLaneNotFound     ErrorCode = "lane_not_found"
	ErrorCodeLaneBuiltin      ErrorCode = "lane_builtin_locked"
	ErrorCodeLaneEverEnabled  ErrorCode = "lane_ever_enabled"
	ErrorCodeLaneHasNoSources ErrorCode = "lane_has_no_sources"
	ErrorCodeLaneInUse        ErrorCode = "lane_in_use"

	// Tiers (等级).
	ErrorCodeTierNotFound   ErrorCode = "tier_not_found"
	ErrorCodeTierExists     ErrorCode = "tier_exists"
	ErrorCodeTierBuiltin    ErrorCode = "tier_builtin_locked"
	ErrorCodeTierHasMembers ErrorCode = "tier_has_accounts"

	// Sell prices and listing.
	ErrorCodeSellPriceMissing         ErrorCode = "sell_price_missing"
	ErrorCodeSellPriceModelListed     ErrorCode = "sell_price_model_listed"
	ErrorCodeSellPriceModelServed     ErrorCode = "sell_price_model_served"
	ErrorCodeSellPriceModelDiscounted ErrorCode = "sell_price_model_discounted"
	ErrorCodeModelListingUnpriced     ErrorCode = "model_listing_unpriced"
	ErrorCodeModelListingNoSource     ErrorCode = "model_listing_no_source"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		ErrorCodeModelNameTaken: {
			ID: "LZ-7501", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A model named {model|with this name} already exists.",
		},
		ErrorCodeVendorNameTaken: {
			ID: "LZ-7502", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A vendor named {vendor|with this name} already exists.",
		},
		ErrorCodePrefillGroupNameTaken: {
			ID: "LZ-7503", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A prefill group named {name|with this name} already exists.",
		},
		ErrorCodeModelSyncNotConfigured: {
			ID: "LZ-7504", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "Model metadata sync is not configured on this server. Set the metadata endpoint URLs and restart the server.",
		},
		ErrorCodeModelSyncSourceFailed: {
			ID: "LZ-7505", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
			Template: "The {source|external} model catalog could not be read. Nothing was changed; retry later.",
			Retry:    true,
		},

		ErrorCodeLaneNotFound: {
			ID: "LZ-7510", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Lane {lane|requested} does not exist.",
		},
		ErrorCodeLaneBuiltin: {
			ID: "LZ-7511", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Lane {lane|requested} is built in and cannot be stopped or deleted.",
		},
		ErrorCodeLaneEverEnabled: {
			ID: "LZ-7512", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Lane {lane|requested} has been enabled before. Stop it instead of deleting it.",
		},
		ErrorCodeLaneHasNoSources: {
			ID: "LZ-7513", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Lane {lane|requested} has no sources yet. Move a source into it before enabling it.",
		},
		ErrorCodeLaneInUse: {
			ID: "LZ-7514", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Lane {lane|requested} still has sources, prices or discounts, so it cannot be deleted.",
		},

		ErrorCodeTierNotFound: {
			ID: "LZ-7520", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceOperator,
			Template: "Tier {tier|requested} does not exist.",
		},
		ErrorCodeTierExists: {
			ID: "LZ-7521", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "A tier named {tier|with this name} already exists.",
		},
		ErrorCodeTierBuiltin: {
			ID: "LZ-7522", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Tier {tier|requested} is built in and cannot be deleted.",
		},
		ErrorCodeTierHasMembers: {
			ID: "LZ-7523", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Tier {tier|requested} still has accounts in it. Move them to another tier first.",
		},

		ErrorCodeSellPriceMissing: {
			ID: "LZ-7530", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceOperator,
			Template: "Enter at least one sell price.",
		},
		ErrorCodeSellPriceModelListed: {
			ID: "LZ-7531", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Model {model|requested} is still listed. Unlist it before removing its price.",
		},
		ErrorCodeSellPriceModelServed: {
			ID: "LZ-7532", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Enabled sources still serve model {model|requested}; without a price its requests would be refused.",
		},
		ErrorCodeSellPriceModelDiscounted: {
			ID: "LZ-7533", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "Model {model|requested} still has an active discount. Remove the discount before removing its price.",
		},
		ErrorCodeModelListingUnpriced: {
			ID: "LZ-7534", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "These models have no sell price on any lane and would be billed at $0: {models}.",
		},
		ErrorCodeModelListingNoSource: {
			ID: "LZ-7535", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceOperator,
			Template: "These models have no enabled source on lane {lane|requested}: {models}.",
		},
	})
}
