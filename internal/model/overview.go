package model

import (
	"context"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/lazu-ai/lazu/internal/common"
)

// The console overview reads the durable hourly rollups (never purged), the
// users' lifecycle columns and the session table, so every range it offers —
// including 90 days — answers from data that outlives request-log retention.

const (
	OverviewBucketHour = "hour"
	OverviewBucketDay  = "day"
	OverviewBucketWeek = "week"

	// A user is churned once they have gone this long without a request.
	OverviewChurnWindow = int64(7 * 24 * 3600)
)

type OverviewRequest struct {
	// ScopeUserID 0 is the whole platform; otherwise one account.
	ScopeUserID int
	Start       int64
	End         int64
	Bucket      string
	// TZOffset is the viewer's UTC offset in seconds; day and week buckets
	// start at the viewer's local midnight.
	TZOffset int64
	Now      int64
	// Platform views add growth, the new-user funnel and the client mix.
	Platform bool
}

type OverviewTotals struct {
	RequestCount         int64    `json:"request_count"`
	OutcomeCount         int64    `json:"outcome_count"`
	ErrorCount           int64    `json:"error_count"`
	SuccessRate          *float64 `json:"success_rate"`
	AmountMicrousd       int64    `json:"amount_microusd"`
	UpstreamCostMicrousd int64    `json:"upstream_cost_microusd,omitempty"`
	GrossMarginMicrousd  int64    `json:"gross_margin_microusd,omitempty"`
	CostSampleCount      int64    `json:"cost_sample_count,omitempty"`
	PromptTokens         int64    `json:"prompt_tokens"`
	CompletionTokens     int64    `json:"completion_tokens"`
	CachedTokens         int64    `json:"cached_tokens"`
	P95LatencyMs         *float64 `json:"p95_latency_ms"`
	P95TTFTMs            *float64 `json:"p95_ttft_ms"`
	ActiveUsers          *int64   `json:"active_users,omitempty"`
}

type OverviewBucket struct {
	Start                int64  `json:"start"`
	End                  int64  `json:"end"`
	Partial              bool   `json:"partial"`
	RequestCount         int64  `json:"request_count"`
	OutcomeCount         int64  `json:"outcome_count"`
	ErrorCount           int64  `json:"error_count"`
	AmountMicrousd       int64  `json:"amount_microusd"`
	UpstreamCostMicrousd int64  `json:"upstream_cost_microusd,omitempty"`
	GrossMarginMicrousd  int64  `json:"gross_margin_microusd,omitempty"`
	PromptTokens         int64  `json:"prompt_tokens"`
	CompletionTokens     int64  `json:"completion_tokens"`
	CachedTokens         int64  `json:"cached_tokens"`
	ActiveUsers          *int64 `json:"active_users,omitempty"`
	NewUsers             *int64 `json:"new_users,omitempty"`
	ReturnedUsers        *int64 `json:"returned_users,omitempty"`
	ChurnedUsers         *int64 `json:"churned_users,omitempty"`
}

type OverviewGrowth struct {
	NewUsers       int64 `json:"new_users"`
	Activated      int64 `json:"activated"`
	Paid           int64 `json:"paid"`
	ActiveUsers    int64 `json:"active_users"`
	ReturnedUsers  int64 `json:"returned_users"`
	ChurnedUsers   int64 `json:"churned_users"`
	TotalCustomers int64 `json:"total_customers"`
	// ChurnApplicable is false for ranges shorter than the churn window.
	ChurnApplicable bool `json:"churn_applicable"`
	// NewUsersPrevious lets the view compare against the previous range.
	NewUsersPrevious    int64 `json:"new_users_previous"`
	ActiveUsersPrevious int64 `json:"active_users_previous"`
	// Sources splits the range's new users by recorded signup attribution,
	// largest first. Users without a record are left out.
	Sources      []OverviewSignupSource `json:"sources,omitempty"`
	Campaigns    []OverviewSignupSource `json:"campaigns,omitempty"`
	LandingPages []OverviewSignupSource `json:"landing_pages,omitempty"`
	// Channels groups the same new users by acquisition channel
	// (AttributionChannel) and adds the range's tagged landings per channel,
	// so a channel that brings visits but no registrations still shows.
	Channels []OverviewSignupSource `json:"channels,omitempty"`
	// SelfReported groups the new users who answered "where did you hear
	// about Lazu" by their answer (model.SourceAnswers, or "skipped").
	SelfReported []OverviewSignupSource `json:"self_reported,omitempty"`
}

