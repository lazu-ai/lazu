package model

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func setupRouteHealthTestDB(t *testing.T) {
	t.Helper()
	setupChannelHealthTestDB(t)
	if err := coreDB.AutoMigrate(&RouteHealthState{}); err != nil {
		t.Fatalf("migrate route health: %v", err)
	}
	RefreshRouteHealthCache()
	t.Cleanup(func() {
		routeHealthCache.Lock()
		routeHealthCache.routes = map[string]routeHealthEntry{}
		routeHealthCache.Unlock()
		routeHealthTableCache.Lock()
		routeHealthTableCache.checked = false
		routeHealthTableCache.db = nil
		routeHealthTableCache.Unlock()
	})
}

// A success on a route with nothing recorded against it must not touch the
// database at all. Writing one row per relayed request would make the breaker
// the busiest writer on the platform, which is the opposite of protecting it.
func TestRecordRouteHealthEvidenceSkipsNoOpSuccess(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()

	if ShouldRecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, true) {
		t.Fatal("a success on an untroubled route has nothing to record")
	}
	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, true, "", now); err != nil {
		t.Fatalf("record success: %v", err)
	}
	var rows int64
	coreDB.Model(&RouteHealthState{}).Count(&rows)
	if rows != 0 {
		t.Fatalf("wrote %d rows for a no-op success, want none", rows)
	}

	// A failure always has something to say, and once it has been said a
	// later success does too.
	if !ShouldRecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, false) {
		t.Fatal("a failure must always be recorded")
	}
	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, false, "boom",
		now.Add(time.Second)); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if !ShouldRecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, true) {
		t.Fatal("a success must clear a route that already has failures against it")
	}
	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, true, "",
		now.Add(2*time.Second)); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if ShouldRecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, true) {
		t.Fatal("once cleared, the route is untroubled again and needs no writes")
	}
}

// One bad response is noise; a run of them is a pattern. The breaker must not
// pull a route out of rotation before it has seen one.
func TestRecordRouteHealthEvidenceTripsOnlyAfterThreshold(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()

	for attempt := 1; attempt < RouteHealthFailureThreshold; attempt++ {
		if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, false, "upstream 500",
			now.Add(time.Duration(attempt)*time.Second)); err != nil {
			t.Fatalf("record failure: %v", err)
		}
		if RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 3, now.Add(time.Minute)) {
			t.Fatalf("route retreated after %d failures, threshold is %d",
				attempt, RouteHealthFailureThreshold)
		}
	}

	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 3, false, "upstream 500",
		now.Add(time.Duration(RouteHealthFailureThreshold)*time.Second)); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if !RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 3, now.Add(time.Minute)) {
		t.Fatal("expected the route to retreat once the threshold was reached")
	}
	// The retreat is a deadline, not a latch: past it the route is eligible
	// again without anyone touching the row.
	if RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 3, now.Add(RouteHealthRetreatFor+time.Minute)) {
		t.Fatal("a retreat must expire on its own")
	}
}

// A single success is enough to end a retreat: that is the half-open probe
// closing the circuit.
func TestRecordRouteHealthEvidenceRecoversOnSuccess(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()
	for attempt := 0; attempt < RouteHealthFailureThreshold; attempt++ {
		if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 4, false, "boom",
			now.Add(time.Duration(attempt)*time.Second)); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	if !RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 4, now) {
		t.Fatal("expected a retreat before recovery")
	}
	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 4, true, "",
		now.Add(time.Minute)); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 4, now.Add(time.Minute)) {
		t.Fatal("a success must end the retreat immediately")
	}
	var row RouteHealthState
	if err := coreDB.Where("channel_id = ?", 4).First(&row).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if row.ConsecutiveFailures != 0 || row.State != RouteHealthStateHealthy {
		t.Fatalf("state = %+v, want a cleared counter", row)
	}
}

// Late evidence from a slow request must not overwrite a newer verdict.
func TestRecordRouteHealthEvidenceIgnoresOutOfOrderObservations(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()
	for attempt := 0; attempt < RouteHealthFailureThreshold; attempt++ {
		if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 5, false, "boom",
			now.Add(time.Duration(attempt)*time.Second)); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	// A request that started before the failures but finished after them.
	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 5, true, "",
		now.Add(-time.Hour)); err != nil {
		t.Fatalf("record stale success: %v", err)
	}
	if !RouteIsRetreating("gpt-5.2", ChannelLaneDirect, 5, now) {
		t.Fatal("stale evidence must not revive a retreating route")
	}
}

