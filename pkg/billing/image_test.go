package billing

import "testing"

func imagePrice(v float64) *float64 { return &v }

func TestImagePricingActualOutputs(t *testing.T) {
	pro := &ImagePrices{OutputUSD: .045, PixelThreshold: 2610000, HighResolutionOutputUSD: imagePrice(.09), LayerUSD: imagePrice(.0225), HighResolutionLayerUSD: imagePrice(.045), ReferenceUSD: imagePrice(.003), FreeReferences: 1}
	for _, tt := range []struct {
		name  string
		usage ImageUsage
		price *ImagePrices
		want  MicroUSD
	}{
		{"threshold included", ImageUsage{Outputs: []ImageOutput{{Pixels: 2610000}}}, pro, 45000},
		{"higher pixel tier", ImageUsage{Outputs: []ImageOutput{{Pixels: 2610001}}}, pro, 90000},
		{"extra references", ImageUsage{InputImages: 3, Outputs: []ImageOutput{{Pixels: 2368 * 1776}}}, pro, 96000},
		{"mixed layer sizes", ImageUsage{InputImages: 1, Outputs: []ImageOutput{{Pixels: 2048 * 2048, Layer: true}, {Pixels: 1556 * 562, Layer: true}}}, pro, 67500},
		{"failed outputs cost zero", ImageUsage{InputImages: 3}, pro, 0},
		{"lite actual two images", ImageUsage{InputImages: 2, Outputs: []ImageOutput{{Pixels: 2048 * 2048}, {Pixels: 4096 * 4096}}}, &ImagePrices{OutputUSD: .035}, 70000},
		{"flash each layer", ImageUsage{InputImages: 1, Outputs: []ImageOutput{{Pixels: 1872 * 1248, Layer: true}, {Pixels: 1057 * 255, Layer: true}, {Pixels: 1541 * 553, Layer: true}}}, &ImagePrices{OutputUSD: .018}, 54000},
		{"provider discount all dimensions", ImageUsage{InputImages: 3, Outputs: []ImageOutput{{Pixels: 2368 * 1776}}}, pro.Scaled(.8), 76800},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := Quote(Usage{Images: &tt.usage}, PriceSheet{Images: tt.price}, nil)
			if q.TotalMicrousd != tt.want || len(q.MissingPriceDimensions) > 0 {
				t.Fatalf("quote=%+v want=%d", q, tt.want)
			}
		})
	}
	if pro.OutputUSD != .045 {
		t.Fatal("scaling mutated shared price snapshot")
	}
}

func TestImagePricingRejectsIncompleteTiers(t *testing.T) {
	if (&ImagePrices{OutputUSD: .045, HighResolutionOutputUSD: imagePrice(.09)}).Validate() == nil {
		t.Fatal("accepted missing pixel threshold")
	}
	q := Quote(Usage{Images: &ImageUsage{Outputs: []ImageOutput{{Pixels: 1024 * 1024}}}}, PriceSheet{}, nil)
	if len(q.MissingPriceDimensions) != 1 || q.TotalMicrousd != 0 {
		t.Fatalf("unpriced images: %+v", q)
	}
}
