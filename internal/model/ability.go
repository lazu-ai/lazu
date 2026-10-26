package model

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool   `json:"enabled"`
	Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint   `json:"weight" gorm:"default:0;index"`
	// Role says what this upstream is FOR on this route, which is a different
	// question from how much traffic it should get. Weight splits load between
	// sources we are actively using; Role decides whether a source is one of
	// those at all.
	Role string `json:"role" gorm:"type:varchar(16);not null;default:'primary';index"`
}

// The three things an upstream can be on one route.
const (
	// AbilityRolePrimary carries traffic now, sharing it with the other
	// primaries by weight and priority.
	AbilityRolePrimary = "primary"
	// AbilityRoleBackup carries nothing while any primary is healthy, and
	// takes over the moment none is. It is ranked below every primary rather
	// than being held outside routing, so failover costs no new machinery:
	// the router already walks priority tiers.
	AbilityRoleBackup = "backup"
	// AbilityRoleInventory is imported capacity we are not serving from. It is
	// still probed, so that the day we promote it we already know whether it
	// works — an unprobed spare is not a spare.
	AbilityRoleInventory = "inventory"

	// backupPriorityFloor sinks every backup below every primary. Operator
	// priorities are small numbers; subtracting a value no hand-set priority
	// will ever reach keeps the relative order inside each role intact.
	backupPriorityFloor = int64(1_000_000)
)

// NormalizeAbilityRole maps stored and user-supplied values onto the three
// known roles. Anything unrecognised is primary, because the safe reading of a
// corrupt or older row is "this route is in service" — the state it had before
// roles existed.
func NormalizeAbilityRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case AbilityRoleBackup:
		return AbilityRoleBackup
	case AbilityRoleInventory:
		return AbilityRoleInventory
	default:
		return AbilityRolePrimary
	}
}

// IsValidAbilityRole reports whether a caller-supplied role is one we accept.
func IsValidAbilityRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case AbilityRolePrimary, AbilityRoleBackup, AbilityRoleInventory:
		return true
	default:
		return false
	}
}

// EffectiveRoutingPriority folds a route's role into the number the router
// ranks by, so failover needs no separate code path in channel selection.
func EffectiveRoutingPriority(priority int64, role string) int64 {
	if NormalizeAbilityRole(role) == AbilityRoleBackup {
		return priority - backupPriorityFloor
	}
	return priority
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func abilityGroupCol() string {
	if commonGroupCol != "" {
		return commonGroupCol
	}
	if common.UsingPostgreSQL {
		return `"group"`
	}
	return "`group`"
}

// GetAllEnableAbilityWithChannels backs the model plaza and the pricing map.
//
// Parked inventory is excluded here for the same reason it is excluded from
// routing: offering a model whose only supply we have deliberately taken out
// of rotation would put it in the catalogue and then refuse every request for
// it. What we list and what we will serve have to be the same set.
func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := CoreDB().Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ? AND abilities.role <> ?", true, AbilityRoleInventory).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	CoreDB().Table("abilities").
		Where(abilityGroupCol()+" = ? and enabled = ? and role <> ?", group, true, AbilityRoleInventory).
		Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	CoreDB().Table("abilities").
		Where("enabled = ? and role <> ?", true, AbilityRoleInventory).
		Distinct("model").Pluck("model", &models)
	return models
}

// abilityEffectivePriorityExpr is EffectiveRoutingPriority expressed in SQL.
// The database path has to rank routes exactly as the in-memory cache does, or
// a deployment with the cache turned off would quietly route to backups.
func abilityEffectivePriorityExpr() string {
	return fmt.Sprintf("(COALESCE(priority, 0) - CASE WHEN role = '%s' THEN %d ELSE 0 END)",
		AbilityRoleBackup, backupPriorityFloor)
}

