package setting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/lazu-ai/lazu/internal/types"
)

// 档位 (channel lane) — the supply tier a request is routed through.
//
// This used to be a pair of constants (direct|cheap) checked by a switch, which
// meant "add a lane" was a code change touching every call site that validated
// one. Operations needs to add a lane with a name, a slug and a colour, so the
// pair became this registry, persisted as the `ChannelLanes` option.
//
// Two invariants the console depends on:
//   - A lane's Slug is its identity in Channel.Group, ModelSellPrice.ChannelGroup,
//     PricePlan.Lanes and the token's group. It is assigned once and
//     never edited, because renaming it would orphan every row that carries it.
//   - A lane that has ever been enabled can be disabled but never deleted: the
//     billing history and the sell prices written against it must keep resolving.
//     Deletion is only for a lane created by mistake and never turned on.
type ChannelLane struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Note        string `json:"note,omitempty"`
	Color       string `json:"color,omitempty"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin,omitempty"`
	EverEnabled bool   `json:"ever_enabled,omitempty"`
	Sort        int    `json:"sort"`
	// PriceRatio is the lane's default sell price as a percent of the official
	// list price. A model's sell price that follows the official price uses it
	// unless the model sets its own ratio. Zero means unset (read as 100).
	PriceRatio float64 `json:"price_ratio,omitempty"`
}

// The two builtin lanes. The agent lane serves coding agents from
// third-party subscription pools; its slug "cheap" predates that name and is
// kept, because a slug is never renamed (see ChannelLane).
const (
	LaneSlugDirect = "direct"
	LaneSlugAgent  = "cheap"
)

var defaultChannelLanes = []ChannelLane{
	{Slug: LaneSlugDirect, Name: "稳定", Note: "官方与云厂商直连，优先保稳定", Color: "evidence", Enabled: true, Builtin: true, EverEnabled: true, Sort: 0, PriceRatio: 100},
	{Slug: LaneSlugAgent, Name: "Agent 专线", Note: "第三方订阅池，只给 Claude Code、Codex 等编码 Agent 用", Color: "warning", Enabled: true, Builtin: true, EverEnabled: true, Sort: 1, PriceRatio: 50},
}

var (
	channelLanes      = cloneChannelLanes(defaultChannelLanes)
	channelLanesMutex sync.RWMutex
)

var laneSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func cloneChannelLanes(src []ChannelLane) []ChannelLane {
	out := make([]ChannelLane, len(src))
	copy(out, src)
	return out
}

// NormalizeLaneSlug lowercases and trims, and maps the historical aliases for
// the direct lane ("", "default", "auto") onto it. Anything else is passed
// through unchanged so an unknown slug stays visible to the caller rather than
// silently becoming the default.
func NormalizeLaneSlug(slug string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	switch s {
	case "", "default", "auto":
		return LaneSlugDirect
	}
	return s
}

func ChannelLanes2JSONString() string {
	channelLanesMutex.RLock()
	defer channelLanesMutex.RUnlock()
	buf, err := json.Marshal(channelLanes)
	if err != nil {
		return "[]"
	}
	return string(buf)
}

// UpdateChannelLanesByJSONString replaces the registry wholesale. The two
// builtin lanes are re-seeded when a payload omits them: losing `direct` would
// leave every unrouted request without a lane to fall back to.
func UpdateChannelLanesByJSONString(jsonStr string) error {
	next := cloneChannelLanes(defaultChannelLanes)
	if strings.TrimSpace(jsonStr) != "" {
		var parsed []ChannelLane
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			return err
		}
		next = parsed
	}
	merged, err := sanitizeChannelLanes(next)
	if err != nil {
		return err
	}
	channelLanesMutex.Lock()
	defer channelLanesMutex.Unlock()
	channelLanes = merged
	return nil
}

