package model

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RouteObservationDays is how long a new route is watched before it may carry
// traffic (source_checks.observation_days in config.yaml). A watch long enough
// to cover a weekend and every hour of the day catches what pooled resellers
// show only at certain times, which a single afternoon hides.
func RouteObservationDays() int {
	return common.AppConfig.SourceChecks.ObservationDays
}

// RouteObservation marks one (lane, model, channel) route as new supply that
// is being watched before it goes live.
//
// The route itself sits in inventory while it is watched: it is probed like
// every other route but the router never picks it. The observation is kept in
// its own table rather than as a column on abilities because abilities are
// rebuilt on every channel edit; this row survives that, and it is only
// "active" while the ability it points at is still inventory.
type RouteObservation struct {
	Lane      string    `json:"lane" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	ModelName string    `json:"model_name" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int       `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	StartedAt time.Time `json:"started_at" gorm:"index"`
}

func (RouteObservation) TableName() string {
	return "route_observation"
}

// The three things an ordinary liveness probe cannot see, each checked once a
// day while a route is watched.
const (
	// ObservationCheckBilling compares the input tokens the upstream bills
	// against what the prompt actually holds. A reseller that prepends its
	// own system prompt bills it to us on every request.
	ObservationCheckBilling = "billing"
	// ObservationCheckLongContext sends a long input and asks for a word from
	// its very start, which catches pools that quietly cut the context.
	ObservationCheckLongContext = "long_context"
	// ObservationCheckTools sends one function and requires a call to it.
	ObservationCheckTools = "tools"
)

// ObservationCheckKinds lists the checks in the order the console shows them.
var ObservationCheckKinds = []string{
	ObservationCheckBilling, ObservationCheckLongContext, ObservationCheckTools,
}

// RouteObservationCheck is one run of one check against a watched route.
type RouteObservationCheck struct {
	Id        int64     `json:"id" gorm:"primaryKey"`
	Lane      string    `json:"lane" gorm:"type:varchar(64);index:idx_route_observation_check_cell,priority:1"`
	ModelName string    `json:"model_name" gorm:"type:varchar(255);index:idx_route_observation_check_cell,priority:2"`
	ChannelId int       `json:"channel_id" gorm:"index:idx_route_observation_check_cell,priority:3"`
	Kind      string    `json:"kind" gorm:"type:varchar(16);index"`
	CheckedAt time.Time `json:"checked_at" gorm:"index"`
	Passed    bool      `json:"passed"`
	// NotApplicable is a check the route cannot be asked: a model that does
	// not declare tool support, or one that does not produce text at all.
	// It counts as passed, and the console says why.
	NotApplicable bool `json:"not_applicable"`
	// Skipped is a run that never reached the upstream — a local error, or a
	// budget that ran out. It is kept so the scheduler backs off, and it is
	// not evidence either way.
	Skipped bool `json:"skipped"`
	// Value is the check's figure: the billed-to-actual ratio for billing, the
	// billed input tokens for long context.
	Value float64 `json:"value"`
	// Detail is the short reason a run failed, shown on hover.
	Detail  string  `json:"detail,omitempty" gorm:"type:varchar(255)"`
	CostUSD float64 `json:"cost_usd" gorm:"type:decimal(12,8)"`
}

func (RouteObservationCheck) TableName() string {
	return "route_observation_check"
}

// RouteObservationCell names one route.
type RouteObservationCell struct {
	Lane      string
	ModelName string
	ChannelId int
}

// Key is the same key the health grid uses for a route.
func (c RouteObservationCell) Key() string {
	return ChannelHealthCellKey(c.ModelName, c.Lane, c.ChannelId)
}

// StartRouteObservations puts routes under observation from `at`. A route
// that is already watched keeps its original start.
func StartRouteObservations(db *gorm.DB, cells []RouteObservationCell, at time.Time) error {
	if db == nil {
		db = CoreDB()
	}
	if db == nil || len(cells) == 0 {
		return nil
	}
	rows := make([]RouteObservation, 0, len(cells))
	for _, cell := range cells {
		name := strings.TrimSpace(cell.ModelName)
		if name == "" || cell.ChannelId <= 0 {
			continue
		}
		rows = append(rows, RouteObservation{
			Lane: NormalizeChannelLane(cell.Lane), ModelName: name,
			ChannelId: cell.ChannelId, StartedAt: at.UTC(),
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 50).Error
}

// EndRouteObservations drops the observation of the given routes, which is
// what happens when they go live or are removed.
func EndRouteObservations(db *gorm.DB, channelID int, lane string, models []string) error {
	if db == nil {
		db = CoreDB()
	}
	if db == nil || channelID <= 0 || len(models) == 0 {
		return nil
	}
	if !db.Migrator().HasTable(&RouteObservation{}) {
		return nil
	}
	return db.Where("channel_id = ? AND lane = ? AND model_name IN ?",
		channelID, NormalizeChannelLane(lane), models).
		Delete(&RouteObservation{}).Error
}

// EndChannelRouteObservations drops every observation of one channel. The
// ones whose route no longer exists would otherwise linger forever.
func EndChannelRouteObservations(db *gorm.DB, channelID int, keep map[string]bool) error {
	if db == nil {
		db = CoreDB()
	}
	if db == nil || channelID <= 0 || !db.Migrator().HasTable(&RouteObservation{}) {
		return nil
	}
	var rows []RouteObservation
	if err := db.Where("channel_id = ?", channelID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if keep[row.Lane+"|"+row.ModelName] {
			continue
		}
		if err := db.Where("channel_id = ? AND lane = ? AND model_name = ?",
			row.ChannelId, row.Lane, row.ModelName).
			Delete(&RouteObservation{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// LoadActiveRouteObservations returns the routes being watched now: an
// observation whose route still exists and is still inventory. A route that
// was moved out of inventory by any path is live, whatever its observation
// row says.
func LoadActiveRouteObservations(channelIDs []int) ([]RouteObservation, error) {
	result := []RouteObservation{}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&RouteObservation{}) {
		return result, nil
	}
	query := CoreDB().Table("route_observation AS o").
		Select("o.lane, o.model_name, o.channel_id, o.started_at").
		Joins("JOIN abilities ON abilities.channel_id = o.channel_id AND abilities.model = o.model_name AND "+
			abilityGroupColumn()+" = o.lane AND abilities.role = ?", AbilityRoleInventory)
	if channelIDs != nil {
		if len(channelIDs) == 0 {
			return result, nil
		}
		query = query.Where("o.channel_id IN ?", channelIDs)
	}
	type row struct {
		Lane      string
		ModelName string
		ChannelId int
		StartedAt ChannelHealthTime
	}
	var rows []row
	if err := query.Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, item := range rows {
		if !item.StartedAt.Valid {
			continue
		}
		result = append(result, RouteObservation{
			Lane: NormalizeChannelLane(item.Lane), ModelName: item.ModelName,
			ChannelId: item.ChannelId, StartedAt: item.StartedAt.Time,
		})
	}
	return result, nil
}

// RecordRouteObservationCheck stores one check run. Like a probe, losing one
// only costs a day's evidence, so callers log rather than fail on error.
func RecordRouteObservationCheck(check RouteObservationCheck) error {
	if CoreDB() == nil {
		return nil
	}
	check.Lane = NormalizeChannelLane(check.Lane)
	if check.CheckedAt.IsZero() {
		check.CheckedAt = time.Now().UTC()
	}
	if len(check.Detail) > 255 {
		check.Detail = check.Detail[:255]
		for len(check.Detail) > 0 && !utf8.ValidString(check.Detail) {
			check.Detail = check.Detail[:len(check.Detail)-1]
		}
	}
	return CoreDB().Create(&check).Error
}

// LoadRouteObservationChecks returns every check run on the given channels
// since `since`, oldest first.
func LoadRouteObservationChecks(channelIDs []int, since time.Time) ([]RouteObservationCheck, error) {
	result := []RouteObservationCheck{}
	if CoreDB() == nil || len(channelIDs) == 0 || !CoreDB().Migrator().HasTable(&RouteObservationCheck{}) {
		return result, nil
	}
	err := CoreDB().Where("channel_id IN ? AND checked_at >= ?", channelIDs, since).
		Order("checked_at ASC, id ASC").
		Find(&result).Error
	return result, err
}

// RouteProbeTally is the probe record of one route since some instant.
type RouteProbeTally struct {
	Lane         string
	ModelName    string
	ChannelId    int
	Samples      int64
	Failures     int64
	AvgLatencyMs float64
}

// LoadRouteProbeTallies counts the non-skipped probes of the given channels
// per route since `since`. A watched route carries no traffic, so its probes
// are the whole of its record.
func LoadRouteProbeTallies(channelIDs []int, since time.Time) (map[string]RouteProbeTally, error) {
	result := map[string]RouteProbeTally{}
	if TelemetryDB() == nil || len(channelIDs) == 0 || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return result, nil
	}
	var rows []RouteProbeTally
	if err := TelemetryDB().Model(&ChannelProbeResult{}).
		Select("lane, model_name, channel_id, COUNT(*) AS samples, "+
			"SUM(CASE WHEN success THEN 0 ELSE 1 END) AS failures, "+
			"COALESCE(AVG(latency_ms), 0) AS avg_latency_ms").
		Where("channel_id IN ? AND probed_at >= ? AND skipped = ?", channelIDs, since, false).
		Group("lane, model_name, channel_id").
		Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		row.Lane = NormalizeChannelLane(row.Lane)
		result[ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = row
	}
	return result, nil
}

// RouteProbeBucket is one slice of a route's probe record.
type RouteProbeBucket struct {
	Lane        string
	ModelName   string
	ChannelId   int
	BucketIndex int
	Samples     int64
	Failures    int64
}

// LoadRouteProbeBuckets slices the probes of the given channels onto the same
// clock-aligned grid the model-ops bars use.
func LoadRouteProbeBuckets(channelIDs []int, since time.Time, bucketSeconds int64) ([]RouteProbeBucket, error) {
	result := []RouteProbeBucket{}
	if TelemetryDB() == nil || len(channelIDs) == 0 || bucketSeconds <= 0 || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return result, nil
	}
	err := TelemetryDB().Model(&ChannelProbeResult{}).
		Select("lane, model_name, channel_id, "+RoutingOutcomeBucketIndexExpr("probed_at")+" AS bucket_index, "+
			"COUNT(*) AS samples, SUM(CASE WHEN success THEN 0 ELSE 1 END) AS failures",
			since.Unix(), bucketSeconds).
		Where("channel_id IN ? AND probed_at >= ? AND skipped = ?", channelIDs, since, false).
		Group("lane, model_name, channel_id, bucket_index").
		Scan(&result).Error
	for index := range result {
		result[index].Lane = NormalizeChannelLane(result[index].Lane)
	}
	return result, err
}

// LoadRouteLastProbeErrors returns, per route, the error of its most recent
// failed probe since `since`. A watched route's failures need a reason the
// operator can read, and the probe row is the only place that keeps one.
func LoadRouteLastProbeErrors(channelIDs []int, since time.Time) (map[string]string, error) {
	result := map[string]string{}
	if TelemetryDB() == nil || len(channelIDs) == 0 || !TelemetryDB().Migrator().HasTable(&ChannelProbeResult{}) {
		return result, nil
	}
	var rows []ChannelProbeResult
	if err := TelemetryDB().Select("lane, model_name, channel_id, error_message, probed_at").
		Where("channel_id IN ? AND probed_at >= ? AND skipped = ? AND success = ?", channelIDs, since, false, false).
		Order("probed_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		key := ChannelHealthCellKey(row.ModelName, NormalizeChannelLane(row.Lane), row.ChannelId)
		if _, seen := result[key]; !seen {
			result[key] = row.ErrorMessage
		}
	}
	return result, nil
}