func getPriority(group string, model string, retry int) (int, error) {

	var priorities []int
	err := CoreDB().Model(&Ability{}).
		Select("DISTINCT "+abilityEffectivePriorityExpr()+" AS effective_priority").
		Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ?", group, model, true, AbilityRoleInventory).
		Order("effective_priority DESC").
		Pluck("effective_priority", &priorities).Error

	if err != nil {
		// 处理错误
		return 0, err
	}

	if len(priorities) == 0 {
		// 如果没有查询到优先级，则返回错误
		return 0, fmt.Errorf("no enabled ability priority for group %s and model %s", group, model)
	}

	// 确定要使用的优先级
	var priorityToUse int
	if retry >= len(priorities) {
		// 如果重试次数大于优先级数，则使用最小的优先级
		priorityToUse = priorities[len(priorities)-1]
	} else {
		priorityToUse = priorities[retry]
	}
	return priorityToUse, nil
}

func getChannelQuery(group string, model string, retry int) (*gorm.DB, error) {
	effective := abilityEffectivePriorityExpr()
	maxPrioritySubQuery := CoreDB().Model(&Ability{}).Select("MAX"+effective).
		Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ?", group, model, true, AbilityRoleInventory)
	channelQuery := CoreDB().Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ? and "+effective+" = (?)",
		group, model, true, AbilityRoleInventory, maxPrioritySubQuery)
	if retry != 0 {
		priority, err := getPriority(group, model, retry)
		if err != nil {
			return nil, err
		} else {
			channelQuery = CoreDB().Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ? and "+effective+" = ?",
				group, model, true, AbilityRoleInventory, priority)
		}
	}

	return channelQuery, nil
}

// getAllChannelsInGroupFromDB returns every enabled channel that has any
// model exposure inside the given group. CoreDB()-side counterpart of
// CacheGetAllChannelsInGroup.getAllChannelsInGroup — only used when the
// in-memory channel cache is disabled.
func getAllChannelsInGroupFromDB(group string) ([]*Channel, error) {
	var abilities []Ability
	if err := CoreDB().Where(abilityGroupCol()+" = ? and enabled = ? and role <> ?", group, true, AbilityRoleInventory).Find(&abilities).Error; err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return nil, nil
	}
	seen := make(map[int]struct{}, len(abilities))
	channelIDs := make([]int, 0, len(abilities))
	for _, ab := range abilities {
		if _, ok := seen[ab.ChannelId]; ok {
			continue
		}
		seen[ab.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ab.ChannelId)
	}
	var channels []*Channel
	if err := CoreDB().Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return nil, err
	}
	if err := decryptChannelKeysForUse(channels); err != nil {
		return nil, err
	}
	return channels, nil
}

// getAllSatisfiedChannelsFromDB is the CoreDB()-backed counterpart of
// CacheGetAllSatisfiedChannels.getAllSatisfiedChannels — used only when
// the in-memory cache is disabled. Returns every enabled channel that
// serves (group, model) across all priority tiers, deduplicated by
// channel id.
func getAllSatisfiedChannelsFromDB(group string, model string) ([]*Channel, error) {
	var abilities []Ability
	if err := CoreDB().Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ?", group, model, true, AbilityRoleInventory).Find(&abilities).Error; err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return nil, nil
	}
	// Deduplicate channel IDs (a channel can match multiple abilities if
	// the operator declared the same channel twice — defensive only).
	seen := make(map[int]struct{}, len(abilities))
	channelIDs := make([]int, 0, len(abilities))
	for _, ab := range abilities {
		if _, ok := seen[ab.ChannelId]; ok {
			continue
		}
		seen[ab.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ab.ChannelId)
	}
	var channels []*Channel
	if err := CoreDB().Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
		return nil, err
	}
	if err := decryptChannelKeysForUse(channels); err != nil {
		return nil, err
	}
	return channels, nil
}