type OverviewSignupSource struct {
	Source    string `json:"source"`
	Users     int64  `json:"users"`
	Activated int64  `json:"activated"`
	Paid      int64  `json:"paid"`
	// Visits and Visitors are tagged landings in the range and their distinct
	// browsers, from a registered SignupVisitsCounter; only set on Channels.
	Visits   int64 `json:"visits,omitempty"`
	Visitors int64 `json:"visitors,omitempty"`
}

type OverviewClient struct {
	Client         string `json:"client"`
	Users          int64  `json:"users"`
	Sessions       int64  `json:"sessions"`
	RequestCount   int64  `json:"request_count"`
	Tokens         int64  `json:"tokens"`
	AmountMicrousd int64  `json:"amount_microusd"`
}

type OverviewSessionStats struct {
	Sessions       int64    `json:"sessions"`
	MedianAmount   *int64   `json:"median_amount_microusd"`
	MedianDuration *int64   `json:"median_duration_seconds"`
	SubagentShare  *float64 `json:"subagent_share"`
}

type OverviewTopup struct {
	Username       string `json:"username"`
	AmountMicrousd int64  `json:"amount_microusd"`
	CompletedAt    int64  `json:"completed_at"`
}

type OverviewTopups struct {
	Count          int64           `json:"count"`
	AmountMicrousd int64           `json:"amount_microusd"`
	Recent         []OverviewTopup `json:"recent"`
}

type Overview struct {
	Start    int64                 `json:"start"`
	End      int64                 `json:"end"`
	Bucket   string                `json:"bucket"`
	TZOffset int64                 `json:"tz_offset"`
	Totals   OverviewTotals        `json:"totals"`
	Previous OverviewTotals        `json:"previous"`
	Series   []OverviewBucket      `json:"series"`
	Growth   *OverviewGrowth       `json:"growth,omitempty"`
	Clients  []OverviewClient      `json:"clients,omitempty"`
	Sessions *OverviewSessionStats `json:"sessions,omitempty"`
	// Breakdown lists: models and Keys for everyone; users (with margin) and
	// top-ups for the platform. Operator-only columns are stripped for self.
	Models []DashboardUsageDimensionRow `json:"models"`
	Keys   []DashboardUsageDimensionRow `json:"keys,omitempty"`
	Users  []DashboardUsageDimensionRow `json:"users,omitempty"`
	Topups *OverviewTopups              `json:"topups,omitempty"`
}

func (r OverviewRequest) bucketStart(ts int64) int64 {
	switch r.Bucket {
	case OverviewBucketHour:
		return ts - ((ts%3600)+3600)%3600
	case OverviewBucketWeek:
		day := floorDiv(ts+r.TZOffset, 86400)
		weekday := ((day+3)%7 + 7) % 7 // 1970-01-01 was a Thursday; Monday = 0
		return (day-weekday)*86400 - r.TZOffset
	default:
		return floorDiv(ts+r.TZOffset, 86400)*86400 - r.TZOffset
	}
}

func (r OverviewRequest) bucketEnd(start int64) int64 {
	switch r.Bucket {
	case OverviewBucketHour:
		return start + 3600
	case OverviewBucketWeek:
		return start + 7*86400
	default:
		return start + 86400
	}
}

