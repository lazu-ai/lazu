package model

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"sort"
)

func channelAllowsClient(c *gin.Context, channel *Channel) bool {
	if channel == nil {
		return false
	}
	settings := channel.GetOtherSettings()
	if settings.CodexClientOnly {
		return c != nil && common.IsCodexResponsesRequest(c.Request)
	}
	if settings.ClaudeCodeClientOnly {
		return c != nil && common.IsClaudeCodeRequest(c.Request)
	}
	return true
}

// channelIsClientRestricted reports whether a source serves only one client.
func channelIsClientRestricted(channel *Channel) bool {
	if channel == nil {
		return false
	}
	settings := channel.GetOtherSettings()
	return settings.CodexClientOnly || settings.ClaudeCodeClientOnly
}

func filterClientCandidates(c *gin.Context, candidates []RoutingCandidate) []RoutingCandidate {
	result := make([]RoutingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if channelAllowsClient(c, candidate.Channel) {
			result = append(result, candidate)
		}
	}
	return result
}

func filterClientChannels(c *gin.Context, channels []*Channel) []*Channel {
	result := make([]*Channel, 0, len(channels))
	for _, channel := range channels {
		if channelAllowsClient(c, channel) {
			result = append(result, channel)
		}
	}
	return result
}

// Preserve legacy selection unless this pool contains a client-restricted source.
func getRandomSatisfiedChannelForClient(c *gin.Context, group, modelName string, retry int) (*Channel, error) {
	candidates, err := getRoutingCandidates(group, modelName)
	if err != nil {
		return nil, err
	}
	restricted := false
	for _, candidate := range candidates {
		if channelIsClientRestricted(candidate.Channel) {
			restricted = true
			break
		}
	}
	if !restricted {
		return getRandomSatisfiedChannel(group, modelName, retry)
	}
	candidates = filterClientCandidates(c, candidates)
	if len(candidates) == 0 {
		return nil, nil
	}
	priorities := []int64{}
	seen := map[int64]bool{}
	for _, candidate := range candidates {
		if !seen[candidate.Priority] {
			priorities = append(priorities, candidate.Priority)
			seen[candidate.Priority] = true
		}
	}
	sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
	if retry < 0 {
		retry = 0
	}
	if retry >= len(priorities) {
		retry = len(priorities) - 1
	}
	total := 0
	for _, candidate := range candidates {
		if candidate.Priority == priorities[retry] {
			total += int(candidate.Weight) + 10
		}
	}
	pick := common.GetRandomInt(total)
	for _, candidate := range candidates {
		if candidate.Priority == priorities[retry] {
			pick -= int(candidate.Weight) + 10
			if pick < 0 {
				return candidate.Channel, nil
			}
		}
	}
	return nil, nil
}

// CacheGetCatalogChannels projects configured capabilities, not the GET caller's
// runtime protocol. Request routing must use the client-filtered APIs instead.
func CacheGetCatalogChannels(group, modelName string) []*Channel {
	return getAllSatisfiedChannels(group, modelName)
}
