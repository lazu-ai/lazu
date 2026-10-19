package model

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type cachedAbility struct {
	ChannelID int
	Priority  int64
	Weight    uint
	// Model and Lane are carried so a cached candidate can be checked against
	// the breaker, which records verdicts per route rather than per channel.
	Model string
	Lane  string
}

// RoutingCandidate preserves the effective per-model routing policy from the
// ability row. Callers that filter candidates must rank with these values,
// rather than falling back to the channel-wide defaults.
type RoutingCandidate struct {
	Channel  *Channel
	Priority int64
	Weight   uint
	Group    string
	// Model is the ability's own model name, which is not always the name the
	// caller asked for: a request can be resolved through a normalized alias.
	// The breaker is keyed per route, so it needs the route's name, not the
	// request's.
	Model string
}

var group2model2channels map[string]map[string][]cachedAbility // enabled abilities
var channelsIDM map[int]*Channel                               // all channels include disabled
var channelSyncLock sync.RWMutex

func InitChannelCache() { logControlRefresh("channel routing", initChannelCacheFromCore()) }
func initChannelCacheFromCore() error {
	if !common.MemoryCacheEnabled && (CoreDB() == nil || CoreDB().Dialector.Name() != "postgres") {
		return nil
	}
	revision, err := beginControlSnapshot(context.Background(), channelRoutingSnapshotKey)
	if err != nil {
		return err
	}
	var channels []*Channel
	if err := CoreDB().Find(&channels).Error; err != nil {
		return fmt.Errorf("load channel cache: %w", err)
	}
	var abilities []*Ability
	if err := CoreDB().Find(&abilities).Error; err != nil {
		return fmt.Errorf("load ability cache: %w", err)
	}
	if CoreDB().Dialector.Name() == "postgres" {
		if err := publishChannelRoutingSnapshot(channels, abilities, revision); err != nil {
			return err
		}
	}
	if err := decryptChannelKeysForUse(channels); err != nil {
		return fmt.Errorf("decrypt channel cache: %w", err)
	}
	installChannelRoutingCache(channels, abilities)
	return refreshRouteHealthCacheFromCore()
}

func installChannelRoutingCache(channels []*Channel, abilities []*Ability) {
	newChannelId2channel := make(map[int]*Channel)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
	}
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]cachedAbility)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]cachedAbility)
	}
	for _, ability := range abilities {
		channel, ok := newChannelId2channel[ability.ChannelId]
		if !ok || !ability.Enabled || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if NormalizeAbilityRole(ability.Role) == AbilityRoleInventory {
			// Imported but not in service. It is still probed by the health
			// worker; it just never receives a user's request.
			continue
		}
		group := strings.TrimSpace(ability.Group)
		modelName := strings.TrimSpace(ability.Model)
		if group == "" || modelName == "" {
			continue
		}
		if _, ok := newGroup2model2channels[group]; !ok {
			newGroup2model2channels[group] = make(map[string][]cachedAbility)
		}
		priority := channel.GetPriority()
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		// Baking the role into the cached priority is what makes failover free:
		// the selector already walks distinct priorities one tier per retry, so
		// a backup is simply the tier after every primary.
		priority = EffectiveRoutingPriority(priority, ability.Role)
		newGroup2model2channels[group][modelName] = append(
			newGroup2model2channels[group][modelName],
			cachedAbility{
				ChannelID: channel.Id,
				Priority:  priority,
				Weight:    ability.Weight,
				Model:     modelName,
				Lane:      group,
			},
		)
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, abilities := range model2channels {
			sort.Slice(abilities, func(i, j int) bool {
				return abilities[i].Priority > abilities[j].Priority
			})
			newGroup2model2channels[group][model] = abilities
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	// Routing policy and breaker verdicts are read together on every request,
	// so they are refreshed together and can never be more than one sync apart.
	channelOfferings = make(map[string]map[string]laneOffering)
	for _, ability := range abilities {
		if channelOfferings[ability.Group] == nil {
			channelOfferings[ability.Group] = map[string]laneOffering{}
		}
		offering := channelOfferings[ability.Group][ability.Model]
		offering.Configured++
		if ability.Enabled {
			offering.Enabled++
		}
		channelOfferings[ability.Group][ability.Model] = offering
	}
	channelOfferingsReady = true
	channelSyncLock.Unlock()
	common.SysDebug("channel routing cache installed")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		if apiControlPlaneReader() {
			logControlRefresh("channel routing", refreshChannelRoutingFromRedis())
		} else {
			InitChannelCache()
		}
	}
}

func CacheGetRandomSatisfiedChannel(c *gin.Context, group string, model string, retry int) (*Channel, string, error) {
	channel, err := getRandomSatisfiedChannelForClient(c, group, model, retry)
	if err != nil {
		return nil, group, err
	}
	return channel, group, nil
}