func floorDiv(a int64, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

type overviewUserHour struct {
	UserID     int   `gorm:"column:scope_user_id"`
	BucketHour int64 `gorm:"column:bucket_hour"`
}

// BuildOverview answers the console overview for one scope and range.
func BuildOverview(req OverviewRequest) (Overview, error) {
	return BuildOverviewContext(context.Background(), req)
}

func BuildOverviewContext(ctx context.Context, req OverviewRequest) (Overview, error) {
	if req.Now <= 0 {
		req.Now = time.Now().Unix()
	}
	if req.End <= req.Start {
		req.End = req.Now
		req.Start = req.End - 86400
	}
	switch req.Bucket {
	case OverviewBucketHour, OverviewBucketDay, OverviewBucketWeek:
	default:
		req.Bucket = OverviewBucketDay
	}
	out := Overview{Start: req.Start, End: req.End, Bucket: req.Bucket, TZOffset: req.TZOffset}
	length := req.End - req.Start
	prevStart := req.Start - length
	scope := dashboardRollupScopeGlobal
	if !req.Platform {
		scope = req.ScopeUserID
	}

	var rows []DashboardUsageHourlyTotal
	if err := TelemetryDB().WithContext(ctx).Where("scope_user_id = ? AND bucket_hour >= ? AND bucket_hour < ?",
		scope, dashboardRollupHour(prevStart), req.End).Find(&rows).Error; err != nil {
		return out, err
	}

	// Buckets for the current range.
	index := map[int64]int{}
	for start := req.bucketStart(req.Start); start < req.End; start = req.bucketEnd(start) {
		index[start] = len(out.Series)
		end := req.bucketEnd(start)
		out.Series = append(out.Series, OverviewBucket{Start: start, End: end, Partial: end > req.Now})
	}

	current := newOverviewAccumulator()
	previous := newOverviewAccumulator()
	for _, row := range rows {
		switch {
		case row.BucketHour >= req.Start && row.BucketHour < req.End:
			current.add(row)
			if i, ok := index[req.bucketStart(row.BucketHour)]; ok {
				b := &out.Series[i]
				b.RequestCount += row.RequestCount
				b.OutcomeCount += row.OutcomeCount
				b.ErrorCount += row.ErrorCount
				b.AmountMicrousd += row.AmountMicrousd
				b.UpstreamCostMicrousd += row.UpstreamMicrousd
				b.GrossMarginMicrousd += row.GrossMicrousd
				b.PromptTokens += row.PromptTokens
				b.CompletionTokens += row.CompletionTokens
				b.CachedTokens += row.CachedInputTokens
			}
		case row.BucketHour >= prevStart && row.BucketHour < req.Start:
			previous.add(row)
		}
	}
	out.Totals = current.result()
	out.Previous = previous.result()
	if !req.Platform {
		for i := range out.Series {
			out.Series[i].UpstreamCostMicrousd, out.Series[i].GrossMarginMicrousd = 0, 0
		}
		out.Totals.UpstreamCostMicrousd, out.Totals.GrossMarginMicrousd, out.Totals.CostSampleCount = 0, 0, 0
		out.Previous.UpstreamCostMicrousd, out.Previous.GrossMarginMicrousd, out.Previous.CostSampleCount = 0, 0, 0
	}

	if req.Platform {
		if err := buildOverviewGrowthContext(ctx, req, &out); err != nil {
			return out, err
		}
		clients, err := buildOverviewClientsContext(ctx, req)
		if err != nil {
			return out, err
		}
		out.Clients = clients
	}
	stats, err := buildOverviewSessionStatsContext(ctx, req)
	if err != nil {
		return out, err
	}
	out.Sessions = stats

	startBucket, endBucket := dashboardRollupHour(req.Start), req.End
	if out.Models, err = dashboardRollupDimensionRowsContext(ctx, scope, dashboardDimensionModel, startBucket, endBucket, 50); err != nil {
		return out, err
	}
	if req.Platform {
		if out.Users, err = dashboardRollupDimensionRowsContext(ctx, scope, dashboardDimensionUser, startBucket, endBucket, 20); err != nil {
			return out, err
		}
		topups, err := buildOverviewTopupsContext(ctx, req)
		if err != nil {
			return out, err
		}
		out.Topups = topups
	} else {
		if out.Keys, err = dashboardRollupDimensionRowsContext(ctx, scope, dashboardDimensionKey, startBucket, endBucket, 20); err != nil {
			return out, err
		}
		for _, rows := range [][]DashboardUsageDimensionRow{out.Models, out.Keys} {
			for i := range rows {
				rows[i].UpstreamMicrousd, rows[i].GrossMicrousd, rows[i].GrossMarginRatio, rows[i].CostSampleCount = 0, 0, 0, 0
			}
		}
	}
	if out.Models == nil {
		out.Models = []DashboardUsageDimensionRow{}
	}
	return out, nil
}

func buildOverviewTopupsContext(ctx context.Context, req OverviewRequest) (*OverviewTopups, error) {
	out := &OverviewTopups{Recent: []OverviewTopup{}}
	base := CoreDB().WithContext(ctx).Model(&TopUp{}).Where("top_ups.status = ? AND top_ups.complete_time >= ? AND top_ups.complete_time < ?", "success", req.Start, req.End)
	var sum struct {
		Count  int64
		Amount int64
	}
	if err := base.Session(&gorm.Session{}).Select("COUNT(*) AS count, COALESCE(SUM(credited_microusd), 0) AS amount").Scan(&sum).Error; err != nil {
		return nil, err
	}
	out.Count, out.AmountMicrousd = sum.Count, sum.Amount
	var rows []struct {
		Username     string
		Amount       int64
		CompleteTime int64
	}
	if err := base.Session(&gorm.Session{}).
		Select("users.username AS username, top_ups.credited_microusd AS amount, top_ups.complete_time AS complete_time").
		Joins("LEFT JOIN users ON users.id = top_ups.user_id").
		Order("top_ups.complete_time DESC").Limit(6).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out.Recent = append(out.Recent, OverviewTopup{Username: row.Username, AmountMicrousd: row.Amount, CompletedAt: row.CompleteTime})
	}
	return out, nil
}

