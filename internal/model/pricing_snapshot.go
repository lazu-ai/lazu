package model

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

const pricingRegistryRedisKey = "lazu:pricing:registry:v1"

type pricingRegistry struct {
	Revision      int64               `json:"revision"`
	SellPrices    []ModelSellPrice    `json:"sell_prices"`
	ProviderCosts []ProviderCostPrice `json:"provider_costs"`
	LoadedAt      time.Time           `json:"loaded_at"`
}

func pricingSnapshotRequired() bool {
	return CoreDB() != nil && CoreDB().Dialector.Name() == "postgres"
}

func pricingSnapshotEnabled() bool { return pricingSnapshotRequired() || common.PricingSnapshotEnabled }

type pricingSnapshot struct {
	revision      int64
	db            *gorm.DB
	providerCosts map[string]map[int]ProviderCostPrice
	sellPrices    map[string]map[string]ModelSellPrice
	loadedAt      time.Time
}

var (
	activePricingSnapshot atomic.Value // *pricingSnapshot
	pricingSnapshotOnce   sync.Once
)

func InitPricingSnapshot() {
	if !pricingSnapshotEnabled() {
		return
	}
	if err := RefreshPricingSnapshot(); err != nil {
		common.SysLog("failed to load pricing snapshot: " + err.Error())
	}
	pricingSnapshotOnce.Do(func() {
		interval := time.Duration(common.PricingSnapshotRefreshSeconds) * time.Second
		if interval <= 0 {
			interval = time.Minute
		}
		common.SafeGo("pricing-snapshot-refresh", func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				refresh := RefreshPricingSnapshot
				if pricingSnapshotRequired() && common.ResolveProcessRole(os.Getenv("PROCESS_ROLE")) == common.ProcessRoleAPI {
					refresh = refreshPricingSnapshotFromRedis
				}
				if err := refresh(); err != nil {
					common.SysLog("failed to refresh pricing snapshot: " + err.Error())
				}
			}
		})
	})
}

func RefreshPricingSnapshot() error {
	if CoreDB() == nil {
		return nil
	}
	startedAt := time.Now()
	var revision int64
	if pricingSnapshotRequired() && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var err error
		revision, err = beginControlSnapshot(ctx, pricingRegistryRedisKey)
		if err != nil {
			return err
		}
	}
	var sellPrices []ModelSellPrice
	if pricingSnapshotRequired() || CoreDB().Migrator().HasTable(&ModelSellPrice{}) {
		if err := CoreDB().Where("active = ?", true).
			Order("model_name ASC, channel_group ASC, pricing_version DESC").
			Find(&sellPrices).Error; err != nil {
			return err
		}
	}

	var costs []ProviderCostPrice
	if providerCostPriceTableExists() {
		if err := CoreDB().Order("model_name ASC, channel_id ASC, pricing_version DESC").Find(&costs).Error; err != nil {
			return err
		}
	}
	registry := pricingRegistry{Revision: revision, SellPrices: sellPrices, ProviderCosts: costs, LoadedAt: startedAt}
	if pricingSnapshotRequired() && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := publishControlSnapshot(ctx, pricingRegistryRedisKey, registry, revision); err != nil {
			return err
		}
		// A newer administration read may have superseded this SQL read. Install
		// the winning shared snapshot, never the delayed local candidate.
		return refreshPricingSnapshotFromRedis()
	}
	installPricingRegistry(registry)
	return nil
}

