package controller

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// The health page is one wall per kind of model. A text model is judged by
// its first token, its decode speed and whether its stream holds; an image
// model by how long an image takes. Putting them in one table forced every row through the text model's
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

	// channelHealthCoarseFactor is how many ordinary slices one slice of a
	// coarse wall spans. Image generation is sampled hours apart, so a
	// wall of hourly slices would be mostly gaps by construction.
	channelHealthCoarseFactor = 3

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
	case common.IsEmbeddingModel(modelName):
		return channelHealthKindEmbedding
	case isAudioOnlyProbeModel(modelName):
		return channelHealthKindAudio
	case common.IsImageGenerationModel(modelName) || strings.Contains(name, "-image"):
		return channelHealthKindImage
	default:
		return channelHealthKindLLM
	}
}