type overviewAccumulator struct {
	totals  OverviewTotals
	latency []int64
	ttft    []int64
	hasRows bool
}

func newOverviewAccumulator() *overviewAccumulator {
	return &overviewAccumulator{latency: newDashboardLatencyBuckets(), ttft: newDashboardTTFTBuckets()}
}

func (a *overviewAccumulator) add(row DashboardUsageHourlyTotal) {
	a.hasRows = true
	a.totals.RequestCount += row.RequestCount
	a.totals.OutcomeCount += row.OutcomeCount
	a.totals.ErrorCount += row.ErrorCount
	a.totals.AmountMicrousd += row.AmountMicrousd
	a.totals.UpstreamCostMicrousd += row.UpstreamMicrousd
	a.totals.GrossMarginMicrousd += row.GrossMicrousd
	a.totals.CostSampleCount += row.CostSampleCount
	a.totals.PromptTokens += row.PromptTokens
	a.totals.CompletionTokens += row.CompletionTokens
	a.totals.CachedTokens += row.CachedInputTokens
	a.latency = mergeDashboardLatencyBuckets(a.latency, row.LatencyBuckets)
	a.ttft = mergeDashboardTTFTBuckets(a.ttft, row.TTFTBuckets)
}

func (a *overviewAccumulator) result() OverviewTotals {
	out := a.totals
	if out.OutcomeCount > 0 {
		rate := float64(out.OutcomeCount-out.ErrorCount) / float64(out.OutcomeCount)
		if rate < 0 {
			rate = 0
		}
		out.SuccessRate = &rate
		p95 := dashboardLatencyP95FromBuckets(a.latency, out.OutcomeCount)
		if p95 > 0 {
			out.P95LatencyMs = &p95
		}
	}
	_, out.P95TTFTMs = dashboardTTFTP95FromBuckets(a.ttft)
	return out
}

func int64Ptr(value int64) *int64 { return &value }

// buildOverviewGrowth counts registrations, activation, first purchase,
// activity, churn and return for the platform.
func buildOverviewGrowth(req OverviewRequest, out *Overview) error {
	return buildOverviewGrowthContext(context.Background(), req, out)
}

