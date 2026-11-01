package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// A model is filed by what it produces, and a name that plainly says what the
// model is outranks a catalog tag an operator forgot to set.
func TestChannelHealthKindOfFilesModelsByOutput(t *testing.T) {
	kinds := map[string]string{
		"gemini-3.1-flash-image": channelHealthKindImage,
		"claude-sonnet-5":        channelHealthKindLLM,
		// Tagged like a chat model in the catalog, as prod's are.
		"text-embedding-3-small": channelHealthKindLLM,
	}
	for name, want := range map[string]string{
		"gemini-3.1-flash-image": channelHealthKindImage,
		"claude-sonnet-5":        channelHealthKindLLM,
		"text-embedding-3-small": channelHealthKindEmbedding,
		"gpt-image-2":            channelHealthKindImage,
		"flux-1.1-pro":           channelHealthKindImage,
		"BAAI/bge-m3":            channelHealthKindEmbedding,
		"deepseek-v4-flash":      channelHealthKindLLM,
	} {
		if got := channelHealthKindOf(kinds, name); got != want {
			t.Errorf("kind of %s = %q, want %q", name, got, want)
		}
	}
	if got := channelHealthKindFromOutputs([]string{"text", "image"}); got != channelHealthKindImage {
		t.Errorf("text+image output = %q, want image", got)
	}
}

// Image lanes are drawn in three-hour slices: a probe image every few hours
// would leave most hourly cells empty by construction.
func TestFoldChannelHealthDrawsImageLanesInCoarseSlices(t *testing.T) {
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	rows := foldHealthForTest(channelHealthFoldInput{
		now: now, since: since, bucketSeconds: 3600,
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-image-2", model.ChannelLaneDirect, 34, "reseller", model.AbilityRolePrimary),
			healthGridCell("gpt-5.5", model.ChannelLaneDirect, 34, "reseller", model.AbilityRolePrimary),
		},
		kinds: map[string]string{"gpt-image-2": channelHealthKindImage},
		coarseBuckets: []model.ChannelHealthBucketRow{{
			ModelName: "gpt-image-2", Lane: model.ChannelLaneDirect, ChannelId: 34,
			BucketIndex: 7, Kind: model.ChannelHealthKindProbe, RequestCount: 1,
		}},
	})
	image := findLane(t, rows, "gpt-image-2", "stable")
	if image.Kind != channelHealthKindImage || len(image.Buckets) != (channelHealthBucketCount/channelHealthCoarseFactor) {
		t.Fatalf("image lane = kind %q with %d buckets, want image with %d",
			image.Kind, len(image.Buckets), (channelHealthBucketCount / channelHealthCoarseFactor))
	}
	if image.Buckets[7].Tone != "ok" {
		t.Fatalf("image bucket 7 = %+v, want the probe's verdict", image.Buckets[7])
	}
	if got := image.Buckets[1].StartAt.Sub(image.Buckets[0].StartAt); got != 3*time.Hour {
		t.Fatalf("image slice = %s, want 3h", got)
	}
	text := findLane(t, rows, "gpt-5.5", "stable")
	if text.Kind != channelHealthKindLLM || len(text.Buckets) != channelHealthBucketCount {
		t.Fatalf("text lane = kind %q with %d buckets", text.Kind, len(text.Buckets))
	}

	views := channelHealthKindViews(rows, 3600, channelHealthBucketCount)
	if len(views) != 2 || views[0].Kind != channelHealthKindLLM || views[1].Kind != channelHealthKindImage ||
		views[1].BucketSeconds != 3*3600 || views[1].BucketCount != (channelHealthBucketCount/channelHealthCoarseFactor) {
		t.Fatalf("kind views = %+v, want llm then a three-hour image section", views)
	}
}

