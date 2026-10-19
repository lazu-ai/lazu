package helper

import (
	"fmt"
	"github.com/tidwall/gjson"

	"github.com/lazu-ai/lazu/internal/common"
)

// outboundResponsesBody is the subset of an outbound Responses request that
// decides whether server-side state is reachable.
type outboundResponsesBody struct {
	Store              []byte `json:"-"`
	PreviousResponseID string `json:"previous_response_id"`
	Input              []byte `json:"-"`
}

// ValidateOutboundResponsesStore rejects a Responses request that disables
// server-side persistence while still referencing server-side state. Upstream
// drops every item id when store=false, so these combinations always fail at
// the provider. The provider's own error ("Items are not persisted when
// `store` is set to false") reads like a configuration problem and sends
// people looking in the wrong place, so we reject them here and say what to do
// instead.
//
// This must run on the body that actually goes upstream, not on the client's
// request: a channel with disable_store strips `store` entirely (which makes
// server-side state usable again), and a param override can force
// `store: false` onto a request that never sent it.
//
// Echoing a reasoning item back with its `encrypted_content` is the supported
// store=false pattern and must keep working, so only items that carry no
// inline state are rejected.
func ValidateOutboundResponsesStore(jsonData []byte) error {
	body, ok := parseOutboundResponsesBody(jsonData)
	if !ok || !storeExplicitlyDisabled(body.Store) {
		return nil
	}

	if body.PreviousResponseID != "" {
		return fmt.Errorf("`previous_response_id` cannot be used together with `store: false`: "+
			"the upstream provider does not persist response %q when store is disabled, so it cannot be referenced on a later turn. "+
			"Either omit `store` (it defaults to true) to keep server-side conversation state, "+
			"or keep `store: false` and resend the full conversation inline in `input`",
			body.PreviousResponseID)
	}

	if itemID, kind := findUnresolvableInputItem(body.Input); kind != "" {
		switch kind {
		case "item_reference":
			return fmt.Errorf("`input` contains an `item_reference` to %q, which cannot be used together with `store: false`: "+
				"the upstream provider does not persist item ids when store is disabled. "+
				"Either omit `store` (it defaults to true), or keep `store: false` and inline the item's content in `input` instead of referencing it",
				itemID)
		case "reasoning":
			return fmt.Errorf("`input` contains reasoning item %q with no `encrypted_content`, which cannot be used together with `store: false`: "+
				"the upstream provider does not persist reasoning items when store is disabled. "+
				"Either omit `store` (it defaults to true), or keep `store: false` and send `include: [\"reasoning.encrypted_content\"]` "+
				"so the reasoning item can be echoed back with its `encrypted_content`",
				itemID)
		}
	}

	return nil
}

// parseOutboundResponsesBody reads only the fields we judge on. A body we
// cannot parse is left to the provider to reject.
func parseOutboundResponsesBody(jsonData []byte) (outboundResponsesBody, bool) {
	if len(jsonData) == 0 {
		return outboundResponsesBody{}, false
	}
	store := gjson.GetBytes(jsonData, "store")
	// The overwhelmingly common persisted request never needs input decoded.
	if store.Type != gjson.False {
		return outboundResponsesBody{Store: []byte(store.Raw)}, true
	}
	return outboundResponsesBody{
		Store:              []byte(store.Raw),
		PreviousResponseID: gjson.GetBytes(jsonData, "previous_response_id").Str,
		Input:              []byte(gjson.GetBytes(jsonData, "input").Raw),
	}, true
}

// storeExplicitlyDisabled reports whether the outbound body carries
// `store: false`. An absent or null store is not a conflict: upstream
// defaults it to true, which is also what a disable_store channel produces.
func storeExplicitlyDisabled(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var store *bool
	if err := common.Unmarshal(raw, &store); err != nil {
		return false
	}
	return store != nil && !*store
}

// findUnresolvableInputItem returns the id and kind of the first input item
// that can only be resolved from server-side state. It returns an empty kind
// when every item carries its own content.
func findUnresolvableInputItem(input []byte) (string, string) {
	if len(input) == 0 {
		return "", ""
	}
	// `input` is either a plain string prompt or an array of items; only the
	// array form can reference server-side state.
	var items []map[string]any
	if err := common.Unmarshal(input, &items); err != nil {
		return "", ""
	}
	for _, item := range items {
		itemType, _ := item["type"].(string)
		switch itemType {
		case "item_reference":
			id, _ := item["id"].(string)
			return id, "item_reference"
		case "reasoning":
			id, _ := item["id"].(string)
			if id == "" {
				continue
			}
			if encrypted, _ := item["encrypted_content"].(string); encrypted != "" {
				continue
			}
			return id, "reasoning"
		}
	}
	return "", ""
}