func buildOverviewGrowthContext(ctx context.Context, req OverviewRequest, out *Overview) error {
	length := req.End - req.Start
	growth := &OverviewGrowth{ChurnApplicable: length >= OverviewChurnWindow}

	// Per-user activity from the durable per-user hourly rollups, reaching
	// back one churn window further so a return can be recognised.
	seedIDs, err := operatedAccountIDs(CoreDB().WithContext(ctx))
	if err != nil {
		return err
	}
	seeded := make(map[int]bool, len(seedIDs))
	for _, id := range seedIDs {
		seeded[id] = true
	}
	var hours []overviewUserHour
	if err := TelemetryDB().WithContext(ctx).Model(&DashboardUsageHourlyTotal{}).Select("scope_user_id, bucket_hour").
		Where("scope_user_id > 0 AND request_count > 0 AND bucket_hour >= ? AND bucket_hour < ?",
			dashboardRollupHour(req.Start-length-OverviewChurnWindow), req.End).
		Order("scope_user_id, bucket_hour").Scan(&hours).Error; err != nil {
		return err
	}
	type userSet map[int]struct{}
	perBucket := make([]userSet, len(out.Series))
	returned := make([]userSet, len(out.Series))
	for i := range perBucket {
		perBucket[i], returned[i] = userSet{}, userSet{}
	}
	index := map[int64]int{}
	for i, b := range out.Series {
		index[b.Start] = i
	}
	activeNow, activeBefore, returnedAll := userSet{}, userSet{}, userSet{}
	var lastUser int
	var lastHour int64 = -1
	for _, h := range hours {
		if seeded[h.UserID] {
			continue
		}
		if h.UserID != lastUser {
			lastUser, lastHour = h.UserID, -1
		}
		inRange := h.BucketHour >= req.Start && h.BucketHour < req.End
		if inRange {
			activeNow[h.UserID] = struct{}{}
			if i, ok := index[req.bucketStart(h.BucketHour)]; ok {
				perBucket[i][h.UserID] = struct{}{}
				if lastHour >= 0 && h.BucketHour-lastHour > OverviewChurnWindow {
					returned[i][h.UserID] = struct{}{}
					returnedAll[h.UserID] = struct{}{}
				}
			}
		} else if h.BucketHour >= req.Start-length && h.BucketHour < req.Start {
			activeBefore[h.UserID] = struct{}{}
		}
		lastHour = h.BucketHour
	}
	growth.ActiveUsers = int64(len(activeNow))
	growth.ActiveUsersPrevious = int64(len(activeBefore))
	growth.ReturnedUsers = int64(len(returnedAll))

	var users []struct {
		Id              int
		CreatedAt       int64
		CreatedAtSource string
		FirstRequestAt  int64
		FirstTopupAt    int64
		LastRequestAt   int64
	}
	// Accounts the operator runs itself are not customers.
	if err := excludeOperatedAccounts(CoreDB().WithContext(ctx).Model(&User{}).
		Select("id", "created_at", "created_at_source", "first_request_at", "first_topup_at", "last_request_at").
		Where("role = ?", common.RoleCommonUser)).
		Scan(&users).Error; err != nil {
		return err
	}
	growth.TotalCustomers = int64(len(users))
	newPer := make([]int64, len(out.Series))
	churnPer := make([]int64, len(out.Series))
	newcomers := map[int]overviewNewcomer{}
	for _, u := range users {
		reliable := u.CreatedAtSource != UserCreatedAtSourceEarliestActivity &&
			u.CreatedAtSource != UserCreatedAtSourceInterpolated && u.CreatedAt > 0
		if reliable && u.CreatedAt >= req.Start && u.CreatedAt < req.End {
			growth.NewUsers++
			newcomers[u.Id] = overviewNewcomer{activated: u.FirstRequestAt > 0, paid: u.FirstTopupAt > 0}
			if u.FirstRequestAt > 0 {
				growth.Activated++
			}
			if u.FirstTopupAt > 0 {
				growth.Paid++
			}
			if i, ok := index[req.bucketStart(u.CreatedAt)]; ok {
				newPer[i]++
			}
		}
		if reliable && u.CreatedAt >= req.Start-length && u.CreatedAt < req.Start {
			growth.NewUsersPrevious++
		}
		if u.LastRequestAt > 0 {
			churnAt := u.LastRequestAt + OverviewChurnWindow
			if churnAt >= req.Start && churnAt < req.End && churnAt <= req.Now {
				growth.ChurnedUsers++
				if i, ok := index[req.bucketStart(churnAt)]; ok {
					churnPer[i]++
				}
			}
		}
	}
	for i := range out.Series {
		out.Series[i].ActiveUsers = int64Ptr(int64(len(perBucket[i])))
		out.Series[i].NewUsers = int64Ptr(newPer[i])
		out.Series[i].ReturnedUsers = int64Ptr(int64(len(returned[i])))
		if growth.ChurnApplicable {
			out.Series[i].ChurnedUsers = int64Ptr(churnPer[i])
		}
	}
	if !growth.ChurnApplicable {
		growth.ChurnedUsers = 0
	}
	sources, err := overviewSignupSourcesContext(ctx, newcomers)
	if err != nil {
		return err
	}
	growth.Sources = sources["source"]
	growth.Campaigns = sources["campaign"]
	growth.LandingPages = sources["landing_path"]
	growth.SelfReported = sources["self_reported"]
	growth.Channels, err = overviewChannelsContext(ctx, sources["channel"], req.Start, req.End)
	if err != nil {
		return err
	}
	active := growth.ActiveUsers
	out.Totals.ActiveUsers = &active
	previousActive := growth.ActiveUsersPrevious
	out.Previous.ActiveUsers = &previousActive
	out.Growth = growth
	return nil
}

