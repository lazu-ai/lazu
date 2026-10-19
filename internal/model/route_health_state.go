package model

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Route health state is the ENGINE's opinion about one (model, lane, channel)
// route. It is deliberately a different thing from `abilities.enabled` and
// `channels.status`, which are the OPERATOR's decisions.
//
// An operator pause is permanent until a human undoes it. A retreat is a
// temporary verdict the gateway reached on its own after consecutive upstream
// failures, and it expires by itself so a recovered upstream comes back
// without anyone being paged. Conflating the two — the way a plain
// auto-disable does — means every transient outage needs a human to undo it,
// which is exactly how a provider's four-hour hiccup turned into a four-hour
// outage for us.
const (
	RouteHealthStateHealthy = "healthy"
	RouteHealthStateRetreat = "retreat"

	// RouteHealthFailureThreshold is how many consecutive failures of a route
	// are needed before the router stops choosing it. One failure is noise:
	// providers return the occasional 500 and the relay already retries. Three
	// in a row is a pattern.
	RouteHealthFailureThreshold = 3

	// RouteHealthRetreatFor is how long a retreating route stays out of
	// rotation before a probe is allowed to try it again. It is short on
	// purpose: the cost of re-testing an upstream is one tiny request, while
	// the cost of leaving capacity parked is paid by every user of the lane.
	RouteHealthRetreatFor = 5 * time.Minute
)

// RouteHealthState is one route's breaker record.
type RouteHealthState struct {
	Lane                string     `gorm:"type:varchar(64);primaryKey;autoIncrement:false" json:"lane"`
	ModelName           string     `gorm:"type:varchar(255);primaryKey;autoIncrement:false" json:"model_name"`
	ChannelId           int        `gorm:"primaryKey;autoIncrement:false;index" json:"channel_id"`
	State               string     `gorm:"type:varchar(16);not null;default:'healthy';index" json:"state"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	RetreatUntil        *time.Time `json:"retreat_until,omitempty"`
	LastEvidenceAt      time.Time  `json:"last_evidence_at"`
	Reason              string     `gorm:"type:varchar(255)" json:"reason,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (RouteHealthState) TableName() string {
	return "route_health_state"
}

// IsRetreating reports whether the router should currently skip this route.
// The stored state is never trusted past its own deadline: a retreat that has
// expired is simply over, whether or not anything has written the row since.
func (s RouteHealthState) IsRetreating(now time.Time) bool {
	if s.State != RouteHealthStateRetreat {
		return false
	}
	return s.RetreatUntil != nil && now.Before(*s.RetreatUntil)
}

// routeHealthEntry is what the hot path needs to know about one troubled
// route. A route in good standing has NO entry at all, which is what makes
// both the routing check and the write check free for the overwhelming
// majority of requests.
type routeHealthEntry struct {
	failures     int
	retreatUntil time.Time
}

// routeHealthCache is the hot-path view of the table above.
//
// Two things read it on every request: the router, to skip a route the
// breaker has pulled, and the evidence recorder, to decide whether this
// observation could possibly change anything. Both must answer from memory.
// The map holds only troubled routes, so its size is bounded by how much is
// currently broken rather than by how much we serve.
var routeHealthCache = struct {
	sync.RWMutex
	routes map[string]routeHealthEntry
}{routes: map[string]routeHealthEntry{}}

// routeHealthTableCache remembers whether the table exists. Asking the
// migrator costs an information_schema query, and this is consulted on the
// request path.
var routeHealthTableCache = struct {
	sync.Mutex
	db        *gorm.DB
	checked   bool
	available bool
}{}

func routeHealthTableExists() bool {
	if CoreDB() == nil {
		return false
	}
	routeHealthTableCache.Lock()
	if routeHealthTableCache.checked && routeHealthTableCache.db == CoreDB() {
		available := routeHealthTableCache.available
		routeHealthTableCache.Unlock()
		return available
	}
	routeHealthTableCache.Unlock()

	available := CoreDB().Migrator().HasTable(&RouteHealthState{})

	routeHealthTableCache.Lock()
	routeHealthTableCache.db = CoreDB()
	routeHealthTableCache.checked = true
	routeHealthTableCache.available = available
	routeHealthTableCache.Unlock()
	return available
}

