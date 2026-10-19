package model

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
)

// AbilityAuditResult reports what one consistency pass found and repaired.
type AbilityAuditResult struct {
	Channels  int
	Rebuilt   int
	RebuiltID []int
}

// AuditAbilities compares the ability table with every channel's own model
// list and rebuilds the channels that drifted apart.
//
// The ability table is a derived copy of channels.models × channels.group.
// It is rebuilt on every channel edit, but writes that bypass the channel
// model (migrations, hand edits, older code paths) leave rows behind for
// models a channel no longer serves. Those rows are invisible to the router,
// yet anything that reads the table raw — the health grid, the probe
// scheduler — treats them as live routes. Rebuilding through UpdateAbilities
// keeps operator-set priority and weight on the routes that still exist.
func AuditAbilities() (AbilityAuditResult, error) {
	result := AbilityAuditResult{}
	if CoreDB() == nil {
		return result, nil
	}
	var channels []*Channel
	if err := CoreDB().Model(&Channel{}).Find(&channels).Error; err != nil {
		return result, err
	}
	var rows []Ability
	if err := CoreDB().Find(&rows).Error; err != nil {
		return result, err
	}
	existing := make(map[int]map[string]struct{}, len(channels))
	for _, row := range rows {
		set, ok := existing[row.ChannelId]
		if !ok {
			set = map[string]struct{}{}
			existing[row.ChannelId] = set
		}
		set[row.Group+"|"+row.Model] = struct{}{}
	}
	result.Channels = len(channels)
	for _, channel := range channels {
		expected := expectedAbilityKeys(channel)
		if abilityKeySetsEqual(expected, existing[channel.Id]) {
			continue
		}
		if err := channel.UpdateAbilities(nil); err != nil {
			common.SysLog(fmt.Sprintf("ability audit: rebuild channel %d failed: %v", channel.Id, err))
			continue
		}
		result.Rebuilt++
		result.RebuiltID = append(result.RebuiltID, channel.Id)
	}
	return result, nil
}

// expectedAbilityKeys mirrors the row set UpdateAbilities would write.
func expectedAbilityKeys(channel *Channel) map[string]struct{} {
	keys := map[string]struct{}{}
	for _, model := range strings.Split(channel.Models, ",") {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		for _, group := range strings.Split(channel.Group, ",") {
			group = NormalizeChannelLane(group)
			if !IsValidChannelLane(group) {
				continue
			}
			keys[group+"|"+model] = struct{}{}
		}
	}
	return keys
}

func abilityKeySetsEqual(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return false
		}
	}
	return true
}