type overviewNewcomer struct{ activated, paid bool }

// overviewChannels merges the range's tagged landings into the per-channel
// registration groups. Landings are recorded only when the link carried a
// utm parameter or a referral code, so search and direct traffic without
// tags appear through registrations alone.
func overviewChannels(signups []OverviewSignupSource, start, end int64) ([]OverviewSignupSource, error) {
	return overviewChannelsContext(context.Background(), signups, start, end)
}

func overviewChannelsContext(ctx context.Context, signups []OverviewSignupSource, start, end int64) ([]OverviewSignupSource, error) {
	byChannel := map[string]*OverviewSignupSource{}
	order := []string{}
	for _, row := range signups {
		entry := row
		byChannel[row.Source] = &entry
		order = append(order, row.Source)
	}
	if CoreDB() != nil && extensions.signupVisits != nil {
		visits, err := extensions.signupVisits(ctx, start, end)
		if err != nil {
			return nil, err
		}
		for _, row := range visits {
			entry := byChannel[row.Source]
			if entry == nil {
				entry = &OverviewSignupSource{Source: row.Source}
				byChannel[row.Source] = entry
				order = append(order, row.Source)
			}
			entry.Visits += row.Visits
			entry.Visitors += row.Visitors
		}
	}
	out := make([]OverviewSignupSource, 0, len(order))
	for _, channel := range order {
		out = append(out, *byChannel[channel])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Users != out[j].Users {
			return out[i].Users > out[j].Users
		}
		if out[i].Visits != out[j].Visits {
			return out[i].Visits > out[j].Visits
		}
		return out[i].Source < out[j].Source
	})
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// overviewSignupSources groups new users by where they came from: the utm
// source when the link carried one, else the referring site, else "invite"
// for an invite code, else "" for a direct visit.
func overviewSignupSources(newcomers map[int]overviewNewcomer) (map[string][]OverviewSignupSource, error) {
	return overviewSignupSourcesContext(context.Background(), newcomers)
}

func overviewSignupSourcesContext(ctx context.Context, newcomers map[int]overviewNewcomer) (map[string][]OverviewSignupSource, error) {
	if len(newcomers) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(newcomers))
	for id := range newcomers {
		ids = append(ids, id)
	}
	var rows []SignupAttribution
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var chunk []SignupAttribution
		if err := CoreDB().WithContext(ctx).Select("user_id", "source", "referrer_host", "invite_code", "referral_code", "campaign", "landing_path", "self_reported").
			Where("user_id IN ?", ids[start:end]).Find(&chunk).Error; err != nil {
			return nil, err
		}
		rows = append(rows, chunk...)
	}
	out := map[string][]OverviewSignupSource{}
	for _, dimension := range []string{"source", "campaign", "landing_path", "channel", "self_reported"} {
		grouped := map[string]*OverviewSignupSource{}
		for _, row := range rows {
			key := strings.ToLower(strings.TrimSpace(row.Source))
			if key == "" {
				key = strings.TrimPrefix(strings.ToLower(row.ReferrerHost), "www.")
			}
			if key == "" && row.InviteCode != "" {
				key = "invite"
			}
			if dimension == "campaign" {
				key = strings.TrimSpace(row.Campaign)
			}
			if dimension == "landing_path" {
				key = strings.TrimSpace(row.LandingPath)
			}
			if dimension == "channel" {
				key = AttributionChannel(row.Source, row.ReferrerHost, row.ReferralCode, row.InviteCode)
			}
			if dimension == "self_reported" {
				if row.SelfReported == "" {
					continue
				}
				key = row.SelfReported
			}
			entry := grouped[key]
			if entry == nil {
				entry = &OverviewSignupSource{Source: key}
				grouped[key] = entry
			}
			user := newcomers[row.UserID]
			entry.Users++
			if user.activated {
				entry.Activated++
			}
			if user.paid {
				entry.Paid++
			}
		}
		for _, entry := range grouped {
			out[dimension] = append(out[dimension], *entry)
		}
		sort.Slice(out[dimension], func(i, j int) bool {
			a, b := out[dimension][i], out[dimension][j]
			if a.Users != b.Users {
				return a.Users > b.Users
			}
			return a.Source < b.Source
		})
	}
	return out, nil
}

