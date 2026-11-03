package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
)

// Session affinity keeps every turn of one agent session on the channel and
// upstream key that served its previous turn.
//
// Spreading a session across channels was harmless while every turn was
// stateless. It no longer is: a reasoning block's signature is bound to the
// upstream account that produced it, and another account drops or refuses it;
// prompt caches live per account; server-side conversation state lives with
// the account that holds it. Two channels serving the same model are two
// accounts, so drifting between them silently loses reasoning, pays for the
// cache again, or fails the turn.
//
// The record expires after an hour without a successful turn, matching the
// longest prompt-cache lifetime upstreams offer, and every hit renews it.

const (
	// DecisionReasonSessionAffinity is the decision reason (and strategy) of a
	// request routed to its session's previous channel.
	DecisionReasonSessionAffinity = "session_affinity"

	SessionAffinityTTL = time.Hour

	sessionAffinityKeyPrefix = "lazu:session_affinity:v1:"
	sessionAffinityTimeout   = 100 * time.Millisecond
	sessionAffinityMemoryCap = 50000
)

// Why a stored record was not used. They are written to the request log's
// admin details, so they are stable identifiers.
const (
	AffinitySkipChannelUnavailable  = "channel_unavailable"
	AffinitySkipLaneChanged         = "lane_changed"
	AffinitySkipPreferredLane       = "preferred_lane_available"
	AffinitySkipRetreating          = "route_retreating"
	AffinitySkipProtocolUnsupported = "protocol_incompatible"
	AffinitySkipProtocolDegraded    = "protocol_degraded"
	AffinitySkipLookupFailed        = "lookup_failed"
)

// AffinityRecord is the channel and upstream key a session's last successful
// turn used. KeyHash identifies the key itself, so an index that now points at
// a different credential (the key list was edited) is not mistaken for it.
type AffinityRecord struct {
	ChannelID int    `json:"c"`
	Group     string `json:"g"`
	KeyIndex  int    `json:"k"`
	KeyHash   string `json:"h,omitempty"`
}

// AffinityKeyHash fingerprints an upstream key without storing it.
func AffinityKeyHash(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// SessionAffinityKey scopes a session to its user and model. Session ids are
// supplied by the caller, so without the user one tenant could steer
// another's routing; without the model, an agent that sends its main model
// and a small side model in the same session would bounce between two
// channel pools on every turn.
func SessionAffinityKey(userID int, sessionID string, modelName string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + modelName))
	return fmt.Sprintf("%s%d:%s", sessionAffinityKeyPrefix, userID, hex.EncodeToString(sum[:16]))
}

// AffinityStore holds session records. Get renews a record it finds.
type AffinityStore interface {
	Get(ctx context.Context, key string, ttl time.Duration) (AffinityRecord, bool, error)
	Set(ctx context.Context, key string, record AffinityRecord, ttl time.Duration) error
}

// affinityStoreOverride replaces the store in tests.
var (
	affinityStoreOverride AffinityStore
	affinityMemory        = NewMemoryAffinityStore()
)

// SetAffinityStoreForTest swaps the store and returns a restore function.
func SetAffinityStoreForTest(store AffinityStore) func() {
	previous := affinityStoreOverride
	affinityStoreOverride = store
	return func() { affinityStoreOverride = previous }
}

// CurrentAffinityStore is Redis when the gateway has it, so every instance
// sees the same record, and process memory otherwise.
func CurrentAffinityStore() AffinityStore {
	if affinityStoreOverride != nil {
		return affinityStoreOverride
	}
	if common.RedisEnabled && common.RDB != nil {
		return redisAffinityStore{client: common.RDB}
	}
	return affinityMemory
}

// LookupSessionAffinity reads a session's record with a short deadline. A
// slow or failing store is a miss: affinity must never fail a request.
func LookupSessionAffinity(ctx context.Context, key string) (AffinityRecord, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, sessionAffinityTimeout)
	defer cancel()
	return CurrentAffinityStore().Get(ctx, key, SessionAffinityTTL)
}

// StoreSessionAffinity writes a session's record. The request's own context
// may already be done once its response has been sent, so the write runs on a
// fresh one.
func StoreSessionAffinity(key string, record AffinityRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), sessionAffinityTimeout)
	defer cancel()
	return CurrentAffinityStore().Set(ctx, key, record, SessionAffinityTTL)
}

type redisAffinityStore struct{ client *redis.Client }

func (s redisAffinityStore) Get(ctx context.Context, key string, ttl time.Duration) (AffinityRecord, bool, error) {
	// GET and EXPIRE in one round trip. GETEX would do the same but needs
	// Redis 6.2, which self-hosted deployments do not all run.
	var get *redis.StringCmd
	_, err := s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		get = pipe.Get(ctx, key)
		pipe.Expire(ctx, key, ttl)
		return nil
	})
	if errors.Is(err, redis.Nil) || (get != nil && errors.Is(get.Err(), redis.Nil)) {
		return AffinityRecord{}, false, nil
	}
	if err != nil {
		return AffinityRecord{}, false, err
	}
	var record AffinityRecord
	if err := json.Unmarshal([]byte(get.Val()), &record); err != nil || record.ChannelID <= 0 {
		return AffinityRecord{}, false, nil
	}
	return record, true, nil
}