// Aggregate failures cannot prove ordering. Only repeated all-failed
// samples are red; interspersed successes must not imply a breaker trip.
func TestChannelHealthToneDoesNotInferStreakFromCounts(t *testing.T) {
	cases := []struct {
		count, errors int64
		want          string
	}{
		{3, 0, "ok"},
		{3, 1, "warn"},
		{2, 2, "bad"},
		{1, 1, "warn"},
		{4, 3, "warn"},
		{2000, 3, "ok"},
		{200, 5, "warn"},
		{200, 40, "warn"},
		{6, 3, "warn"},
		{3, 3, "bad"},
	}
	for _, tc := range cases {
		rate := successRate(tc.count, tc.errors)
		if got := channelHealthTone(rate, tc.errors, channelHealthSourceProbe); got != tc.want {
			t.Errorf("%d/%d failed: tone %s, want %s", tc.errors, tc.count, got, tc.want)
		}
	}
}

// A reply that returned 200 is not a working image route unless an image
// came back in it; the same goes for an embedding with no vector.
func TestProbeReplyValidation(t *testing.T) {
	var png64 bytes.Buffer
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	canvas.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&png64, canvas); err != nil {
		t.Fatalf("encode: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(png64.Bytes())
	cases := map[string]bool{
		`{"data":[{"b64_json":"` + encoded + `"}]}`:      true,
		`{"data":[{"url":"https://example.com/a.png"}]}`: true,
		`{"data":[]}`: false,
		`{"data":[{"b64_json":"bm90IGFuIGltYWdl"}]}`: false,
	}
	for body, want := range cases {
		if got := probeReplyHasImage([]byte(body)); got != want {
			t.Errorf("image reply %.60s = %v, want %v", body, got, want)
		}
	}
	if !probeReplyHasEmbedding([]byte(`{"data":[{"embedding":[0,0.12,-0.3]}]}`)) {
		t.Error("a real vector was rejected")
	}
	if probeReplyHasEmbedding([]byte(`{"data":[{"embedding":[0,0,0]}]}`)) {
		t.Error("an all-zero vector was accepted")
	}

	outcome := finalizeChannelProbeResult(context.Background(),
		model.ChannelProbeResult{Modality: channelHealthKindImage},
		testResult{providerAttempted: true, durationMs: 9000, body: []byte(`{"data":[]}`)})
	if outcome.Success || outcome.ErrorMessage == "" {
		t.Fatalf("empty image reply = %+v, want a failure", outcome)
	}
}

func TestProbeReplyHasImageWithLargeJPEGMetadata(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	// Gemini JPEGs can carry metadata before the frame header. Put a legal
	// APP1 segment well beyond the old 3 KiB decoded-prefix limit, without
	// relying on a provider-generated image fixture.
	metadata := bytes.Repeat([]byte("metadata"), 1536)
	segmentLength := len(metadata) + 2
	jpegBytes := append([]byte{0xff, 0xd8, 0xff, 0xe1, byte(segmentLength >> 8), byte(segmentLength)}, metadata...)
	jpegBytes = append(jpegBytes, encoded.Bytes()[2:]...)
	if _, _, err := image.Decode(bytes.NewReader(jpegBytes)); err != nil {
		t.Fatalf("fixture must be a decodable JPEG: %v", err)
	}
	body := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(jpegBytes) + `"}]}`)
	outcome := finalizeChannelProbeResult(context.Background(),
		model.ChannelProbeResult{Modality: channelHealthKindImage},
		testResult{providerAttempted: true, durationMs: 9000, body: body})
	if !outcome.Success || outcome.ErrorMessage != "" {
		t.Fatalf("valid JPEG with large metadata was rejected: %+v", outcome)
	}
	// A valid image header must not hide invalid base64 later in the payload.
	invalidBody := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(jpegBytes) + `!"}]}`)
	if probeReplyHasImage(invalidBody) {
		t.Fatal("invalid base64 suffix was accepted")
	}
}