type overviewClientRow struct {
	Client string
	Users  int64
	Reqs   int64
	Tokens int64
	Amount int64
}

func buildOverviewClientsContext(ctx context.Context, req OverviewRequest) ([]OverviewClient, error) {
	if TelemetryDB() == nil {
		return nil, nil
	}
	var rows []overviewClientRow
	if usageSlicesAvailable() {
		var err error
		if rows, err = overviewClientRowsFromSlicesContext(ctx, req.Start, req.End); err != nil {
			return nil, err
		}
	} else if err := TelemetryDB().WithContext(ctx).Model(&Log{}).
		Select("COALESCE(NULLIF(client_framework, ''), 'unknown') AS client, COUNT(DISTINCT user_id) AS users, COUNT(*) AS reqs, "+
			"COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(SUM(amount_microusd), 0) AS amount").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, req.Start, req.End).
		Group("COALESCE(NULLIF(client_framework, ''), 'unknown')").Scan(&rows).Error; err != nil {
		return nil, err
	}
	var sessions []struct {
		Client string
		Count  int64
	}
	if err := TelemetryDB().WithContext(ctx).Model(&AgentSession{}).Select("COALESCE(NULLIF(client, ''), 'unknown') AS client, COUNT(*) AS count").
		Where("last_at >= ? AND started_at < ? AND source <> ?", req.Start, req.End, "token").
		Group("COALESCE(NULLIF(client, ''), 'unknown')").Scan(&sessions).Error; err != nil {
		return nil, err
	}
	sessionsBy := map[string]int64{}
	for _, s := range sessions {
		sessionsBy[s.Client] = s.Count
	}
	out := make([]OverviewClient, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverviewClient{Client: row.Client, Users: row.Users, Sessions: sessionsBy[row.Client],
			RequestCount: row.Reqs, Tokens: row.Tokens, AmountMicrousd: row.Amount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestCount > out[j].RequestCount })
	return out, nil
}

func buildOverviewSessionStatsContext(ctx context.Context, req OverviewRequest) (*OverviewSessionStats, error) {
	query := TelemetryDB().WithContext(ctx).Model(&AgentSession{}).Select("amount_microusd", "started_at", "last_at", "thread_count").
		Where("last_at >= ? AND started_at < ? AND source <> ?", req.Start, req.End, "token")
	if !req.Platform {
		query = query.Where("user_id = ?", req.ScopeUserID)
	}
	var rows []struct {
		AmountMicrousd int64
		StartedAt      int64
		LastAt         int64
		ThreadCount    int64
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	stats := &OverviewSessionStats{Sessions: int64(len(rows))}
	if len(rows) == 0 {
		return stats, nil
	}
	amounts := make([]int64, len(rows))
	durations := make([]int64, len(rows))
	withSubagents := 0
	for i, row := range rows {
		amounts[i] = row.AmountMicrousd
		durations[i] = row.LastAt - row.StartedAt
		if row.ThreadCount > 0 {
			withSubagents++
		}
	}
	medianAmount, medianDuration := median(amounts), median(durations)
	share := float64(withSubagents) / float64(len(rows))
	stats.MedianAmount, stats.MedianDuration, stats.SubagentShare = &medianAmount, &medianDuration, &share
	return stats, nil
}

func median(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