// RouteHealthCacheKey identifies a route inside the hot-path cache.
//
// The name is normalized because the two sides of this table do not agree on
// which name a route has. The relay knows the name the caller asked for; the
// router and the probe know the ability's own name, which for aliased models
// (the gizmo families, the gemini thinking-budget forms) is a different
// string. Keying on the raw name would file a relay's failures where nothing
// ever looks for them, so a probe success could not end a retreat caused by
// traffic, nor the reverse. Normalization is idempotent, so applying it to a
// name that is already an ability's is harmless.
func RouteHealthCacheKey(modelName string, lane string, channelID int) string {
	return ChannelHealthCellKey(NormalizePricingModelName(modelName), lane, channelID)
}

func lookupRouteHealth(key string) (routeHealthEntry, bool) {
	routeHealthCache.RLock()
	entry, ok := routeHealthCache.routes[key]
	routeHealthCache.RUnlock()
	return entry, ok
}

// RouteIsRetreating answers the router's question without touching the
// database. An unknown route is healthy: this cache can only ever remove
// capacity, so when in doubt it must remove none.
func RouteIsRetreating(modelName string, lane string, channelID int, now time.Time) bool {
	entry, ok := lookupRouteHealth(RouteHealthCacheKey(modelName, lane, channelID))
	if !ok || entry.retreatUntil.IsZero() {
		return false
	}
	return now.Before(entry.retreatUntil)
}

// ShouldRecordRouteHealthEvidence reports whether an observation can change
// anything, so the caller can skip the write entirely.
//
// A failure always can: it either starts a count or advances one. A success
// only can when this route already has trouble recorded against it. Without
// this test the gateway would perform one locked read-modify-write per
// relayed request, all of it to store the number zero on a route that was
// already zero — turning a breaker meant to protect the platform into the
// busiest writer on it.
func ShouldRecordRouteHealthEvidence(
	modelName string, lane string, channelID int, success bool,
) bool {
	if CoreDB() == nil || channelID <= 0 || strings.TrimSpace(modelName) == "" {
		return false
	}
	if !success {
		return true
	}
	_, ok := lookupRouteHealth(RouteHealthCacheKey(modelName, NormalizeChannelLane(lane), channelID))
	return ok
}

// RefreshRouteHealthCache reloads the troubled-route set from the database.
//
// It carries every route with a live retreat OR a non-zero failure count: the
// second kind is what lets a replica that did not see the failures still know
// that a success there is worth writing down.
func RefreshRouteHealthCache() {
	if apiControlPlaneReader() {
		logControlRefresh("route health", refreshRouteHealthFromRedis())
		return
	}
	logControlRefresh("route health", refreshRouteHealthCacheFromCore())
}

func refreshRouteHealthCacheFromCore() error {
	if CoreDB() == nil || !routeHealthTableExists() {
		return nil
	}
	revision, err := beginControlSnapshot(context.Background(), routeHealthSnapshotKey)
	if err != nil {
		return err
	}
	var rows []RouteHealthState
	now := time.Now().UTC()
	if err := CoreDB().Where("consecutive_failures > ? OR (state = ? AND retreat_until IS NOT NULL AND retreat_until > ?)",
		0, RouteHealthStateRetreat, now).Find(&rows).Error; err != nil {
		return err
	}
	if CoreDB().Dialector.Name() == "postgres" {
		if err := publishRouteHealthSnapshot(rows, revision); err != nil {
			return err
		}
	}
	installRouteHealthCache(rows)
	return nil
}

func installRouteHealthCache(rows []RouteHealthState) {
	next := make(map[string]routeHealthEntry, len(rows))
	for _, row := range rows {
		entry := routeHealthEntry{failures: row.ConsecutiveFailures}
		if row.State == RouteHealthStateRetreat && row.RetreatUntil != nil {
			entry.retreatUntil = row.RetreatUntil.UTC()
		}
		next[RouteHealthCacheKey(row.ModelName, row.Lane, row.ChannelId)] = entry
	}
	routeHealthCache.Lock()
	routeHealthCache.routes = next
	routeHealthCache.Unlock()
}

// SyncRouteHealthCache keeps the hot-path view fresh on its own schedule.
//
// It is deliberately independent of the channel cache: that one only runs when
// the in-memory channel cache is enabled, and a deployment without it would
// otherwise have a breaker that records verdicts and then never acts on them.
func SyncRouteHealthCache(frequency int) {
	if frequency <= 0 {
		frequency = 60
	}
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		RefreshRouteHealthCache()
	}
}