// A second replica that reaches a cell after the first has probed and
// released it must see the fresh probe and stand down.
func TestChannelProbeAlreadyFreshSeesAnotherReplicasProbe(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	now := time.Now().UTC()
	candidate := ChannelProbeCandidate{
		Lane: model.ChannelLaneDirect, ModelName: "gpt-5.5", ChannelId: 34, Kind: channelHealthKindLLM,
	}
	if channelProbeAlreadyFresh(candidate, now) {
		t.Fatal("a never-probed route read as fresh")
	}
	if err := db.Create(&model.ChannelProbeResult{
		Lane: model.ChannelLaneDirect, ModelName: "gpt-5.5", ChannelId: 34,
		ProbedAt: now.Add(-20 * time.Second), Success: true,
	}).Error; err != nil {
		t.Fatalf("seed probe: %v", err)
	}
	if !channelProbeAlreadyFresh(candidate, now) {
		t.Fatal("a route probed seconds ago read as due")
	}
}

// Latency for kinds without a stream is the customers' own once there are
// enough of them; a probe's one short request stands in otherwise.
func TestChannelHealthLatencyPairPrefersEnoughTraffic(t *testing.T) {
	if p50, p95 := channelHealthLatencyPair(12, 900, 4000, 300, 400); p50 != 900 || p95 != 4000 {
		t.Fatalf("with traffic = %v/%v, want the traffic figures", p50, p95)
	}
	if p50, p95 := channelHealthLatencyPair(2, 900, 4000, 300, 400); p50 != 300 || p95 != 400 {
		t.Fatalf("two calls = %v/%v, want the probe figures", p50, p95)
	}
	if p50, p95 := channelHealthLatencyPair(2, 900, 4000, 0, 0); p50 != 900 || p95 != 4000 {
		t.Fatalf("no probes = %v/%v, want whatever traffic there is", p50, p95)
	}
}

func TestFoldChannelHealthLatencyCutoffCountsMeasuredSuccesses(t *testing.T) {
	traffic := healthEvidence("image-latency", model.ChannelLaneDirect, 35, model.ChannelHealthKindTraffic, 8, 6)
	traffic.DurationCount = 2
	probe := healthEvidence("image-latency", model.ChannelLaneDirect, 35, model.ChannelHealthKindProbe, 5, 0)
	probe.DurationCount = 5
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("image-latency", model.ChannelLaneDirect, 35, "source", model.AbilityRolePrimary),
		},
		kinds:    map[string]string{"image-latency": channelHealthKindImage},
		evidence: []model.ChannelHealthEvidenceRow{traffic, probe},
		percentiles: []model.ChannelHealthPercentileRow{{
			ModelName: "image-latency", Lane: model.ChannelLaneDirect,
			P50LatencyMs: 900, P95LatencyMs: 4000,
			ProbeP50LatencyMs: 300, ProbeP95LatencyMs: 400,
		}},
	})
	row := findLane(t, rows, "image-latency", "stable")
	if row.LatencyP50Ms == nil || *row.LatencyP50Ms != 300 || row.LatencyP95Ms == nil || *row.LatencyP95Ms != 400 {
		t.Fatalf("latency = %v/%v, want probe 300/400 with only two measured customer successes", row.LatencyP50Ms, row.LatencyP95Ms)
	}
}

// A rerank reply that ranks nothing is not a working reranker, and a stream
// that broke after its first token is an interruption, not a refusal.
func TestProbeRerankAndInterruptedStreams(t *testing.T) {
	if !probeReplyHasRanking([]byte(`{"results":[{"index":1,"relevance_score":0.93}]}`)) {
		t.Error("a ranked reply was rejected")
	}
	if probeReplyHasRanking([]byte(`{"results":[]}`)) {
		t.Error("an empty ranking was accepted")
	}
	outcome := finalizeChannelProbeResult(context.Background(),
		model.ChannelProbeResult{Modality: "text"},
		testResult{providerAttempted: true, streamed: true, ttftMs: 700,
			lazuErr: types.NewError(errors.New("stream reset"), types.ErrorCodeBadResponse)})
	if outcome.Success || outcome.TTFTMs != 700 {
		t.Fatalf("broken stream = %+v, want a failure that keeps its first-token time", outcome)
	}
}