func refreshPricingSnapshotFromRedis() error {
	if common.RDB == nil {
		return errors.New("pricing registry Redis client unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := common.RDB.Get(ctx, pricingRegistryRedisKey).Bytes()
	if err != nil {
		return err // retain the last complete registry; never query Core on API refresh
	}
	var registry pricingRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return err
	}
	if registry.LoadedAt.IsZero() {
		return errors.New("pricing registry has no snapshot timestamp")
	}
	if current, ok := currentPricingSnapshot(); ok {
		if registry.Revision < current.revision || registry.Revision == 0 && current.revision == 0 && registry.LoadedAt.Before(current.loadedAt) {
			return nil
		}
	}
	installPricingRegistry(registry)
	return nil
}

func installPricingRegistry(registry pricingRegistry) {
	snapshot := &pricingSnapshot{
		revision: registry.Revision,
		db:       CoreDB(), providerCosts: make(map[string]map[int]ProviderCostPrice),
		sellPrices: make(map[string]map[string]ModelSellPrice),
		loadedAt:   registry.LoadedAt,
	}
	sellPrices, costs := registry.SellPrices, registry.ProviderCosts
	seen := make(map[string]struct{}, len(sellPrices))
	for _, row := range sellPrices {
		row.ChannelGroup = NormalizeChannelLane(row.ChannelGroup)
		if !IsKnownChannelLane(row.ChannelGroup) {
			row.ChannelGroup = ChannelLaneDirect
		}
		key := row.ModelName + "\x00" + row.ChannelGroup
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := snapshot.sellPrices[row.ModelName]; !ok {
			snapshot.sellPrices[row.ModelName] = make(map[string]ModelSellPrice)
		}
		snapshot.sellPrices[row.ModelName][row.ChannelGroup] = row
	}
	for _, row := range costs {
		if snapshot.providerCosts[row.ModelName] == nil {
			snapshot.providerCosts[row.ModelName] = make(map[int]ProviderCostPrice)
		}
		if _, exists := snapshot.providerCosts[row.ModelName][row.ChannelID]; !exists {
			snapshot.providerCosts[row.ModelName][row.ChannelID] = row
		}
	}
	activePricingSnapshot.Store(snapshot)
}

func currentPricingSnapshot() (*pricingSnapshot, bool) {
	if !pricingSnapshotEnabled() {
		return nil, false
	}
	value := activePricingSnapshot.Load()
	if value == nil {
		return nil, false
	}
	snapshot, ok := value.(*pricingSnapshot)
	return snapshot, ok && snapshot != nil && snapshot.db == CoreDB()
}

func getSnapshotActiveModelSellPrice(modelName, channelGroup string) (ModelSellPrice, bool) {
	snapshot, ok := currentPricingSnapshot()
	if !ok {
		return ModelSellPrice{}, false
	}
	if row, ok := snapshot.findModelSellPrice(modelName, channelGroup); ok {
		return row, true
	}
	if matched := NormalizePricingModelName(modelName); matched != "" && matched != modelName {
		return snapshot.findModelSellPrice(matched, channelGroup)
	}
	return ModelSellPrice{}, false
}

func getSnapshotActiveModelSellPriceLaneMap() (map[string][]ModelSellPrice, bool) {
	snapshot, ok := currentPricingSnapshot()
	if !ok {
		return nil, false
	}
	result := make(map[string][]ModelSellPrice, len(snapshot.sellPrices))
	for modelName, lanes := range snapshot.sellPrices {
		for _, row := range lanes {
			result[modelName] = append(result[modelName], row)
		}
	}
	return result, true
}

func (snapshot *pricingSnapshot) findModelSellPrice(modelName, channelGroup string) (ModelSellPrice, bool) {
	channelGroup = NormalizeChannelLane(channelGroup)
	if !IsKnownChannelLane(channelGroup) {
		channelGroup = ChannelLaneDirect
	}
	lanes, ok := snapshot.sellPrices[modelName]
	if !ok {
		return ModelSellPrice{}, false
	}
	row, ok := lanes[channelGroup]
	return row, ok
}

func (snapshot *pricingSnapshot) findProviderCost(modelName string, channelID int) (ProviderCostPrice, bool) {
	rows := snapshot.providerCosts[modelName]
	if channelID > 0 {
		if row, ok := rows[channelID]; ok {
			return row, true
		}
	}
	row, ok := rows[0]
	return row, ok
}