func sanitizeChannelLanes(in []ChannelLane) ([]ChannelLane, error) {
	seen := make(map[string]bool, len(in))
	out := make([]ChannelLane, 0, len(in)+len(defaultChannelLanes))
	for _, lane := range in {
		lane.Slug = NormalizeLaneSlug(lane.Slug)
		if !laneSlugPattern.MatchString(lane.Slug) {
			return nil, fmt.Errorf("invalid lane slug: %s", lane.Slug)
		}
		if seen[lane.Slug] {
			return nil, fmt.Errorf("duplicate lane slug: %s", lane.Slug)
		}
		lane.Name = strings.TrimSpace(lane.Name)
		if lane.Name == "" {
			lane.Name = lane.Slug
		}
		if lane.Enabled {
			lane.EverEnabled = true
		}
		if builtin, ok := builtinLane(lane.Slug); ok {
			// The builtins are not deletable and not disable-able: a request
			// that falls back has to land somewhere.
			lane.Builtin = true
			lane.Enabled = true
			lane.EverEnabled = true
			// The agent lane was named 便宜 and then 优惠 before it was named
			// for what it is; a registry still carrying an old default name or
			// note takes the current one, while an operator's own name stays.
			if lane.Name == lane.Slug || (lane.Slug == LaneSlugAgent && (lane.Name == "便宜" || lane.Name == "优惠")) {
				lane.Name = builtin.Name
			}
			if lane.Slug == LaneSlugAgent && lane.Note == "价格更低的一批来源" {
				lane.Note = builtin.Note
			}
			if lane.PriceRatio <= 0 {
				lane.PriceRatio = builtin.PriceRatio
			}
		}
		if lane.PriceRatio < 0 || lane.PriceRatio > 1000 {
			return nil, fmt.Errorf("invalid lane price ratio: %s", lane.Slug)
		}
		seen[lane.Slug] = true
		out = append(out, lane)
	}
	for _, lane := range defaultChannelLanes {
		if !seen[lane.Slug] {
			out = append(out, lane)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

func builtinLane(slug string) (ChannelLane, bool) {
	for _, lane := range defaultChannelLanes {
		if lane.Slug == slug {
			return lane, true
		}
	}
	return ChannelLane{}, false
}

// AllChannelLanes returns every registered lane, enabled or not, in display order.
func AllChannelLanes() []ChannelLane {
	channelLanesMutex.RLock()
	defer channelLanesMutex.RUnlock()
	return cloneChannelLanes(channelLanes)
}

func LaneBySlug(slug string) (ChannelLane, bool) {
	slug = NormalizeLaneSlug(slug)
	channelLanesMutex.RLock()
	defer channelLanesMutex.RUnlock()
	for _, lane := range channelLanes {
		if lane.Slug == slug {
			return lane, true
		}
	}
	return ChannelLane{}, false
}

// IsLaneKnown reports whether the slug is registered at all. Pricing and billing
// use this: a sell price or discount written against a lane that was later
// disabled must still resolve and still be editable.
func IsLaneKnown(slug string) bool {
	_, ok := LaneBySlug(slug)
	return ok
}

// IsLaneEnabled reports whether new traffic may be routed through the lane.
// Routing, ability building and "which lane may this key ask for" use this.
func IsLaneEnabled(slug string) bool {
	lane, ok := LaneBySlug(slug)
	return ok && lane.Enabled
}

// EnabledLaneSlugs is the routing-visible set, in display order.
func EnabledLaneSlugs() []string {
	channelLanesMutex.RLock()
	defer channelLanesMutex.RUnlock()
	out := make([]string, 0, len(channelLanes))
	for _, lane := range channelLanes {
		if lane.Enabled {
			out = append(out, lane.Slug)
		}
	}
	return out
}

// KnownLaneSlugs includes disabled lanes — the console lists these so an
// operator can still see and edit what a stopped lane carries.
func KnownLaneSlugs() []string {
	channelLanesMutex.RLock()
	defer channelLanesMutex.RUnlock()
	out := make([]string, 0, len(channelLanes))
	for _, lane := range channelLanes {
		out = append(out, lane.Slug)
	}
	return out
}

// UpsertChannelLane creates or edits one lane. Slug is identity: it is taken
// from the payload on create and ignored on edit.
func UpsertChannelLane(lane ChannelLane) (ChannelLane, error) {
	lane.Slug = NormalizeLaneSlug(lane.Slug)
	if !laneSlugPattern.MatchString(lane.Slug) {
		return ChannelLane{}, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "slug"})
	}
	lane.Name = strings.TrimSpace(lane.Name)
	if lane.Name == "" {
		return ChannelLane{}, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "name"})
	}
	if lane.PriceRatio < 0 || lane.PriceRatio > 1000 {
		return ChannelLane{}, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "price_ratio"})
	}
	channelLanesMutex.Lock()
	defer channelLanesMutex.Unlock()
	for i, existing := range channelLanes {
		if existing.Slug != lane.Slug {
			continue
		}
		existing.Name = lane.Name
		existing.Note = lane.Note
		existing.Color = lane.Color
		existing.Sort = lane.Sort
		if lane.PriceRatio > 0 {
			existing.PriceRatio = lane.PriceRatio
		}
		// Enabling requires the dedicated endpoint and its source guard.
		channelLanes[i] = existing
		return existing, nil
	}
	if lane.Sort == 0 {
		lane.Sort = len(channelLanes)
	}
	if lane.PriceRatio <= 0 {
		lane.PriceRatio = 100
	}
	// A new lane starts stopped: nothing supplies it yet, so a discount typed
	// against it would have no traffic to apply to.
	lane.Enabled = false
	lane.EverEnabled = false
	lane.Builtin = false
	channelLanes = append(channelLanes, lane)
	return lane, nil
}

// SetChannelLaneEnabled turns a lane on or off. Turning one on is what makes it
// routable; turning it off leaves its prices and discounts in place.
func SetChannelLaneEnabled(slug string, enabled bool) (ChannelLane, error) {
	slug = NormalizeLaneSlug(slug)
	channelLanesMutex.Lock()
	defer channelLanesMutex.Unlock()
	for i, lane := range channelLanes {
		if lane.Slug != slug {
			continue
		}
		if lane.Builtin && !enabled {
			return ChannelLane{}, types.Err(types.ErrorCodeLaneBuiltin, types.D{"lane": slug})
		}
		lane.Enabled = enabled
		if enabled {
			lane.EverEnabled = true
		}
		channelLanes[i] = lane
		return lane, nil
	}
	return ChannelLane{}, types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": slug})
}

// DeleteChannelLane removes a lane that was never turned on. Anything that has
// carried traffic is refused — the caller shows "停用" instead.
func DeleteChannelLane(slug string) error {
	slug = NormalizeLaneSlug(slug)
	channelLanesMutex.Lock()
	defer channelLanesMutex.Unlock()
	for i, lane := range channelLanes {
		if lane.Slug != slug {
			continue
		}
		if lane.Builtin {
			return types.Err(types.ErrorCodeLaneBuiltin, types.D{"lane": slug})
		}
		if lane.EverEnabled {
			return types.Err(types.ErrorCodeLaneEverEnabled, types.D{"lane": slug})
		}
		channelLanes = append(channelLanes[:i], channelLanes[i+1:]...)
		return nil
	}
	return types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": slug})
}

// LanePriceRatio is the lane's default sell price as a percent of the official
// price; 100 when the lane is unknown or carries none.
func LanePriceRatio(slug string) float64 {
	lane, ok := LaneBySlug(slug)
	if !ok || lane.PriceRatio <= 0 {
		return 100
	}
	return lane.PriceRatio
}
