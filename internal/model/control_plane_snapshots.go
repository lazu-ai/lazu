package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

const channelRoutingSnapshotKey = "lazu:control:channels"
const routeHealthSnapshotKey = "lazu:control:route-health"
const optionsSnapshotKey = "lazu:control:options"
const catalogSnapshotKey = "lazu:control:catalog"

func apiControlPlaneReader() bool {
	return CoreDB() != nil && CoreDB().Dialector.Name() == "postgres" && common.ResolveProcessRole(os.Getenv("PROCESS_ROLE")) == common.ProcessRoleAPI
}

// Capture before the SQL read. A later administration refresh invalidates an
// older jobs read, so a delayed publisher cannot replace newer committed data.
func beginControlSnapshot(ctx context.Context, key string) (int64, error) {
	if CoreDB() == nil || CoreDB().Dialector.Name() != "postgres" {
		return 0, nil
	}
	if common.RDB == nil {
		return 0, errors.New("control-plane Redis unavailable")
	}
	return common.RDB.Incr(ctx, key+":revision").Result()
}

func publishControlSnapshot(ctx context.Context, key string, value interface{}, revision int64) error {
	if common.RDB == nil {
		return errors.New("control-plane Redis unavailable")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if revision == 0 {
		return common.RDB.Set(ctx, key, string(raw), 0).Err()
	}
	_, err = common.RDB.Eval(ctx, `if redis.call('GET',KEYS[2]) == ARGV[1] then redis.call('SET',KEYS[1],ARGV[2]); return 1 end; return 0`, []string{key, key + ":revision"}, strconv.FormatInt(revision, 10), string(raw)).Result()
	return err
}
func readControlSnapshot(ctx context.Context, key string, value interface{}) error {
	if common.RDB == nil {
		return errors.New("control-plane Redis unavailable")
	}
	raw, err := common.RDB.Get(ctx, key).Result()
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), value)
}

// Channels carry storage-encrypted keys, not decrypted process credentials.
// The API decrypts only after reading the shared policy snapshot.
type channelRoutingSnapshot struct {
	ChannelIDs []int             `json:"channel_ids"`
	Channels   []*Channel        `json:"channels"`
	Abilities  []*Ability        `json:"abilities"`
	Digests    map[int][2]string `json:"digests"`
}

func publishChannelRoutingSnapshot(channels []*Channel, abilities []*Ability, revision int64) error {
	digests := make(map[int][2]string, len(channels))
	stored := make([]*Channel, 0, len(channels))
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			return errors.New("invalid nil channel")
		}
		copy, err := channel.storageCopyWithEncryptedKey()
		if err != nil {
			return err
		}
		stored = append(stored, copy)
		ids = append(ids, copy.Id)
		digests[copy.Id] = [2]string{copy.KeyDigest, copy.KeyDigests}
	}
	return publishControlSnapshot(context.Background(), channelRoutingSnapshotKey, channelRoutingSnapshot{ChannelIDs: ids, Channels: stored, Abilities: abilities, Digests: digests}, revision)
}
func refreshChannelRoutingFromRedis() error {
	var snapshot channelRoutingSnapshot
	if err := readControlSnapshot(context.Background(), channelRoutingSnapshotKey, &snapshot); err != nil {
		return err
	}
	if len(snapshot.Channels) != len(snapshot.ChannelIDs) {
		return errors.New("invalid channel identities")
	}
	for i, channel := range snapshot.Channels {
		if channel == nil {
			return errors.New("invalid nil channel")
		}
		channel.Id = snapshot.ChannelIDs[i]
		if channel.Id <= 0 {
			return errors.New("invalid channel identity")
		}
		digest := snapshot.Digests[channel.Id]
		channel.KeyDigest, channel.KeyDigests = digest[0], digest[1]
	}
	for _, ability := range snapshot.Abilities {
		if ability == nil {
			return errors.New("invalid nil ability")
		}
	}
	if err := decryptChannelKeysForUse(snapshot.Channels); err != nil {
		return err
	}
	installChannelRoutingCache(snapshot.Channels, snapshot.Abilities)
	return nil
}
func publishRouteHealthSnapshot(rows []RouteHealthState, revision int64) error {
	return publishControlSnapshot(context.Background(), routeHealthSnapshotKey, rows, revision)
}
func refreshRouteHealthFromRedis() error {
	var rows []RouteHealthState
	if err := readControlSnapshot(context.Background(), routeHealthSnapshotKey, &rows); err != nil {
		return err
	}
	installRouteHealthCache(rows)
	return nil
}
func refreshOptionsFromRedis() error {
	var options []*Option
	if err := readControlSnapshot(context.Background(), optionsSnapshotKey, &options); err != nil {
		return err
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := updateOptionMap(option.Key, option.Value); err != nil {
			return err
		}
	}
	return nil
}
func publishOptionsSnapshot(options []*Option, revision int64) error {
	return publishControlSnapshot(context.Background(), optionsSnapshotKey, options, revision)
}
func syncOptionsOnce() error {
	if apiControlPlaneReader() {
		return refreshOptionsFromRedis()
	}
	revision, err := beginControlSnapshot(context.Background(), optionsSnapshotKey)
	if err != nil {
		return err
	}
	options, err := AllOption()
	if err != nil {
		return err
	}
	for _, option := range options {
		if err := updateOptionMap(option.Key, option.Value); err != nil {
			return err
		}
	}
	if CoreDB() != nil && CoreDB().Dialector.Name() == "postgres" {
		return publishOptionsSnapshot(options, revision)
	}
	return nil
}

