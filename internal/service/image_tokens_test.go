package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"
	"testing"

	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func inlinePNG(t testing.TB, width, height int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestImageTokenGeometry(t *testing.T) {
	for _, tc := range []struct {
		model, detail string
		w, h, want    int
	}{
		{"gpt-4o-mini", "low", 4096, 4096, 2833},
		{"gpt-4o-mini", "high", 512, 512, 8500},
		{"gpt-4o-mini", "auto", 1024, 1024, 25501},
		{"gpt-4o-mini", "high", 2048, 768, 48169},
		{"gpt-4o", "high", 1, 1000000, 765},
		{"gpt-4o", "high", 64, 64, 255},
		{"gpt-4o", "high", 512, 512, 255},
		{"gpt-4o", "auto", 512, 512, 255},
		{"gpt-4o", "high", 512, 1024, 425},
		{"gpt-4o", "high", 768, 768, 765},
		{"gpt-4o", "high", 1024, 1024, 765},
		{"gpt-5.1", "high", 512, 512, 210},
		{"o3", "high", 512, 512, 225},
		{"gpt-4.1-mini", "high", 1024, 1024, 1659},
		{"gpt-5.4", "high", 1024, 1024, 1229},
		{"gpt-5.4-mini", "high", 2048, 2048, 3000},
		{"gpt-5.4", "low", 2048, 2048, 4916},
		{"gpt-5.4", "original", 3200, 3200, 12000},
	} {
		t.Run(tc.model+"/"+tc.detail+"/"+strconv.Itoa(tc.w)+"x"+strconv.Itoa(tc.h), func(t *testing.T) {
			rule, ok := visionTokenRule(tc.model, tc.detail)
			if !ok {
				t.Fatal("missing known rule")
			}
			if got := rule.count(tc.w, tc.h, tc.detail); got != tc.want {
				t.Fatalf("%dx%d got %d want %d", tc.w, tc.h, got, tc.want)
			}
		})
	}
}

func TestImageAdmissionUsesDimensionsOrModelReserve(t *testing.T) {
	for _, tc := range []struct {
		detail, data string
		want         int
	}{
		{"low", "https://unreachable.invalid/image.png", 2833},
		{"high", "https://unreachable.invalid/image.png", 48169},
		{"auto", inlinePNG(t, 128, 128), 8500},
		{"high", inlinePNG(t, 1024, 1024), 25501},
		{"high", "bad", 48169},
	} {
		file := &types.FileMeta{FileType: types.FileTypeImage, OriginData: tc.data, Detail: tc.detail}
		got, err := EstimateRequestTPMTokens(&types.TokenCountMeta{Files: []*types.FileMeta{file, file}}, &relaycommon.RelayInfo{OriginModelName: "gpt-4o-mini"})
		if err != nil || got != 2*tc.want {
			t.Fatalf("detail %s: got %d err %v want %d", tc.detail, got, err, 2*tc.want)
		}
	}
	// Unknown future models must not accidentally use the old gpt-5 tile rule.
	if _, known := visionTokenRule("gpt-5.99", "high"); known {
		t.Fatal("unknown model inherited tile rule")
	}
}

func TestImageHeaderBoundAndNoMutation(t *testing.T) {
	file := &types.FileMeta{OriginData: inlinePNG(t, 128, 128), Detail: "auto"}
	if got, err := getImageToken(file, "gpt-4o", true); err != nil || got != 255 {
		t.Fatalf("got %d err %v", got, err)
	}
	if file.Detail != "auto" || file.MimeType != "" {
		t.Fatal("count mutated request metadata")
	}
	for _, input := range []string{
		"data:" + strings.Repeat("x", 32<<20),
		strings.Repeat("\n", 32<<20) + strings.TrimPrefix(file.OriginData, "data:image/png;base64,"),
		"https://unreachable.invalid/image.png",
	} {
		if _, err := inlineImageConfig(&types.FileMeta{OriginData: input}); err == nil {
			t.Fatal("unbounded or remote image unexpectedly parsed")
		}
	}
}

func TestImageEstimateStaysWithinReserve(t *testing.T) {
	for _, model := range []string{"gpt-4o-mini", "gpt-5.4", "gpt-4.1-mini", "o4-mini"} {
		for _, detail := range []string{"low", "high", "original"} {
			rule, _ := visionTokenRule(model, detail)
			for _, w := range []int{1, 32, 33, 512, 768, 1024, 2048, 1000000, math.MaxInt} {
				for _, h := range []int{1, 511, 1537, 4096, math.MaxInt} {
					got := rule.count(w, h, detail)
					if got <= 0 || got > rule.reserve(detail) {
						t.Fatalf("%s/%s %dx%d got %d reserve %d", model, detail, w, h, got, rule.reserve(detail))
					}
				}
			}
		}
	}
}

func BenchmarkImageAdmissionHeader(b *testing.B) {
	file := &types.FileMeta{OriginData: inlinePNG(b, 64, 64) + strings.Repeat("A", 64<<20), Detail: "high"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		imageAdmissionTokens(file, "gpt-4o-mini")
	}
}