func getRoutingCandidatesFromDB(group string, modelName string) ([]RoutingCandidate, error) {
	var abilities []Ability
	query := CoreDB().Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ?", group, modelName, true, AbilityRoleInventory)
	if err := query.Find(&abilities).Error; err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		normalized := NormalizePricingModelName(modelName)
		if normalized != modelName {
			if err := CoreDB().Where(abilityGroupCol()+" = ? and model = ? and enabled = ? and role <> ?", group, normalized, true, AbilityRoleInventory).Find(&abilities).Error; err != nil {
				return nil, err
			}
		}
	}
	result := make([]RoutingCandidate, 0, len(abilities))
	for _, ability := range abilities {
		channel, err := GetChannelById(ability.ChannelId, true)
		if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		priority := channel.GetPriority()
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		result = append(result, RoutingCandidate{
			Channel:  channel,
			Priority: EffectiveRoutingPriority(priority, ability.Role),
			Weight:   ability.Weight,
			Model:    ability.Model,
		})
	}
	return dropRetreatingCandidates(result, modelName, group), nil
}

// dropRetreatingCandidates removes routes the breaker has taken out, and gives
// up doing so the moment that would leave nothing.
//
// Failing open is the whole discipline here. A breaker exists to avoid a
// doomed attempt when a working alternative exists; with no alternative, the
// doomed attempt is strictly better than a certain refusal, and it doubles as
// the probe that lets the route recover.
func dropRetreatingCandidates(candidates []RoutingCandidate, modelName string, lane string) []RoutingCandidate {
	if len(candidates) <= 1 {
		return candidates
	}
	now := time.Now().UTC()
	kept := make([]RoutingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		routeModel := candidate.Model
		if routeModel == "" {
			routeModel = modelName
		}
		if candidate.Channel != nil && RouteIsRetreating(routeModel, lane, candidate.Channel.Id, now) {
			continue
		}
		kept = append(kept, candidate)
	}
	if len(kept) == 0 {
		return candidates
	}
	return kept
}

func GetRandomSatisfiedChannel(group string, model string, retry int) (*Channel, error) {
	var abilities []Ability

	var err error = nil
	channelQuery, err := getChannelQuery(group, model, retry)
	if err != nil {
		return nil, err
	}
	if common.UsingSQLite || common.UsingPostgreSQL {
		err = channelQuery.Order("weight DESC").Find(&abilities).Error
	} else {
		err = channelQuery.Order("weight DESC").Find(&abilities).Error
	}
	if err != nil {
		return nil, err
	}
	channel := Channel{}
	if len(abilities) > 0 {
		// Randomly choose one
		weightSum := uint(0)
		for _, ability_ := range abilities {
			weightSum += ability_.Weight + 10
		}
		// Randomly choose one
		weight := common.GetRandomInt(int(weightSum))
		for _, ability_ := range abilities {
			weight -= int(ability_.Weight) + 10
			//log.Printf("weight: %d, ability weight: %d", weight, *ability_.Weight)
			if weight <= 0 {
				channel.Id = ability_.ChannelId
				break
			}
		}
	} else {
		return nil, nil
	}
	err = CoreDB().First(&channel, "id = ?", channel.Id).Error
	if err == nil {
		err = channel.decryptKeyForUse()
	}
	return &channel, err
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	return channel.addAbilities(tx, nil)
}

