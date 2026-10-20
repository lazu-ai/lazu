package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay/channel/decision"
)

type PriorityRouter struct{}

const routingIntentMultiImage = "multi_image"

func (r *PriorityRouter) Name() string    { return "baseline" }
func (r *PriorityRouter) Version() string { return "v1" }

func (r *PriorityRouter) Select(ctx context.Context, req *RequestContext) (*Decision, error) {
	if req == nil {
		return nil, errors.New("routing request context is nil")
	}
	groups := req.CandidateGroups
	if len(groups) == 0 && req.DefaultGroup != "" {
		groups = []string{req.DefaultGroup}
	}
	var lastErr error
	lastGroup := req.DefaultGroup
	for _, group := range groups {
		if group == "" {
			continue
		}
		lastGroup = group
		preferredIntents := preferredChannelIntents(req.Features)
		channel, selectedGroup, err := selectProtocolCompatibleChannel(req.GinContext, group, req.ResolvedModel, nil, preferredIntents)
		if err != nil {
			lastErr = err
			continue
		}
		if channel == nil {
			continue
		}
		if selectedGroup == "" {
			selectedGroup = group
		}
		reason := "priority_weight"
		meta := map[string]any{
			"strategy_name":    r.Name(),
			"strategy_version": r.Version(),
			"router":           "priority",
			"group":            selectedGroup,
		}
		if preferredFor := matchingPreferredIntent(channel, preferredIntents); preferredFor != "" {
			reason = "capability_preference"
			meta["preferred_for"] = preferredFor
			if req.Features != nil {
				meta["vision_image_count"] = req.Features.VisionImageCount
			}
		}
		return &Decision{
			ChannelID:  channel.Id,
			Model:      req.ResolvedModel,
			Provider:   channel.GetTag(),
			Group:      selectedGroup,
			Reason:     reason,
			Score:      float64(channel.GetPriority()),
			Candidates: []Candidate{{ChannelID: channel.Id, Score: float64(channel.GetWeight())}},
			Meta:       meta,
			Channel:    channel,
		}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if lastGroup == "" {
		lastGroup = req.DefaultGroup
	}
	return nil, &NoChannelError{Group: lastGroup, Model: req.ResolvedModel}
}

// SelectProtocolCompatibleChannel filters all candidates with the same
// per-attempt planner used by the relay controller before applying the legacy
// priority+weight preference. This keeps incompatible dialects out of the
// random draw in the first place. Retries cannot be relied on to correct a bad
// draw: a request gets at most one channel switch, and the agent lane gets none.
func SelectProtocolCompatibleChannel(c *gin.Context, group, modelName string, excluded map[int]struct{}) (*model.Channel, string, error) {
	return selectProtocolCompatibleChannel(c, group, modelName, excluded, preferredChannelIntentsFromContext(c))
}

func preferredChannelIntentsFromContext(c *gin.Context) []string {
	if c == nil {
		return nil
	}
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil || parsed.Semantic == nil {
		return nil
	}
	ctx := context.Background()
	if c.Request != nil {
		ctx = c.Request.Context()
	}
	return preferredChannelIntents(ExtractSemanticFeatures(ctx, nil, parsed.Semantic))
}

func selectProtocolCompatibleChannel(c *gin.Context, group, modelName string, excluded map[int]struct{}, preferredIntents []string) (*model.Channel, string, error) {
	if c == nil {
		return model.CacheGetRandomSatisfiedChannel(nil, group, modelName, 0)
	}
	parsed, hasTextgen := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	isDecision := c.Request != nil && c.Request.URL.Path == "/v1/decisions"
	if (!hasTextgen || parsed == nil) && !isDecision {
		return model.CacheGetRandomSatisfiedChannel(c, group, modelName, 0)
	}
	candidates, selectedGroup, err := model.CacheGetRoutingCandidates(c, group, modelName)
	if err != nil {
		return nil, selectedGroup, err
	}
	nativeCompatible := make([]model.RoutingCandidate, 0, len(candidates))
	bridgedCompatible := make([]model.RoutingCandidate, 0, len(candidates))
	// a plan that applied a registered degradation is lossy: it is taken only
	// when no candidate carries the request losslessly, native or bridged
	degradedCompatible := make([]model.RoutingCandidate, 0, len(candidates))
	var lastPlanError error
	for _, candidate := range candidates {
		channel := candidate.Channel
		if channel == nil {
			continue
		}
		if _, skip := excluded[channel.Id]; skip {
			continue
		}
		if isDecision {
			settings := channel.GetSetting()
			dialect := settings.DecisionDialect
			compatibleModel := common.DecisionModelSupportsDialect(modelName, dialect)
			if settings.ValidateDecisionSettings() == nil && compatibleModel && decision.GetAdaptor(dialect) != nil {
				nativeCompatible = append(nativeCompatible, candidate)
			}
			continue
		}
		plan, planErr := textgenruntime.PlanForChannel(parsed, channel, modelName)
		if planErr != nil {
			lastPlanError = planErr
			continue
		}
		switch {
		case plan.Degraded():
			degradedCompatible = append(degradedCompatible, candidate)
		case plan.Native:
			nativeCompatible = append(nativeCompatible, candidate)
		default:
			bridgedCompatible = append(bridgedCompatible, candidate)
		}
	}
	compatible := nativeCompatible
	if len(compatible) == 0 {
		compatible = bridgedCompatible
	}
	if len(compatible) == 0 {
		compatible = degradedCompatible
	}
	if len(compatible) == 0 {
		if lastPlanError != nil {
			return nil, selectedGroup, fmt.Errorf("no protocol-compatible channel: %w", lastPlanError)
		}
		return nil, selectedGroup, nil
	}
	preferred := make([]model.RoutingCandidate, 0, len(compatible))
	for _, candidate := range compatible {
		if matchingPreferredIntent(candidate.Channel, preferredIntents) != "" {
			preferred = append(preferred, candidate)
		}
	}
	if len(preferred) > 0 {
		compatible = preferred
	}
	maxPriority := compatible[0].Priority
	for _, candidate := range compatible[1:] {
		if candidate.Priority > maxPriority {
			maxPriority = candidate.Priority
		}
	}
	tier := compatible[:0]
	for _, candidate := range compatible {
		if candidate.Priority == maxPriority {
			tier = append(tier, candidate)
		}
	}
	totalWeight := 0
	for _, candidate := range tier {
		totalWeight += int(candidate.Weight) + 10
	}
	pick := common.GetRandomInt(totalWeight)
	for _, candidate := range tier {
		pick -= int(candidate.Weight) + 10
		if pick < 0 {
			return candidate.Channel, selectedGroup, nil
		}
	}
	return tier[len(tier)-1].Channel, selectedGroup, nil
}

func preferredChannelIntents(features *Features) []string {
	if features == nil || features.VisionImageCount < 2 {
		return nil
	}
	return []string{routingIntentMultiImage}
}

func matchingPreferredIntent(channel *model.Channel, intents []string) string {
	if channel == nil || len(intents) == 0 {
		return ""
	}
	capabilities := channel.GetCapabilities()
	for _, intent := range intents {
		if capabilities.PrefersIntent(intent) {
			return intent
		}
	}
	return ""
}

type NoChannelError struct {
	Group string
	Model string
}

func (e *NoChannelError) Error() string {
	return "no routing channel available"
}
