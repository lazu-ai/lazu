package model

import "strings"

// ChannelLaneHasReferences says whether anything still points at a lane. A
// never-enabled lane can already be provisioned, so references are kept until
// operators explicitly remove them, historical price records included.
func ChannelLaneHasReferences(lane string) (bool, error) {
	count, err := CountChannelsInLane(lane)
	if err != nil || count > 0 {
		return count > 0, err
	}
	if err := CoreDB().Model(&ModelSellPrice{}).Where("channel_group = ?", lane).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountChannelsInLane backs the lane panel's "在这档供货的来源" and the guard on
// stopping one.
func CountChannelsInLane(lane string) (int64, error) {
	if CoreDB() == nil {
		return 0, nil
	}
	lane = NormalizeChannelLane(lane)
	var channels []Channel
	if err := CoreDB().Select("id", "group").Find(&channels).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, channel := range channels {
		// Read the raw assignment rather than GetGroups: this count is the
		// precondition for *enabling* a lane, so it has to see the sources
		// sitting in it while it is still stopped.
		for _, group := range strings.Split(strings.Trim(channel.Group, ","), ",") {
			if NormalizeChannelLane(group) == lane {
				count++
				break
			}
		}
	}
	return count, nil
}

// CountPricedModelsInLane is what the lane panel reports as "这档现在的样子".
func CountPricedModelsInLane(lane string) (int64, error) {
	if CoreDB() == nil {
		return 0, nil
	}
	lane = NormalizeChannelLane(lane)
	var count int64
	err := CoreDB().Model(&ModelSellPrice{}).
		Where("channel_group = ? AND active = ?", lane, true).
		Distinct("model_name").Count(&count).Error
	return count, err
}