func setRouteHealthCacheEntry(key string, failures int, until *time.Time) {
	routeHealthCache.Lock()
	if failures <= 0 && until == nil {
		delete(routeHealthCache.routes, key)
	} else {
		entry := routeHealthEntry{failures: failures}
		if until != nil {
			entry.retreatUntil = until.UTC()
		}
		routeHealthCache.routes[key] = entry
	}
	routeHealthCache.Unlock()
}

// RecordRouteHealthEvidence folds one observation into a route's breaker.
//
// It is the single writer for both evidence sources — real relayed requests
// and synthetic probes — because a breaker fed by only one of them would
// either never trip on a quiet lane or never recover on a busy one.
func RecordRouteHealthEvidence(
	modelName string, lane string, channelID int, success bool, reason string, at time.Time,
) error {
	if CoreDB() == nil || channelID <= 0 {
		return nil
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil
	}
	lane = NormalizeChannelLane(lane)
	if !routeHealthTableExists() {
		return nil
	}
	// Store under the same name the key uses, so the row and the cache entry
	// describe one route rather than two.
	modelName = NormalizePricingModelName(modelName)
	key := RouteHealthCacheKey(modelName, lane, channelID)
	if success {
		// Nothing recorded against this route, so there is nothing for a
		// success to clear. Skipping here is what keeps the breaker's write
		// volume proportional to failures rather than to traffic.
		if _, ok := lookupRouteHealth(key); !ok {
			return nil
		}
	}
	at = at.UTC()

	var cached *time.Time
	cachedFailures := 0
	applied := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var row RouteHealthState
		// The row is locked for the read-modify-write: two replicas observing
		// the same upstream at the same moment must not each read "2 failures"
		// and each write "3", losing one of the observations.
		query := tx.Where("lane = ? AND model_name = ? AND channel_id = ?", lane, modelName, channelID)
		if !common.UsingSQLite {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		found := true
		if err := query.First(&row).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
			found = false
			row = RouteHealthState{Lane: lane, ModelName: modelName, ChannelId: channelID}
		}
		if row.LastEvidenceAt.After(at) {
			// Out-of-order evidence, e.g. a long request finishing after a
			// probe that started later. The newer verdict already stands, and
			// the cache must be left exactly as it is — treating this as a
			// write with no retreat would revive a route we just pulled.
			return nil
		}
		applied = true
		if success {
			row.ConsecutiveFailures = 0
			row.State = RouteHealthStateHealthy
			row.RetreatUntil = nil
			row.Reason = ""
		} else {
			row.ConsecutiveFailures++
			if row.ConsecutiveFailures >= RouteHealthFailureThreshold {
				row.State = RouteHealthStateRetreat
				until := at.Add(RouteHealthRetreatFor)
				row.RetreatUntil = &until
			}
			row.Reason = truncateRouteHealthReason(reason)
		}
		row.LastEvidenceAt = at
		row.UpdatedAt = time.Now().UTC()
		cached = row.RetreatUntil
		cachedFailures = row.ConsecutiveFailures
		if !found {
			return tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "lane"}, {Name: "model_name"}, {Name: "channel_id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"state", "consecutive_failures", "retreat_until",
					"last_evidence_at", "reason", "updated_at",
				}),
			}).Create(&row).Error
		}
		return tx.Save(&row).Error
	})
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	// Reflect the verdict immediately rather than waiting for the next cache
	// sync: the whole value of tripping a breaker is that the next request
	// does not repeat the failure.
	setRouteHealthCacheEntry(key, cachedFailures, cached)
	if CoreDB().Dialector.Name() == "postgres" {
		return refreshRouteHealthCacheFromCore()
	}
	return nil
}

func truncateRouteHealthReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 250 {
		return reason[:250]
	}
	return reason
}

// ClearRouteHealthRetreat ends a retreat now instead of at its deadline.
//
// It resets the failure count as well as the state: an operator saying "this
// is fixed" is telling us the evidence that tripped the breaker no longer
// describes the route, and leaving the count at its threshold would make the
// very next hiccup trip it again instantly.
func ClearRouteHealthRetreat(modelName string, lane string, channelID int) error {
	if CoreDB() == nil || !routeHealthTableExists() {
		return nil
	}
	lane = NormalizeChannelLane(lane)
	modelName = NormalizePricingModelName(strings.TrimSpace(modelName))
	if err := CoreDB().Model(&RouteHealthState{}).
		Where("lane = ? AND model_name = ? AND channel_id = ?", lane, modelName, channelID).
		Updates(map[string]any{
			"state":                RouteHealthStateHealthy,
			"consecutive_failures": 0,
			"retreat_until":        nil,
			"reason":               "",
			"updated_at":           time.Now().UTC(),
		}).Error; err != nil {
		return err
	}
	setRouteHealthCacheEntry(RouteHealthCacheKey(modelName, lane, channelID), 0, nil)
	if CoreDB().Dialector.Name() == "postgres" {
		return refreshRouteHealthCacheFromCore()
	}
	return nil
}

