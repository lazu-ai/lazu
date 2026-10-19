package model

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"gorm.io/gorm"
)

// The SQL callbacks reject every query. This unit fixture selects the PG API
// branch without relying on a live Core server; Redis remains a real server.
type controlPlaneReaderDialector struct{ gorm.Dialector }

func (controlPlaneReaderDialector) Name() string { return "postgres" }

func TestControlPlaneAPIReadersUseRedisWithoutSQLIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	db := withChannelKeyTestDB(t, "control_plane_reader_no_sql")
	common.MemoryCacheEnabled = true
	db.Config.Dialector = controlPlaneReaderDialector{db.Config.Dialector}
	t.Setenv("PROCESS_ROLE", common.ProcessRoleAPI)
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	oldRedis := common.RDB
	common.RDB = client
	oldPricing, oldVendors, oldTime := pricingMap, vendorsList, lastGetPricingTime
	oldEndpointTypes, oldEndpointInfo, oldSupported := modelSupportEndpointTypes, modelEndpointInfoMap, supportedEndpointMap
	oldGroups, oldQuotaTypes := modelEnableGroups, modelQuotaTypeMap
	oldModelVendors, oldVendorDetails, oldOfficial := modelVendorMap, vendorDetailMap, modelOfficialPriceMap
	oldChannels, oldAbilities, oldOfferings, oldReady := channelsIDM, group2model2channels, channelOfferings, channelOfferingsReady
	oldRouteHealth := routeHealthCache.routes
	oldOptionValue := GetOptionValue("ControlSnapshotTest")
	t.Cleanup(func() {
		client.Del(ctx, channelRoutingSnapshotKey, routeHealthSnapshotKey, optionsSnapshotKey, catalogSnapshotKey, optionsSnapshotKey+":revision")
		client.Close()
		common.RDB = oldRedis
		pricingMap, vendorsList, lastGetPricingTime = oldPricing, oldVendors, oldTime
		modelSupportEndpointTypes, modelEndpointInfoMap, supportedEndpointMap = oldEndpointTypes, oldEndpointInfo, oldSupported
		modelEnableGroups, modelQuotaTypeMap = oldGroups, oldQuotaTypes
		modelVendorMap, vendorDetailMap, modelOfficialPriceMap = oldModelVendors, oldVendorDetails, oldOfficial
		channelsIDM, group2model2channels, channelOfferings, channelOfferingsReady = oldChannels, oldAbilities, oldOfferings, oldReady
		routeHealthCache.routes = oldRouteHealth
		common.OptionMapRWMutex.Lock()
		if oldOptionValue == "" {
			delete(common.OptionMap, "ControlSnapshotTest")
		} else {
			common.OptionMap["ControlSnapshotTest"] = oldOptionValue
		}
		common.OptionMapRWMutex.Unlock()
	})
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	channel := &Channel{Id: 9911, Key: "sk-control-snapshot-secret", KeyDigest: "digest", KeyDigests: "digests", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI}
	ability := &Ability{ChannelId: channel.Id, Group: ChannelLaneAgent, Model: "snapshot-model", Enabled: true}
	if err := publishChannelRoutingSnapshot([]*Channel{channel}, []*Ability{ability}, 0); err != nil {
		t.Fatal(err)
	}
	expectedChannel, err := channel.storageCopyWithEncryptedKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := client.Get(ctx, channelRoutingSnapshotKey).Result()
	if strings.Contains(raw, channel.Key) {
		t.Fatal("snapshot exposed plaintext channel key")
	}
	until := time.Now().Add(time.Minute)
	if err := publishRouteHealthSnapshot([]RouteHealthState{{ModelName: ability.Model, Lane: ability.Group, ChannelId: channel.Id, State: RouteHealthStateRetreat, ConsecutiveFailures: 3, RetreatUntil: &until}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := publishOptionsSnapshot([]*Option{{Key: "ControlSnapshotTest", Value: "redis-value"}}, 0); err != nil {
		t.Fatal(err)
	}
	catalog := catalogSnapshot{Pricing: []Pricing{{ModelName: ability.Model}}, Vendors: []PricingVendor{{Name: "snapshot-vendor"}}, VendorIDs: []int{99}, ModelVendors: map[string]int{ability.Model: 99}, Groups: map[string][]string{ability.Model: {ChannelLaneAgent}}}
	if err := publishControlSnapshot(ctx, catalogSnapshotKey, catalog, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	block := func(tx *gorm.DB) { calls++; tx.AddError(errors.New("unexpected API Core SQL")) }
	db.Callback().Query().Before("gorm:query").Register("test:no_api_sql", block)
	db.Callback().Row().Before("gorm:row").Register("test:no_api_sql", block)
	if err := refreshChannelRoutingFromRedis(); err != nil {
		t.Fatal(err)
	}
	cached, err := CacheGetChannel(channel.Id)
	if err != nil || cached.Key != channel.Key || cached.KeyDigest != expectedChannel.KeyDigest || cached.KeyDigests != expectedChannel.KeyDigests {
		t.Fatalf("channel snapshot roundtrip: %+v %v", cached, err)
	}
	configured, enabled, ready := CachedLaneOffering(ability.Model, ChannelLaneAgent)
	if !ready || configured != 1 || enabled != 1 {
		t.Fatalf("cached offering %d/%d ready=%t", configured, enabled, ready)
	}
	RefreshRouteHealthCache()
	if entry, ok := lookupRouteHealth(RouteHealthCacheKey(ability.Model, ability.Group, channel.Id)); !ok || entry.failures != 3 {
		t.Fatalf("route health snapshot: %+v %t", entry, ok)
	}
	if err := syncOptionsOnce(); err != nil {
		t.Fatal(err)
	}
	if GetOptionValue("ControlSnapshotTest") != "redis-value" {
		t.Fatal("options were not refreshed")
	}
	lastGetPricingTime = time.Time{}
	prices := GetPricing()
	if len(prices) != 1 || prices[0].InternalVendorID != 99 || len(prices[0].EnableGroup) != 1 || GetVendors()[0].InternalID != 99 {
		t.Fatalf("catalog internal identities lost: %+v", prices)
	}
	client.Del(ctx, catalogSnapshotKey, optionsSnapshotKey)
	lastGetPricingTime = time.Time{}
	if got := GetPricing(); len(got) != 1 {
		t.Fatal("Redis failure discarded last valid catalog")
	}
	if err := syncOptionsOnce(); err == nil {
		t.Fatal("missing snapshot was not reported")
	}
	if calls != 0 {
		t.Fatalf("API snapshot readers issued %d Core queries", calls)
	}
}

func TestControlSnapshotDelayedPublisherCannotReplaceNewerRevisionIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	old := common.RDB
	common.RDB = client
	key := "lazu:control:test-ordering"
	t.Cleanup(func() { client.Del(ctx, key, key+":revision"); client.Close(); common.RDB = old })
	client.Set(ctx, key+":revision", 1, 0)
	if err := publishControlSnapshot(ctx, key, "old", 1); err != nil {
		t.Fatal(err)
	}
	client.Set(ctx, key+":revision", 2, 0)
	if err := publishControlSnapshot(ctx, key, "new", 2); err != nil {
		t.Fatal(err)
	}
	if err := publishControlSnapshot(ctx, key, "late-old", 1); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := readControlSnapshot(ctx, key, &got); err != nil || got != "new" {
		t.Fatalf("stale publisher replaced committed snapshot: %q %v", got, err)
	}
}