// addAbilities creates this channel's routes. A route found in `existing`
// keeps the priority, weight and role it had; FixAbility passes the table as
// it was before the rebuild, so a repair never promotes a backup, never puts a
// parked spare back into service, and never re-parks a route that has already
// finished its observation.
func (channel *Channel) addAbilities(tx *gorm.DB, existing map[string]Ability) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	// A source created to be watched brings its routes in as inventory; they
	// go live one by one once their observation passes.
	observe := channelObservesNewRoutes(channel)
	var watched []RouteObservationCell
	for _, model := range models_ {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		for _, group := range groups_ {
			group = NormalizeChannelLane(group)
			if !IsValidChannelLane(group) {
				continue
			}
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Role:      AbilityRolePrimary,
			}
			if previous, ok := existing[key]; ok {
				ability.Priority = previous.Priority
				ability.Weight = previous.Weight
				ability.Role = NormalizeAbilityRole(previous.Role)
			} else if observe {
				ability.Role = AbilityRoleInventory
				watched = append(watched, RouteObservationCell{Lane: group, ModelName: model, ChannelId: channel.Id})
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose CoreDB() or provided tx
	useDB := CoreDB()
	if tx != nil {
		useDB = tx
	}
	if err := StartRouteObservations(useDB, watched, time.Now()); err != nil {
		return err
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return CoreDB().Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = CoreDB().Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// Preserve pool-local routing overrides for abilities that continue to
	// exist. Channel edits rebuild this relation for model/group membership,
	// status and tags; they must not silently replace an operator's
	// model/lane-specific priority, weight or role with the channel defaults.
	// Losing a role here would silently promote every backup and every parked
	// spare into live traffic the next time someone renamed a channel.
	var existingAbilities []Ability
	if err := tx.Where("channel_id = ?", channel.Id).Find(&existingAbilities).Error; err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}
	existingRouting := make(map[string]Ability, len(existingAbilities))
	for _, ability := range existingAbilities {
		existingRouting[ability.Group+"|"+ability.Model] = ability
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	observe := channelObservesNewRoutes(channel)
	var watched []RouteObservationCell
	for _, model := range models_ {
		// Mirror AddAbilities: a trailing comma or a stray space in the model
		// list must not become an ability row with an empty or padded name.
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		for _, group := range groups_ {
			group = NormalizeChannelLane(group)
			if !IsValidChannelLane(group) {
				continue
			}
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Role:      AbilityRolePrimary,
			}
			if existing, ok := existingRouting[key]; ok {
				ability.Priority = existing.Priority
				ability.Weight = existing.Weight
				ability.Role = NormalizeAbilityRole(existing.Role)
			} else if observe {
				// Only a route this edit adds is watched. One that already
				// carried traffic keeps its role; observing it would take a
				// working route out of service.
				ability.Role = AbilityRoleInventory
				watched = append(watched, RouteObservationCell{Lane: group, ModelName: model, ChannelId: channel.Id})
			}
			abilities = append(abilities, ability)
		}
	}

	keep := make(map[string]bool, len(abilitySet))
	for key := range abilitySet {
		keep[key] = true
	}
	if err := EndChannelRouteObservations(tx, channel.Id, keep); err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}
	if err := StartRouteObservations(tx, watched, time.Now()); err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return CoreDB().Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

// The tag lives on the channel; abilities follow it through channel_id.
func abilityChannelIDsByTag(tag string) *gorm.DB {
	return CoreDB().Model(&Channel{}).Select("id").Where("tag = ?", tag)
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return CoreDB().Model(&Ability{}).Where("channel_id IN (?)", abilityChannelIDsByTag(tag)).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, priority *int64, weight *uint) error {
	ability := Ability{}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return CoreDB().Model(&Ability{}).Where("channel_id IN (?)", abilityChannelIDsByTag(tag)).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, types.Err(types.ErrorCodeChannelTaskRunning, types.D{"task": "ability_fix"})
	}
	defer fixLock.Unlock()

	// The rebuild recreates every route, so what an operator set on each one
	// has to be read first and carried across.
	var before []Ability
	if err := CoreDB().Find(&before).Error; err != nil {
		return 0, 0, err
	}
	previous := make(map[int]map[string]Ability)
	for _, ability := range before {
		if previous[ability.ChannelId] == nil {
			previous[ability.ChannelId] = make(map[string]Ability)
		}
		previous[ability.ChannelId][ability.Group+"|"+ability.Model] = ability
	}

	// truncate abilities table
	if common.UsingSQLite {
		err := CoreDB().Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := CoreDB().Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := CoreDB().Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = CoreDB().Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.addAbilities(nil, previous[channel.Id])
			if err != nil {
				common.SysLog(fmt.Sprintf("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}

// channelObservesNewRoutes reports whether routes this channel newly serves
// start under observation. A Codex-only source is never observed: nothing but
// a real Codex session can reach it, so no probe or check could ever pass it.
func channelObservesNewRoutes(channel *Channel) bool {
	settings := channel.GetOtherSettings()
	return settings.ObserveNewRoutes && !settings.CodexClientOnly
}
