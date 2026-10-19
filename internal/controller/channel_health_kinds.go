package controller

import (
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// The health page is one wall per kind of model. A text model is judged by
// its first token, its decode speed and whether its stream holds; an image
// model by how long an image takes; a search backend by how long a query
// takes. Putting them in one table forced every row through the text model's
// columns, which is how image rows came to be a line of dashes.
//
// Kind is what a model PRODUCES, read from the catalog's output modality —
// the same field the model plaza shows — so a model is filed once and in the
// same place everywhere. Audio and video have kinds ready for when those
// models ship; nothing else has to change for their sections to appear.

const (
	channelHealthKindLLM       = "llm"
	channelHealthKindEmbedding = "embedding"
	channelHealthKindImage     = "image"
	channelHealthKindAudio     = "audio"
	channelHealthKindVideo     = "video"
	channelHealthKindRerank    = "rerank"
	channelHealthKindDecision  = "decision"
	channelHealthKindSearch    = "search"

	// channelHealthCoarseFactor is how many ordinary slices one slice of a
	// coarse wall spans. Image generation is sampled hours apart, so a
	// wall of hourly slices would be mostly gaps by construction.
	channelHealthCoarseFactor = 3

	// searchHealthFailureStreak is how many failed samples in a row make a
	// search backend read as down. It matches the router's breaker for model
	// routes, so "outage" means the same thing in every section.
	searchHealthFailureStreak = model.RouteHealthFailureThreshold

	// channelHealthMinTrafficForLatency is how many customer requests a
	// lane needs before its latency is read from them alone. A probe asks for
	// one short string or one small image while customers send batches and
	// high-quality renders, so the two are not one population; but a p95 of
	// two customer calls is not a percentile either, and below this the
	// probes' steady figure is the more honest one.
	channelHealthMinTrafficForLatency = 5
)

// channelHealthLatencyPair picks the latency a kind without a stream shows:
// the customers' own once there are enough of them, the probes' otherwise.
func channelHealthLatencyPair(traffic int64, p50, p95, probeP50, probeP95 float64) (float64, float64) {
	if traffic >= channelHealthMinTrafficForLatency && (p50 > 0 || p95 > 0) {
		return p50, p95
	}
	if probeP50 > 0 || probeP95 > 0 {
		return probeP50, probeP95
	}
	return p50, p95
}

// channelHealthKindOrder is the order sections are shown in. Text models are
// what most of the traffic is and come first; the rest follow by how often a
// reader is likely to look for them.
var channelHealthKindOrder = []string{
	channelHealthKindLLM,
	channelHealthKindEmbedding,
	channelHealthKindRerank,
	channelHealthKindDecision,
	channelHealthKindImage,
	channelHealthKindAudio,
	channelHealthKindVideo,
	channelHealthKindSearch,
}

// channelHealthKindView is one section of the page.
type channelHealthKindView struct {
	Kind          string `json:"kind"`
	BucketCount   int    `json:"bucket_count"`
	BucketSeconds int64  `json:"bucket_seconds"`
	RowCount      int    `json:"row_count"`
}

// channelHealthKindIsCoarse reports whether a kind's wall uses wide slices.
func channelHealthKindIsCoarse(kind string) bool {
	return kind == channelHealthKindImage || kind == channelHealthKindVideo
}

// channelHealthKindViews lists the sections the given rows fill, in display
// order, with the slice width each one's wall is drawn in.
func channelHealthKindViews(rows []channelHealthLaneView, bucketSeconds int64, bucketCount int) []channelHealthKindView {
	counts := map[string]int{}
	for _, row := range rows {
		counts[channelHealthRowKind(row)]++
	}
	views := make([]channelHealthKindView, 0, len(counts))
	for _, kind := range channelHealthKindOrder {
		if counts[kind] == 0 {
			continue
		}
		view := channelHealthKindView{
			Kind:          kind,
			BucketCount:   bucketCount,
			BucketSeconds: bucketSeconds,
			RowCount:      counts[kind],
		}
		if channelHealthKindIsCoarse(kind) {
			view.BucketCount = bucketCount / channelHealthCoarseFactor
			view.BucketSeconds = bucketSeconds * channelHealthCoarseFactor
		}
		views = append(views, view)
	}
	return views
}

// channelHealthRowKind reads a row's kind, treating a row from before kinds
// existed — a materialized snapshot written by an older build — as text.
func channelHealthRowKind(row channelHealthLaneView) string {
	if row.Kind == "" {
		return channelHealthKindLLM
	}
	return row.Kind
}

// channelHealthCatalogKinds maps every catalog model to what it produces,
// read from the same modality the catalog publishes.
func channelHealthCatalogKinds() map[string]string {
	pricingList := model.GetPricing()
	kinds := make(map[string]string, len(pricingList))
	for _, pricing := range pricingList {
		name := strings.TrimSpace(pricing.ModelName)
		if name == "" {
			continue
		}
		endpointTypes := pricing.SupportedEndpointTypes
		if len(endpointTypes) == 0 {
			endpointTypes = model.GetModelSupportEndpointTypes(name)
		}
		modality := buildCatalogModality(true, pricing, endpointTypes)
		kinds[name] = channelHealthKindFromOutputs(modality.Output)
	}
	return kinds
}

// channelHealthKindFromOutputs files a model by the most specific thing it
// produces. A model that writes text and also draws images is an image model
// here: its text is incidental, and its images are what it is chosen for.
func channelHealthKindFromOutputs(outputs []string) string {
	has := map[string]bool{}
	for _, output := range outputs {
		has[strings.ToLower(strings.TrimSpace(output))] = true
	}
	switch {
	case has["decision"]:
		return channelHealthKindDecision
	case has["video"]:
		return channelHealthKindVideo
	case has["image"]:
		return channelHealthKindImage
	case has["audio"]:
		return channelHealthKindAudio
	case has["embedding"]:
		return channelHealthKindEmbedding
	case has["rerank"]:
		return channelHealthKindRerank
	default:
		return channelHealthKindLLM
	}
}

// channelHealthKindOf resolves one model's kind. The catalog answers for
// every model it publishes; a routed variant falls back to its catalog name,
// and a model the catalog does not describe to what its name plainly says.
func channelHealthKindOf(kinds map[string]string, modelName string) string {
	if kind, ok := kinds[modelName]; ok && kind != channelHealthKindLLM {
		return kind
	}
	if normalized := model.NormalizePricingModelName(modelName); normalized != "" {
		if kind, ok := kinds[normalized]; ok && kind != channelHealthKindLLM {
			return kind
		}
	}
	// The catalog's modality comes from tags and endpoint lists an operator
	// maintains by hand, and an embedding model tagged like a chat model is
	// common. A name that says what the model is outranks a missing tag.
	name := strings.ToLower(strings.TrimSpace(modelName))
	if common.IsDecisionModel(name) {
		return channelHealthKindDecision
	}
	switch {
	case common.IsVideoGenerationModel(modelName):
		return channelHealthKindVideo
	case strings.Contains(name, "rerank"):
		return channelHealthKindRerank
	case looksLikeEmbeddingModel(modelName):
		return channelHealthKindEmbedding
	case isAudioOnlyProbeModel(modelName):
		return channelHealthKindAudio
	case common.IsImageGenerationModel(modelName) || strings.Contains(name, "-image"):
		return channelHealthKindImage
	default:
		return channelHealthKindLLM
	}
}

// searchProviderLabels is how each search backend is named on the page. They
// are the names customers already pass as `backend` to /v1/search, so unlike
// model upstreams they are part of the product rather than an operations
// detail.
var searchProviderLabels = map[string]string{
	"tavily": "Tavily",
	"serper": "Serper",
	"jina":   "Jina",
	"exa":    "Exa",
	"brave":  "Brave",
}

func searchProviderLabel(provider string) string {
	if label, ok := searchProviderLabels[provider]; ok {
		return label
	}
	if provider == "" {
		return "Search"
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// searchHealthBackend is one enabled search backend the page reports on.
type searchHealthBackend struct {
	channelID int
	name      string
	provider  string
}

// loadSearchHealthBackends lists the enabled search backends.
func loadSearchHealthBackends() ([]searchHealthBackend, error) {
	if model.CoreDB() == nil {
		return nil, nil
	}
	var channels []*model.Channel
	if err := model.CoreDB().
		Where("type IN ? AND status = ?", constant.SearchChannelTypes(), common.ChannelStatusEnabled).
		Order("id asc").
		Find(&channels).Error; err != nil {
		return nil, err
	}
	backends := make([]searchHealthBackend, 0, len(channels))
	for _, channel := range channels {
		provider := searchChannelProvider(channel)
		// A fetch-only channel never answers /v1/search and is not a
		// search source customers could be affected by.
		if provider == "" || !service.SearchChannelServesWebSearch(channel) {
			continue
		}
		backends = append(backends, searchHealthBackend{
			channelID: channel.Id, name: channel.Name, provider: provider,
		})
	}
	return backends, nil
}

// searchChannelProvider names the provider a search channel speaks for, with
// the same override /v1/search honours.
func searchChannelProvider(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	provider := constant.SearchProviderFromChannelType(channel.Type)
	if settings := channel.GetOtherSettings().SearchBackend; settings != nil && strings.TrimSpace(settings.Provider) != "" {
		provider = strings.ToLower(strings.TrimSpace(settings.Provider))
	}
	return provider
}

// foldSearchHealth turns search samples into one row per search provider.
//
// A provider is a row rather than a channel because the provider is what a
// customer selects; two keys for one provider are one row, as two upstreams of
// one model lane are. Status follows the breaker's rule: three failures in a
// row is down, a failed newest sample is degraded, a successful one is up.
func foldSearchHealth(
	backends []searchHealthBackend,
	samples []model.SearchHealthSample,
	since time.Time,
	bucketSeconds int64,
	bucketCount int,
) []channelHealthLaneView {
	type accumulator struct {
		view     channelHealthLaneView
		channels map[int]*channelHealthSourceView
		cells    map[int]map[int]*channelHealthSourceCell
		traffic  []float64
		probes   []float64
		streak   map[int]int
	}
	providers := map[string]*accumulator{}
	byChannel := map[int]string{}
	ensure := func(provider string) *accumulator {
		if acc, ok := providers[provider]; ok {
			return acc
		}
		acc := &accumulator{
			view: channelHealthLaneView{
				ModelName: searchProviderLabel(provider),
				Kind:      channelHealthKindSearch,
				Provider:  provider,
				Status:    publicStatusUnknown,
				Source:    channelHealthSourceNone,
			},
			channels: map[int]*channelHealthSourceView{},
			cells:    map[int]map[int]*channelHealthSourceCell{},
			streak:   map[int]int{},
		}
		providers[provider] = acc
		return acc
	}
	for _, backend := range backends {
		acc := ensure(backend.provider)
		byChannel[backend.channelID] = backend.provider
		acc.channels[backend.channelID] = &channelHealthSourceView{
			ChannelID:   backend.channelID,
			ChannelName: backend.name,
			Status:      publicStatusUnknown,
			Source:      channelHealthSourceNone,
			Role:        model.AbilityRolePrimary,
		}
		acc.view.ChannelCount++
	}

	for _, sample := range samples {
		provider, ok := byChannel[sample.ChannelId]
		if !ok {
			// A backend since disabled or deleted: history, not the pool.
			continue
		}
		acc := providers[provider]
		source := acc.channels[sample.ChannelId]
		at := sample.SampledAt.UTC()
		index := int(at.Sub(since) / (time.Duration(bucketSeconds) * time.Second))
		if index < 0 || index >= bucketCount {
			continue
		}
		if acc.cells[index] == nil {
			acc.cells[index] = map[int]*channelHealthSourceCell{}
		}
		cell := acc.cells[index][sample.ChannelId]
		if cell == nil {
			cell = &channelHealthSourceCell{}
			acc.cells[index][sample.ChannelId] = cell
		}
		failed := int64(0)
		if !sample.Success {
			failed = 1
		}
		if sample.Kind == model.SearchHealthKindProbe {
			cell.probeCount++
			cell.probeError += failed
			source.ProbeCount++
			source.probeErrors += failed
			source.lastProbedAt = laterChannelHealthTime(source.lastProbedAt, &at)
		} else {
			cell.trafficCount++
			cell.trafficError += failed
			source.RequestCount++
			source.trafficErrors += failed
			source.lastTrafficAt = laterChannelHealthTime(source.lastTrafficAt, &at)
		}
		source.LastEvidenceAt = laterChannelHealthTime(source.LastEvidenceAt, &at)
		// Samples arrive oldest first, so the streak ends as the run of
		// failures leading up to the newest sample.
		if sample.Success {
			acc.streak[sample.ChannelId] = 0
			if sample.LatencyMs > 0 && sample.Kind == model.SearchHealthKindProbe {
				acc.probes = append(acc.probes, float64(sample.LatencyMs))
			} else if sample.LatencyMs > 0 {
				acc.traffic = append(acc.traffic, float64(sample.LatencyMs))
			}
		} else {
			acc.streak[sample.ChannelId]++
		}
	}

	rows := make([]channelHealthLaneView, 0, len(providers))
	for _, acc := range providers {
		view := acc.view
		view.Buckets = emptyChannelHealthBuckets(since, bucketSeconds, bucketCount)
		sources := make([]channelHealthSourceView, 0, len(acc.channels))
		for channelID, source := range acc.channels {
			source.ConsecutiveFailures = acc.streak[channelID]
			source.Source = channelHealthSourceOf(source)
			source.Status = channelHealthSourceStatus(source)
			if source.samples() > 0 {
				source.SuccessRate = successRate(source.samples(), source.sampleErrors())
			}
			view.RequestCount += source.RequestCount
			view.ErrorCount += source.trafficErrors
			view.ProbeCount += source.ProbeCount
			view.ProbeErrorCount += source.probeErrors
			view.LastOutcomeAt = laterChannelHealthTime(view.LastOutcomeAt, source.lastTrafficAt)
			view.LastProbedAt = laterChannelHealthTime(view.LastProbedAt, source.lastProbedAt)
			timeline := channelHealthChannelTimeline(acc.cells, channelID, false, bucketCount)
			for index := range timeline {
				if timeline[index] != nil {
					mergeChannelHealthBucketVerdict(&view.Buckets[index], *timeline[index], channelID)
				}
			}
			sources = append(sources, *source)
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i].ChannelID < sources[j].ChannelID })
		view.Sources = sources
		switch {
		case view.RequestCount > 0:
			view.Source = channelHealthSourceTraffic
		case view.ProbeCount > 0:
			view.Source = channelHealthSourceProbe
		}
		view.SuccessRate = successRate(view.RequestCount+view.ProbeCount, view.ErrorCount+view.ProbeErrorCount)
		latencyP50, latencyP95 := channelHealthLatencyPair(
			int64(len(acc.traffic)),
			channelHealthRank(acc.traffic, 0.50), channelHealthRank(acc.traffic, 0.95),
			channelHealthRank(acc.probes, 0.50), channelHealthRank(acc.probes, 0.95),
		)
		view.LatencyP50Ms = positiveChannelHealthFigure(latencyP50)
		view.LatencyP95Ms = positiveChannelHealthFigure(latencyP95)
		view.P95LatencyMs = view.LatencyP95Ms
		serving, _ := electChannelHealthServingSources(sources)
		view.ServingCount = len(serving)
		view.Status = channelHealthLaneStatus(serving)
		rows = append(rows, view)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ModelName < rows[j].ModelName })
	return rows
}