func getRandomSatisfiedChannel(group string, model string, retry int) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetRandomSatisfiedChannel(group, model, retry)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	abilities := group2model2channels[group][model]

	// If no channels found, try to find channels with the normalized model name.
	if len(abilities) == 0 {
		normalizedModel := NormalizePricingModelName(model)
		abilities = group2model2channels[group][normalizedModel]
	}

	if len(abilities) == 0 {
		return nil, nil
	}
	abilities = dropRetreatingAbilities(abilities)

	if len(abilities) == 1 {
		if channel, ok := channelsIDM[abilities[0].ChannelID]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("ability points at missing channel %d; repair abilities", abilities[0].ChannelID)
	}

	uniquePriorities := make(map[int]bool)
	for _, ability := range abilities {
		if _, ok := channelsIDM[ability.ChannelID]; ok {
			uniquePriorities[int(ability.Priority)] = true
		} else {
			return nil, fmt.Errorf("ability points at missing channel %d; repair abilities", ability.ChannelID)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var targetAbilities []cachedAbility
	for _, ability := range abilities {
		if _, ok := channelsIDM[ability.ChannelID]; ok {
			if ability.Priority == targetPriority {
				targetAbilities = append(targetAbilities, ability)
			}
		} else {
			return nil, fmt.Errorf("ability points at missing channel %d; repair abilities", ability.ChannelID)
		}
	}

	// 平滑系数
	smoothingFactor := 10
	// Calculate the total weight of all channels up to endIdx
	totalWeight := 0
	for _, ability := range targetAbilities {
		totalWeight += int(ability.Weight) + smoothingFactor
	}
	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, ability := range targetAbilities {
		randomWeight -= int(ability.Weight) + smoothingFactor
		if randomWeight < 0 {
			return channelsIDM[ability.ChannelID], nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

// dropRetreatingAbilities removes routes the breaker has taken out of
// rotation, and stops doing so as soon as that would empty the pool.
//
// It runs before priority tiers are computed, so a retreating primary does not
// merely lose the draw — it stops defining a tier, and the backup below it
// becomes the pool the very first attempt uses instead of the second.
func dropRetreatingAbilities(abilities []cachedAbility) []cachedAbility {
	if len(abilities) <= 1 {
		return abilities
	}
	now := time.Now().UTC()
	kept := make([]cachedAbility, 0, len(abilities))
	for _, ability := range abilities {
		if RouteIsRetreating(ability.Model, ability.Lane, ability.ChannelID, now) {
			continue
		}
		kept = append(kept, ability)
	}
	if len(kept) == 0 {
		// Everything is in retreat. Serving the request badly beats refusing
		// it outright, and the attempt is what lets a route recover.
		return abilities
	}
	return kept
}

// CacheGetAllSatisfiedChannels returns every enabled channel that serves
// (group, model) — across all priority tiers — so callers can do their
// own filtering and ranking instead of accepting the priority+weight
// random pick that getRandomSatisfiedChannel does.
//
// Used by the composite router; CacheGetRandomSatisfiedChannel remains
// the entry point for legacy / baseline routing.
func CacheGetAllSatisfiedChannels(c *gin.Context, group string, modelName string) ([]*Channel, string, error) {
	return filterClientChannels(c, getAllSatisfiedChannels(group, modelName)), group, nil
}

// CacheGetRoutingCandidates returns every enabled candidate together with the
// effective priority and weight for this exact (group, model) ability, tagged
// with the group it came from.
func CacheGetRoutingCandidates(c *gin.Context, group string, modelName string) ([]RoutingCandidate, string, error) {
	candidates, err := getRoutingCandidates(group, modelName)
	for index := range candidates {
		candidates[index].Group = group
	}
	return filterClientCandidates(c, candidates), group, err
}

func getRoutingCandidates(group string, modelName string) ([]RoutingCandidate, error) {
	if !common.MemoryCacheEnabled {
		return getRoutingCandidatesFromDB(group, modelName)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	abilities := group2model2channels[group][modelName]
	if len(abilities) == 0 {
		abilities = group2model2channels[group][NormalizePricingModelName(modelName)]
	}
	result := make([]RoutingCandidate, 0, len(abilities))
	for _, ability := range abilities {
		channel, ok := channelsIDM[ability.ChannelID]
		if !ok || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		result = append(result, RoutingCandidate{Channel: channel, Priority: ability.Priority, Weight: ability.Weight})
	}
	return result, nil
}

// CacheGetAllChannelsInGroup returns every enabled channel in the given
// group, *regardless of which models it serves*. The distributor uses it to
// resolve accepted auto-model aliases before baseline priority routing.
func CacheGetAllChannelsInGroup(c *gin.Context, group string) ([]*Channel, string, error) {
	return filterClientChannels(c, getAllChannelsInGroup(group)), group, nil
}

// getAllChannelsInGroup returns every distinct channel in the given
// group across every model it serves. Order is unspecified.
func getAllChannelsInGroup(group string) []*Channel {
	if !common.MemoryCacheEnabled {
		// CoreDB()-backed: ask Ability for all enabled rows in this group,
		// dedupe by channel id, fetch the Channel rows.
		channels, err := getAllChannelsInGroupFromDB(group)
		if err != nil {
			return nil
		}
		return channels
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	modelMap, ok := group2model2channels[group]
	if !ok {
		return nil
	}
	seen := make(map[int]struct{})
	out := make([]*Channel, 0)
	for _, abilities := range modelMap {
		for _, ability := range abilities {
			if _, dup := seen[ability.ChannelID]; dup {
				continue
			}
			seen[ability.ChannelID] = struct{}{}
			if ch, ok := channelsIDM[ability.ChannelID]; ok {
				out = append(out, ch)
			}
		}
	}
	return out
}

// getAllSatisfiedChannels reads the channel cache (or CoreDB() when cache is
// disabled) and returns every channel registered for (group, model).
// Order is unspecified — callers must sort if they care.
func getAllSatisfiedChannels(group string, modelName string) []*Channel {
	if !common.MemoryCacheEnabled {
		channels, err := getAllSatisfiedChannelsFromDB(group, modelName)
		if err != nil {
			return nil
		}
		return channels
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	abilities := group2model2channels[group][modelName]
	if len(abilities) == 0 {
		normalized := NormalizePricingModelName(modelName)
		abilities = group2model2channels[group][normalized]
	}
	if len(abilities) == 0 {
		return nil
	}
	out := make([]*Channel, 0, len(abilities))
	for _, ability := range abilities {
		if ch, ok := channelsIDM[ability.ChannelID]; ok {
			out = append(out, ch)
		}
	}
	return out
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, types.Err(types.ErrorCodeChannelNotFound, types.D{"channel_id": id})
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, types.Err(types.ErrorCodeChannelNotFound, types.D{"channel_id": id})
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, abilities := range model2channels {
				for i, ability := range abilities {
					if ability.ChannelID == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(abilities[:i], abilities[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel == nil {
		return
	}
	channelsIDM[channel.Id] = channel
}

// LaneServiceability answers the two questions a lane picker has to ask
// before it offers a lane: can this lane serve the model at all, and will it
// serve the caller's client. It is derived from the live channel cache.
type LaneServiceability struct {
	Available bool
	// ClientOnly names the client every channel in this lane insists on
	// ("codex"), and is empty when at least one channel takes any client. A
	// lane restricted this way still routes — just not for other tools — so
	// the connect dialog shows its price and refuses the switch instead of
	// pretending the lane does not exist.
	ClientOnly string
}

// GetLaneServiceability maps model name -> lane -> serviceability across the
// published lanes. One pass over the cached abilities; callers hold it for the
// length of a request, never longer.
func GetLaneServiceability() map[string]map[string]LaneServiceability {
	result := make(map[string]map[string]LaneServiceability)
	for _, group := range []string{ChannelLaneDirect, ChannelLaneAgent} {
		for _, modelName := range laneModelNames(group) {
			channels := getAllSatisfiedChannels(group, modelName)
			if len(channels) == 0 {
				continue
			}
			// The lane is client-locked only when every channel insists on the
			// same client; one open channel, or two different clients, is not.
			clientOnly, seen := "", false
			for _, channel := range channels {
				if channel == nil {
					continue
				}
				client := channelClientOnly(channel)
				if !seen {
					clientOnly, seen = client, true
				}
				if client == "" || client != clientOnly {
					clientOnly = ""
					break
				}
			}
			if result[modelName] == nil {
				result[modelName] = make(map[string]LaneServiceability, 2)
			}
			entry := LaneServiceability{Available: true, ClientOnly: clientOnly}
			result[modelName][group] = entry
		}
	}
	return result
}

// laneModelNames lists the models a lane serves, from the same place the
// selector reads: the cached abilities when the cache is on, the abilities
// table when it is not. Reading the table while the cache is authoritative
// would report a lane the router cannot actually take.
func laneModelNames(group string) []string {
	if !common.MemoryCacheEnabled {
		return GetGroupEnabledModels(group)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	names := make([]string, 0, len(group2model2channels[group]))
	for modelName := range group2model2channels[group] {
		names = append(names, modelName)
	}
	return names
}

// The client restrictions a source can carry.
const (
	ClientOnlyCodex      = "codex"
	ClientOnlyClaudeCode = "claude_code"
)

// channelClientOnly names the single client a source admits, or "".
func channelClientOnly(channel *Channel) string {
	if channel == nil {
		return ""
	}
	settings := channel.GetOtherSettings()
	switch {
	case settings.CodexClientOnly:
		return ClientOnlyCodex
	case settings.ClaudeCodeClientOnly:
		return ClientOnlyClaudeCode
	}
	return ""
}

type laneOffering struct {
	Configured int64
	Enabled    int64
}

var channelOfferings map[string]map[string]laneOffering
var channelOfferingsReady bool

func CachedLaneOffering(modelName, lane string) (configured, enabled int64, ready bool) {
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	entry := channelOfferings[lane][modelName]
	return entry.Configured, entry.Enabled, channelOfferingsReady
}