// The breaker may only ever remove capacity that something else can cover.
func TestDropRetreatingAbilitiesFailsOpen(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()
	for _, channelID := range []int{7, 8} {
		for attempt := 0; attempt < RouteHealthFailureThreshold; attempt++ {
			if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, channelID, false, "boom",
				now.Add(time.Duration(attempt)*time.Second)); err != nil {
				t.Fatalf("record failure: %v", err)
			}
		}
	}
	both := []cachedAbility{
		{ChannelID: 7, Model: "gpt-5.2", Lane: ChannelLaneDirect},
		{ChannelID: 8, Model: "gpt-5.2", Lane: ChannelLaneDirect},
	}
	if got := dropRetreatingAbilities(both); len(got) != 2 {
		t.Fatalf("kept %d candidates, want both back when every route is in retreat", len(got))
	}

	if err := RecordRouteHealthEvidence("gpt-5.2", ChannelLaneDirect, 8, true, "",
		now.Add(time.Minute)); err != nil {
		t.Fatalf("record success: %v", err)
	}
	got := dropRetreatingAbilities(both)
	if len(got) != 1 || got[0].ChannelID != 8 {
		t.Fatalf("kept %+v, want only the recovered upstream", got)
	}
}

// Roles decide what a source is for; the router reads that as a priority tier
// so failover needs no separate code path.
func TestEffectiveRoutingPrioritySinksBackups(t *testing.T) {
	if got := EffectiveRoutingPriority(5, AbilityRolePrimary); got != 5 {
		t.Fatalf("primary priority = %d, want it untouched", got)
	}
	backup := EffectiveRoutingPriority(1000, AbilityRoleBackup)
	lowestPrimary := EffectiveRoutingPriority(-1000, AbilityRolePrimary)
	if backup >= lowestPrimary {
		t.Fatalf("backup priority %d must sit below every primary (%d)", backup, lowestPrimary)
	}
	if NormalizeAbilityRole("") != AbilityRolePrimary {
		t.Fatal("an unset role must read as primary, the state every pre-role row was in")
	}
}

// The breaker's threshold counts failing REQUESTS. A request that exhausts its
// same-channel retries posts one failure per attempt plus one for the final
// outcome; left unclaimed that is four, enough to retreat a route — and on a
// single-channel lane, to take the lane out from under everyone — on the
// strength of one unlucky caller.
func TestClaimRouteHealthFailureCountsRequestsNotAttempts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	claimed := 0
	for attempt := 0; attempt < RouteHealthFailureThreshold+1; attempt++ {
		if ClaimRouteHealthFailure(c, "gpt-5.6-sol", ChannelLaneAgent, 83) {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("one request contributed %d failures to the breaker, want 1", claimed)
	}

	// A different route in the same request is its own evidence.
	if !ClaimRouteHealthFailure(c, "gpt-5.6-sol", ChannelLaneAgent, 84) {
		t.Fatal("a second channel in the same request was silenced")
	}
	if !ClaimRouteHealthFailure(c, "gpt-5.6-terra", ChannelLaneAgent, 83) {
		t.Fatal("a second model in the same request was silenced")
	}
	if !ClaimRouteHealthFailure(c, "gpt-5.6-sol", ChannelLaneDirect, 83) {
		t.Fatal("a second lane in the same request was silenced")
	}

	// The next request starts clean, so real patterns still reach the threshold.
	next, _ := gin.CreateTestContext(httptest.NewRecorder())
	if !ClaimRouteHealthFailure(next, "gpt-5.6-sol", ChannelLaneAgent, 83) {
		t.Fatal("a later request could not contribute its own failure")
	}
}

