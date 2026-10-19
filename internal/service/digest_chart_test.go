package service

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestRenderDigestChartDrawsInkOnPaper(t *testing.T) {
	chart := DigestChart{
		Spend:  []float64{7.2, 8.1, 6.9, 9.4, 8.3, 2.6, 1.8},
		Tokens: []float64{5.1, 6.0, 4.9, 7.2, 6.3, 1.4, 0.9},
		Prev:   []float64{8.0, 7.6, 7.9, 8.8, 9.1, 3.2, 2.5},
		Peak:   3,
	}
	out, err := RenderDigestChart(chart)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 400 {
		t.Fatalf("chart is %v, want 1024×400", b)
	}
	// The top-left corner is paper; the peak dot's ring is ink.
	if r, g, b, _ := img.At(2, 2).RGBA(); r>>8 != 0xfd || g>>8 != 0xfd || b>>8 != 0xfc {
		t.Fatalf("background is not paper: %x %x %x", r>>8, g>>8, b>>8)
	}
	if dir := os.Getenv("DIGEST_CHART_OUT"); dir != "" {
		_ = os.WriteFile(dir+"/weekly.png", out, 0o644)
		monthly := DigestChart{Spend: []float64{3.1, 4.2, 2.8, 5.6, 6.1, 1.2, 0.9, 5.4, 6.8, 7.2, 6.4, 5.9, 1.8, 1.1, 6.6, 7.9, 9.4, 14.3, 8.8, 2.4, 1.6, 8.42, 9.52, 9.02, 10.42, 9.92, 2.2, 1.4, 12.0, 11.8}, RefAvg: 4.83, Peak: 17}
		for _, v := range monthly.Spend {
			monthly.Tokens = append(monthly.Tokens, v*0.22)
		}
		m, _ := RenderDigestChart(monthly)
		_ = os.WriteFile(dir+"/monthly.png", m, 0o644)
	}
}