func (s redisAffinityStore) Set(ctx context.Context, key string, record AffinityRecord, ttl time.Duration) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key, raw, ttl).Err()
}

// MemoryAffinityStore is the single-instance store used without Redis.
type MemoryAffinityStore struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]memoryAffinityEntry
}

type memoryAffinityEntry struct {
	record    AffinityRecord
	expiresAt time.Time
}

func NewMemoryAffinityStore() *MemoryAffinityStore {
	return &MemoryAffinityStore{now: time.Now, entries: map[string]memoryAffinityEntry{}}
}

func (s *MemoryAffinityStore) Get(_ context.Context, key string, ttl time.Duration) (AffinityRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	now := s.now()
	if !ok || !now.Before(entry.expiresAt) {
		delete(s.entries, key)
		return AffinityRecord{}, false, nil
	}
	entry.expiresAt = now.Add(ttl)
	s.entries[key] = entry
	return entry.record, true, nil
}

func (s *MemoryAffinityStore) Set(_ context.Context, key string, record AffinityRecord, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.entries[key]; !exists && len(s.entries) >= sessionAffinityMemoryCap {
		// Bounded like the other per-session caches: dropping every record
		// costs each live session one unpinned turn, never a failure.
		clear(s.entries)
	}
	s.entries[key] = memoryAffinityEntry{record: record, expiresAt: s.now().Add(ttl)}
	return nil
}

// SetClockForTest replaces the store's clock.
func (s *MemoryAffinityStore) SetClockForTest(now func() time.Time) { s.now = now }

// SelectByAffinity checks that a session's previous channel may still serve
// this request and returns the decision to use it, or why it may not.
//
// The record is honoured only on the terms the router itself would accept the
// channel: it must still be an enabled candidate for this model in a lane the
// request may use, the breaker must not have taken its route out, and the
// request must plan onto it without loss. Within those terms affinity outranks
// priority and weight, which only choose among equals; it does not outrank
// the caller's lane, so a session that fell back to a later lane returns to
// its preferred lane as soon as that lane has candidates again.
func SelectByAffinity(req *RequestContext, record AffinityRecord) (*Decision, string) {
	if req == nil || record.ChannelID <= 0 {
		return nil, AffinitySkipChannelUnavailable
	}
	groups := req.CandidateGroups
	if len(groups) == 0 && req.DefaultGroup != "" {
		groups = []string{req.DefaultGroup}
	}
	laneIndex := slices.Index(groups, record.Group)
	if laneIndex < 0 {
		return nil, AffinitySkipLaneChanged
	}
	for _, earlier := range groups[:laneIndex] {
		candidates, _, err := model.CacheGetRoutingCandidates(req.GinContext, earlier, req.ResolvedModel)
		if err != nil {
			return nil, AffinitySkipLookupFailed
		}
		if len(candidates) > 0 {
			return nil, AffinitySkipPreferredLane
		}
	}
	candidates, _, err := model.CacheGetRoutingCandidates(req.GinContext, record.Group, req.ResolvedModel)
	if err != nil {
		return nil, AffinitySkipLookupFailed
	}
	index := slices.IndexFunc(candidates, func(candidate model.RoutingCandidate) bool {
		return candidate.Channel != nil && candidate.Channel.Id == record.ChannelID
	})
	if index < 0 {
		return nil, AffinitySkipChannelUnavailable
	}
	candidate := candidates[index]
	routeModel := candidate.Model
	if routeModel == "" {
		routeModel = req.ResolvedModel
	}
	// The breaker fails open when every route of a pool is in retreat; so does
	// this check, by handing the request back to the router, which then draws
	// from the whole pool.
	if model.RouteIsRetreating(routeModel, record.Group, candidate.Channel.Id, time.Now().UTC()) {
		return nil, AffinitySkipRetreating
	}
	if reason := affinityPlanRejection(req, candidate.Channel); reason != "" {
		return nil, reason
	}
	return &Decision{
		ChannelID:  candidate.Channel.Id,
		Model:      req.ResolvedModel,
		Provider:   candidate.Channel.GetTag(),
		Group:      record.Group,
		Reason:     DecisionReasonSessionAffinity,
		Score:      float64(candidate.Priority),
		Candidates: []Candidate{{ChannelID: candidate.Channel.Id, Score: float64(candidate.Weight)}},
		Meta: map[string]any{
			"strategy_name":       DecisionReasonSessionAffinity,
			"strategy_version":    "v1",
			"group":               record.Group,
			"affinity_channel_id": record.ChannelID,
		},
		Channel: candidate.Channel,
	}, ""
}

// affinityPlanRejection applies the per-attempt planner the router filters
// with. A degraded plan is refused here because the router would prefer any
// lossless candidate over it.
func affinityPlanRejection(req *RequestContext, channel *model.Channel) string {
	if req.GinContext == nil {
		return ""
	}
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](req.GinContext, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil {
		return ""
	}
	plan, err := textgenruntime.PlanForChannel(parsed, channel, req.ResolvedModel)
	if err != nil {
		return AffinitySkipProtocolUnsupported
	}
	if plan.Degraded() {
		return AffinitySkipProtocolDegraded
	}
	return ""
}