// Deleting a channel takes its breaker records with it, from the table and
// from the hot-path cache, and the retention sweep catches any a bulk delete
// left behind.
func TestRouteHealthStatesLeaveWithTheirChannel(t *testing.T) {
	setupRouteHealthTestDB(t)
	now := time.Now().UTC()
	if err := coreDB.Create(&Channel{Id: 5, Name: "kept", Status: 1}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, channelID := range []int{5, 37, 38} {
		for i := 0; i < RouteHealthFailureThreshold; i++ {
			if err := RecordRouteHealthEvidence("gpt-5.5", ChannelLaneAgent, channelID, false, "401", now.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatalf("record failure: %v", err)
			}
		}
	}

	if err := DeleteRouteHealthStatesForChannels([]int{37}); err != nil {
		t.Fatalf("delete states: %v", err)
	}
	if RouteIsRetreating("gpt-5.5", ChannelLaneAgent, 37, now) {
		t.Fatal("deleted channel is still retreating in the hot-path cache")
	}
	if !RouteIsRetreating("gpt-5.5", ChannelLaneAgent, 38, now) {
		t.Fatal("an unrelated channel lost its breaker state")
	}

	if err := PurgeOrphanRouteHealthStates(); err != nil {
		t.Fatalf("purge orphans: %v", err)
	}
	var remaining []RouteHealthState
	if err := coreDB.Find(&remaining).Error; err != nil {
		t.Fatalf("load states: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ChannelId != 5 {
		t.Fatalf("remaining = %+v, want only the existing channel's record", remaining)
	}
}

// A route tripped by one process must be clearable by a success observed in
// another. The jobs process runs no breaker sync loop of its own; its
// control-plane refresh is what loads the troubled-route view.
func TestRouteHealthRefreshLetsJobsClearTrippedRoutes(t *testing.T) {
	setupRouteHealthTestDB(t)
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if err := coreDB.Create(&RouteHealthState{
		Lane: ChannelLaneDirect, ModelName: "seedance-2.5", ChannelId: 9,
		State: RouteHealthStateRetreat, ConsecutiveFailures: 3, LastEvidenceAt: at,
	}).Error; err != nil {
		t.Fatalf("seed tripped route: %v", err)
	}
	routeHealthCache.Lock()
	routeHealthCache.routes = map[string]routeHealthEntry{}
	routeHealthCache.Unlock()
	if ShouldRecordRouteHealthEvidence("seedance-2.5", ChannelLaneDirect, 9, true) {
		t.Fatal("precondition: an unloaded view must not know the route")
	}

	if err := refreshRouteHealthCacheFromCore(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !ShouldRecordRouteHealthEvidence("seedance-2.5", ChannelLaneDirect, 9, true) {
		t.Fatal("a success on a tripped route must be recorded after the refresh")
	}
	if err := RecordRouteHealthEvidence("seedance-2.5", ChannelLaneDirect, 9, true, "", at.Add(time.Minute)); err != nil {
		t.Fatalf("record success: %v", err)
	}
	var row RouteHealthState
	if err := coreDB.First(&row, "model_name = ? AND channel_id = ?", "seedance-2.5", 9).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.ConsecutiveFailures != 0 || row.State != RouteHealthStateHealthy {
		t.Fatalf("success did not clear the route: %+v", row)
	}
}

// Reaching a video provider proves the API answers and the key works. It ends
// a streak of reachability failures, but not a streak of failed generations:
// those only a delivered video can end.
func TestRouteReachabilityEvidenceClearsOnlyReachabilityStreaks(t *testing.T) {
	setupRouteHealthTestDB(t)
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	trip := func(channelID int, reason string) {
		for i := 0; i < RouteHealthFailureThreshold; i++ {
			if err := RecordRouteHealthEvidence("seedance-2.5", ChannelLaneDirect, channelID, false, reason, at.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatalf("trip route %d: %v", channelID, err)
			}
		}
	}
	failures := func(channelID int) int {
		var row RouteHealthState
		if err := coreDB.First(&row, "channel_id = ?", channelID).Error; err != nil {
			t.Fatalf("load route %d: %v", channelID, err)
		}
		return row.ConsecutiveFailures
	}
	trip(11, "upstream_overloaded")
	trip(12, RouteHealthGenerationReason("InternalServiceError"))

	for _, channelID := range []int{11, 12} {
		if err := RecordRouteReachabilityEvidence("seedance-2.5", ChannelLaneDirect, channelID, true, "", at.Add(time.Minute)); err != nil {
			t.Fatalf("reachable check on %d: %v", channelID, err)
		}
	}
	if got := failures(11); got != 0 {
		t.Fatalf("reachability streak after a reachable check = %d, want 0", got)
	}
	if got := failures(12); got != RouteHealthFailureThreshold {
		t.Fatalf("generation streak after a reachable check = %d, want it untouched", got)
	}

	if err := RecordRouteReachabilityEvidence("seedance-2.5", ChannelLaneDirect, 12, false, "auth", at.Add(2*time.Minute)); err != nil {
		t.Fatalf("unreachable check: %v", err)
	}
	if got := failures(12); got != RouteHealthFailureThreshold+1 {
		t.Fatalf("an unreachable check must count: failures = %d", got)
	}
}
