package model

import (
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// DigestAmounts are one period's totals for a usage digest. Money is microUSD.
type DigestAmounts struct {
	Spend    int64 `json:"spend"`
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
	Tokens   int64 `json:"tokens"`
	Cache    int64 `json:"cache_tokens"`
	// Official is the vendor list price of the requests that carry a fair
	// one; Compared is what those same requests were charged.
	Official int64 `json:"official"`
	Compared int64 `json:"compared"`
	Sessions int64 `json:"sessions"`
}

type DigestDay struct {
	Date   string `json:"date"`
	Spend  int64  `json:"spend"`
	Tokens int64  `json:"tokens"`
}

type DigestShare struct {
	Key       string `json:"key"`
	Spend     int64  `json:"spend"`
	PrevSpend int64  `json:"prev_spend"`
	Requests  int64  `json:"requests"`
	Sessions  int64  `json:"sessions"`
}

// DigestUsage is everything a digest reads from the logs for one payer.
type DigestUsage struct {
	Current  DigestAmounts `json:"current"`
	Previous DigestAmounts `json:"previous"`
	Days     []DigestDay   `json:"days"`
	// PrevDays is the previous period by day, aligned to Days (weekly only).
	PrevDays []int64       `json:"prev_days,omitempty"`
	Models   []DigestShare `json:"models"`
	Sources  []DigestShare `json:"sources"`
	Wallet   int64         `json:"wallet"`
	// CurrentMissing / PreviousMissing: the period starts before the rollup
	// holds complete data, so its figures would be short.
	CurrentMissing  bool `json:"current_missing,omitempty"`
	PreviousMissing bool `json:"previous_missing,omitempty"`
}

// DigestPeriod is a half-open window [Start, End) in the reader's zone.
type DigestPeriod struct {
	Start time.Time
	End   time.Time
}

// CollectDigestUsage reads one payer's spend for a period and the one before
// it from the hourly rollup. "Payer" is whoever's wallet paid: their own
// requests plus any team member's requests billed to them.
func CollectDigestUsage(userID int, current DigestPeriod, previous DigestPeriod, alignPrevDays bool) (*DigestUsage, error) {
	loc := current.Start.Location()
	out := &DigestUsage{}
	dayIndex := map[string]int{}
	for day := current.Start; day.Before(current.End); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		dayIndex[key] = len(out.Days)
		out.Days = append(out.Days, DigestDay{Date: key})
	}
	if alignPrevDays {
		out.PrevDays = make([]int64, len(out.Days))
	}
	models := map[string]*DigestShare{}
	sources := map[string]*DigestShare{}
	share := func(set map[string]*DigestShare, key string) *DigestShare {
		item := set[key]
		if item == nil {
			item = &DigestShare{Key: key}
			set[key] = item
		}
		return item
	}

	// Complete data begins where the rollup's backfill began.
	since := DigestRollupSince()
	out.CurrentMissing = since == 0 || current.Start.Unix() < since
	out.PreviousMissing = since == 0 || previous.Start.Unix() < since

	var hours []DigestUsageHour
	if err := TelemetryDB().Where("payer_user_id = ? AND hour_start >= ? AND hour_start < ?", userID, previous.Start.Unix(), current.End.Unix()).
		Find(&hours).Error; err != nil {
		return nil, err
	}
	for _, h := range hours {
		isCurrent := h.HourStart >= current.Start.Unix()
		amounts := &out.Previous
		if isCurrent {
			amounts = &out.Current
		}
		amounts.Requests += h.Requests
		amounts.Errors += h.Errors
		amounts.Spend += h.Spend
		amounts.Tokens += h.Tokens
		amounts.Cache += h.Cache
		amounts.Official += h.Official
		amounts.Compared += h.Compared
		if h.Requests == 0 {
			continue
		}
		at := time.Unix(h.HourStart, 0).In(loc)
		if isCurrent {
			if i, ok := dayIndex[at.Format("2006-01-02")]; ok {
				out.Days[i].Spend += h.Spend
				out.Days[i].Tokens += h.Tokens
			}
			share(models, h.ModelName).Spend += h.Spend
			share(models, h.ModelName).Requests += h.Requests
			share(sources, h.Source).Spend += h.Spend
			share(sources, h.Source).Requests += h.Requests
		} else {
			if alignPrevDays {
				offset := int(at.Sub(previous.Start).Hours() / 24)
				if offset >= 0 && offset < len(out.PrevDays) {
					out.PrevDays[offset] += h.Spend
				}
			}
			share(models, h.ModelName).PrevSpend += h.Spend
			share(sources, h.Source).PrevSpend += h.Spend
		}
	}

	// Agent sessions are counted by when they started, per client.
	type sessionCount struct {
		Client  string
		Current int64
		Prev    int64
	}
	var counts []sessionCount
	if err := TelemetryDB().Model(&AgentSession{}).
		Select("client, SUM(CASE WHEN started_at >= ? THEN 1 ELSE 0 END) AS current, SUM(CASE WHEN started_at < ? THEN 1 ELSE 0 END) AS prev", current.Start.Unix(), current.Start.Unix()).
		Where("user_id = ? AND source <> ? AND started_at >= ? AND started_at < ?", userID, "token", previous.Start.Unix(), current.End.Unix()).
		Group("client").Scan(&counts).Error; err != nil {
		return nil, err
	}
	for _, c := range counts {
		out.Current.Sessions += c.Current
		out.Previous.Sessions += c.Prev
		share(sources, DigestSourceKey(c.Client)).Sessions += c.Current
	}

	out.Models = sortedShares(models)
	out.Sources = sortedShares(sources)
	var wallet struct{ Quota int64 }
	if err := CoreDB().Model(&User{}).Select("quota").Where("id = ?", userID).Scan(&wallet).Error; err != nil {
		return nil, err
	}
	out.Wallet = wallet.Quota
	return out, nil
}