// LoadRouteHealthStates returns every stored breaker record, for the health
// page to report why a route is out of rotation.
func LoadRouteHealthStates() ([]RouteHealthState, error) {
	if CoreDB() == nil || !routeHealthTableExists() {
		return []RouteHealthState{}, nil
	}
	var rows []RouteHealthState
	if err := CoreDB().Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// PurgeStaleRouteHealthStates drops records for routes that have been quiet
// well past any retreat, so the table tracks live routing rather than growing
// a row per model name we ever served.
func PurgeStaleRouteHealthStates(before time.Time) error {
	if CoreDB() == nil || !routeHealthTableExists() {
		return nil
	}
	return CoreDB().Where("last_evidence_at < ? AND (state = ? OR retreat_until IS NULL OR retreat_until < ?)",
		before.UTC(), RouteHealthStateHealthy, time.Now().UTC()).
		Delete(&RouteHealthState{}).Error
}

// DeleteRouteHealthStatesForChannels drops every breaker record of the given
// channels, from the table and from the hot-path cache. A deleted channel's
// verdicts describe nothing that can be routed to, and left behind they keep
// showing up wherever breaker state is listed.
func DeleteRouteHealthStatesForChannels(channelIDs []int) error {
	if CoreDB() == nil || len(channelIDs) == 0 || !routeHealthTableExists() {
		return nil
	}
	if err := CoreDB().Where("channel_id IN ?", channelIDs).Delete(&RouteHealthState{}).Error; err != nil {
		return err
	}
	deleted := make(map[int]bool, len(channelIDs))
	for _, id := range channelIDs {
		deleted[id] = true
	}
	routeHealthCache.Lock()
	for key := range routeHealthCache.routes {
		if deleted[routeHealthCacheKeyChannel(key)] {
			delete(routeHealthCache.routes, key)
		}
	}
	routeHealthCache.Unlock()
	return nil
}

// PurgeOrphanRouteHealthStates drops breaker records whose channel no longer
// exists. Channel deletion cleans up after itself; this catches the bulk
// deletions that go straight to the channels table, and anything older.
func PurgeOrphanRouteHealthStates() error {
	if CoreDB() == nil || !routeHealthTableExists() || !CoreDB().Migrator().HasTable(&Channel{}) {
		return nil
	}
	return CoreDB().Where("channel_id NOT IN (?)", CoreDB().Model(&Channel{}).Select("id")).
		Delete(&RouteHealthState{}).Error
}

func routeHealthCacheKeyChannel(key string) int {
	index := strings.LastIndex(key, "\x1f")
	if index < 0 {
		return 0
	}
	id, err := strconv.Atoi(key[index+1:])
	if err != nil {
		return 0
	}
	return id
}

// ClaimRouteHealthFailure reports whether this request may contribute a failure
// to the given route's breaker count, and records that it has.
//
// The breaker trips on RouteHealthFailureThreshold CONSECUTIVE failures, a
// number chosen to mean "a pattern across requests" — its premise is that the
// relay retries, so a single request's retries are one piece of evidence, not
// three. Without this claim a request that exhausts its retries against one
// channel would post a failure per attempt and trip the breaker by itself,
// which on a single-channel lane takes the whole lane out of rotation on the
// strength of one unlucky caller.
//
// Successes are deliberately not claimed: each one resets the count, and a
// recovery is always worth writing down.
func ClaimRouteHealthFailure(c *gin.Context, modelName, lane string, channelID int) bool {
	if c == nil {
		return true
	}
	key := RouteHealthCacheKey(modelName, NormalizeChannelLane(lane), channelID)
	claimed, _ := c.Get(routeHealthClaimContextKey)
	seen, ok := claimed.(map[string]struct{})
	if !ok {
		seen = make(map[string]struct{}, 1)
		c.Set(routeHealthClaimContextKey, seen)
	}
	if _, already := seen[key]; already {
		return false
	}
	seen[key] = struct{}{}
	return true
}

const routeHealthClaimContextKey = "route_health_failure_claimed"