// channelHealthRank returns the first value at or past the fraction of the
// sorted population — the same rule the SQL percentiles use, so a search
// p95 and a model p95 are the same statistic.
func channelHealthRank(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	threshold := float64(len(sorted)) * fraction
	for index, value := range sorted {
		if float64(index+1) >= threshold {
			return value
		}
	}
	return sorted[len(sorted)-1]
}

// Search has no routing breaker. Refresh its current verdict with a bounded
// indexed read; retain the materialized window's figures and buckets verbatim.
func overlayChannelHealthSearch(snapshot channelHealthSnapshot, now time.Time) (channelHealthSnapshot, error) {
	backends, err := loadSearchHealthBackends()
	if err != nil {
		return snapshot, err
	}
	ids := make([]int, 0, len(backends))
	for _, backend := range backends {
		ids = append(ids, backend.channelID)
	}
	since := now.Add(-time.Duration(snapshot.windowSeconds) * time.Second)
	samples, err := model.QuerySearchHealthRecentSamples(ids, since)
	if err != nil {
		return snapshot, err
	}
	// One temporary bucket is sufficient: these samples supply only status.
	current := foldSearchHealth(backends, samples, since, snapshot.windowSeconds+1, 1)
	historical := make(map[string]channelHealthLaneView)
	rows := make([]channelHealthLaneView, 0, len(snapshot.rows)+len(current))
	for _, row := range snapshot.rows {
		if row.Kind == channelHealthKindSearch {
			historical[row.Provider] = row
		} else {
			rows = append(rows, row)
		}
	}
	for _, fresh := range current {
		row, exists := historical[fresh.Provider]
		if !exists {
			row = channelHealthLaneView{ModelName: fresh.ModelName, Provider: fresh.Provider, Kind: channelHealthKindSearch,
				Source: channelHealthSourceNone, Buckets: emptyChannelHealthBuckets(snapshot.since, snapshot.bucketSeconds, snapshot.slices())}
		}
		oldSources := make(map[int]channelHealthSourceView)
		for _, source := range row.Sources {
			oldSources[source.ChannelID] = source
		}
		row.Sources = make([]channelHealthSourceView, 0, len(fresh.Sources))
		for _, source := range fresh.Sources {
			old := oldSources[source.ChannelID]
			old.ChannelID, old.ChannelName, old.Role = source.ChannelID, source.ChannelName, source.Role
			old.Status, old.ConsecutiveFailures, old.LastEvidenceAt = source.Status, source.ConsecutiveFailures, source.LastEvidenceAt
			row.Sources = append(row.Sources, old)
		}
		// A newly observed backend is visible to members immediately, even
		// before its first full historical snapshot is materialized.
		if row.Source == channelHealthSourceNone && fresh.Source != channelHealthSourceNone {
			row.Source = fresh.Source
		}
		row.Status, row.ServingCount, row.ChannelCount = fresh.Status, fresh.ServingCount, fresh.ChannelCount
		rows = append(rows, row)
	}
	snapshot.rows = rows
	return snapshot, nil
}