// DigestSourceKey groups a client tag into the sources a digest names: the
// coding agents by name, everything else (SDKs, curl, unknown) as direct API.
func DigestSourceKey(client string) string {
	switch client = strings.ToLower(strings.TrimSpace(client)); client {
	case "claude-code", "codex", "opencode", "gemini-cli", "qwen-code", "kilo-code", "roo-code", "crush", "openclaw":
		return client
	}
	if _, ok := common.ExtensionClientName(client); ok {
		return client
	}
	return "api"
}

func sortedShares(set map[string]*DigestShare) []DigestShare {
	out := make([]DigestShare, 0, len(set))
	for _, item := range set {
		if item.Spend == 0 && item.PrevSpend == 0 && item.Sessions == 0 {
			continue
		}
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spend != out[j].Spend {
			return out[i].Spend > out[j].Spend
		}
		if out[i].PrevSpend != out[j].PrevSpend {
			return out[i].PrevSpend > out[j].PrevSpend
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// DigestCandidate is an account the digests may reach.
type DigestCandidate struct {
	Id      int
	Email   string
	Setting string
}

// ListDigestCandidates returns enabled accounts that have paid at least
// once: an online top-up, or credit an operator added by hand (customers
// who pay offline are topped up that way).
func ListDigestCandidates() ([]DigestCandidate, error) {
	var out []DigestCandidate
	manual := CoreDB().Model(&WalletLedger{}).Select("user_id").
		Where("kind = ? AND delta_microusd > 0", WalletLedgerKindAdjustment)
	err := CoreDB().Model(&User{}).Select("id, email, setting").
		Where("status = ? AND email <> ''", 1).
		Where("has_topped_up = ? OR id IN (?)", true, manual).
		Order("id ASC").Scan(&out).Error
	return out, err
}

// DigestLaunch is a model that became available during a period.
type DigestLaunch struct {
	Model  string   `json:"model"`
	Maker  string   `json:"maker"`
	Input  *float64 `json:"input_per_mtok,omitempty"`
	Output *float64 `json:"output_per_mtok,omitempty"`
}

// DigestLaunches lists priced catalogue models added in [start, end), makers
// the reader already uses first.
func DigestLaunches(start, end time.Time, usedModels []string, limit int) []DigestLaunch {
	pricing := GetPricing()
	usedMakers := map[string]bool{}
	for _, name := range usedModels {
		for _, p := range pricing {
			if p.ModelName == name && p.Maker != "" {
				usedMakers[p.Maker] = true
			}
		}
	}
	var out []DigestLaunch
	for _, p := range pricing {
		// Only chat models with an input and output price: a line of
		// "$x in / $y out" is what the digest can say about them.
		if !p.HasPrice || p.InputPerMtok == nil || p.OutputPerMtok == nil || p.CreatedAt < start.Unix() || p.CreatedAt >= end.Unix() {
			continue
		}
		out = append(out, DigestLaunch{Model: p.ModelName, Maker: p.Maker, Input: p.InputPerMtok, Output: p.OutputPerMtok})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return usedMakers[out[i].Maker] && !usedMakers[out[j].Maker]
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