// Control-plane option edits publish their committed values immediately. This
// read is allowed for administration; background API refresh only reads Redis.
func publishCommittedOptions() error {
	if CoreDB() == nil || CoreDB().Dialector.Name() != "postgres" {
		return nil
	}
	revision, err := beginControlSnapshot(context.Background(), optionsSnapshotKey)
	if err != nil {
		return err
	}
	options, err := AllOption()
	if err != nil {
		return err
	}
	return publishOptionsSnapshot(options, revision)
}

type catalogSnapshot struct {
	Pricing            []Pricing                                 `json:"pricing"`
	Vendors            []PricingVendor                           `json:"vendors"`
	VendorIDs          []int                                     `json:"vendor_ids"`
	EndpointTypes      map[string][]constant.EndpointType        `json:"endpoint_types"`
	EndpointInfo       map[string]map[string]common.EndpointInfo `json:"endpoint_info"`
	SupportedEndpoints map[string]common.EndpointInfo            `json:"supported_endpoints"`
	Groups             map[string][]string                       `json:"groups"`
	QuotaTypes         map[string]int                            `json:"quota_types"`
	ModelVendors       map[string]int                            `json:"model_vendors"`
	VendorDetails      map[int]PricingVendor                     `json:"vendor_details"`
	OfficialPrices     map[string]ModelOfficialPrice             `json:"official_prices"`
	Reasoning          map[string]ModelReasoningOptions          `json:"reasoning"`
}

// Called while updatePricingLock and modelSupportEndpointsLock are held.
func publishCatalogSnapshot(reasoning map[string]ModelReasoningOptions, revision int64) error {
	snapshot := catalogSnapshot{Pricing: pricingMap, Vendors: vendorsList, EndpointTypes: modelSupportEndpointTypes, EndpointInfo: modelEndpointInfoMap, SupportedEndpoints: supportedEndpointMap, Groups: modelEnableGroups, QuotaTypes: modelQuotaTypeMap, ModelVendors: modelVendorMap, VendorDetails: vendorDetailMap, OfficialPrices: modelOfficialPriceMap, Reasoning: reasoning}
	for _, vendor := range vendorsList {
		snapshot.VendorIDs = append(snapshot.VendorIDs, vendor.InternalID)
	}
	return publishControlSnapshot(context.Background(), catalogSnapshotKey, snapshot, revision)
}
func refreshCatalogFromRedis() error {
	var snapshot catalogSnapshot
	if err := readControlSnapshot(context.Background(), catalogSnapshotKey, &snapshot); err != nil {
		return err
	}
	if len(snapshot.Vendors) != len(snapshot.VendorIDs) {
		return errors.New("invalid catalog vendor identities")
	}
	for i := range snapshot.Vendors {
		snapshot.Vendors[i].InternalID = snapshot.VendorIDs[i]
	}
	for id, detail := range snapshot.VendorDetails {
		detail.InternalID = id
		snapshot.VendorDetails[id] = detail
	}
	for i := range snapshot.Pricing {
		snapshot.Pricing[i].InternalVendorID = snapshot.ModelVendors[snapshot.Pricing[i].ModelName]
		snapshot.Pricing[i].EnableGroup = snapshot.Groups[snapshot.Pricing[i].ModelName]
	}
	pricingMap, vendorsList = snapshot.Pricing, snapshot.Vendors
	modelSupportEndpointTypes, modelEndpointInfoMap, supportedEndpointMap = snapshot.EndpointTypes, snapshot.EndpointInfo, snapshot.SupportedEndpoints
	modelEnableGroupsLock.Lock()
	modelEnableGroups, modelQuotaTypeMap = snapshot.Groups, snapshot.QuotaTypes
	modelEnableGroupsLock.Unlock()
	modelVendorLock.Lock()
	modelVendorMap, vendorDetailMap, modelOfficialPriceMap = snapshot.ModelVendors, snapshot.VendorDetails, snapshot.OfficialPrices
	modelVendorLock.Unlock()
	setCatalogReasoningOptions(snapshot.Reasoning)
	lastGetPricingTime = time.Now()
	return nil
}

// Startup and operator changes may load SQL once. Warm API requests and timer
// ticks use the published projection even when it is absent or Redis fails.
func WarmCatalogCacheFromCore() { logControlRefresh("catalog", warmCatalogCacheFromCore()) }
func warmCatalogCacheFromCore() error {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	modelSupportEndpointsLock.Lock()
	defer modelSupportEndpointsLock.Unlock()
	return updatePricingFromCore()
}
func RefreshControlPlaneSnapshots(ctx context.Context) error {
	if apiControlPlaneReader() {
		return errors.New("API processes cannot run SQL control-plane refresh tasks")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syncOptionsOnce(); err != nil {
		return err
	}
	if err := initChannelCacheFromCore(); err != nil {
		return err
	}
	// The jobs process writes breaker verdicts too: probes and asynchronous
	// media results. A success only reaches the breaker when the writer's own
	// view already lists the route as troubled, and the API's sync loop does
	// not run here, so without this refresh every success recorded by jobs
	// was dropped and a route tripped by the API could never be cleared by a
	// probe or a delivered video.
	if err := refreshRouteHealthCacheFromCore(); err != nil {
		return err
	}
	if err := RecalculateRetiredModelRatingRanks(); err != nil {
		return err
	}
	return warmCatalogCacheFromCore()
}
func logControlRefresh(kind string, err error) {
	if err != nil {
		common.SysLog(fmt.Sprintf("%s snapshot refresh: %v", kind, err))
	}
}

func SyncCatalogCache(frequency int) {
	if frequency <= 0 {
		frequency = 60
	}
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		if !apiControlPlaneReader() {
			continue
		}
		updatePricingLock.Lock()
		modelSupportEndpointsLock.Lock()
		err := refreshCatalogFromRedis()
		modelSupportEndpointsLock.Unlock()
		updatePricingLock.Unlock()
		logControlRefresh("catalog", err)
	}
}
