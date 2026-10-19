package service

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/vector"
)

// DigestChart is the daily chart of a usage digest: thin token bars, the
// spend as a smooth ink line and a grey dashed baseline (the previous week by
// day, or last month's daily average). It is drawn as a PNG because Gmail and
// Outlook do not show SVG; every figure it shows is also written in the mail.
type DigestChart struct {
	Spend  []float64 `json:"spend"`
	Tokens []float64 `json:"tokens"`
	Prev   []float64 `json:"prev,omitempty"`
	RefAvg float64   `json:"ref_avg,omitempty"`
	Peak   int       `json:"peak"`
}

// Chart geometry in CSS pixels; the PNG is drawn at 2×.
const (
	digestChartW     = 512
	digestChartH     = 200
	digestChartScale = 2
)

var (
	chartPaper   = color.RGBA{0xfd, 0xfd, 0xfc, 0xff}
	chartGrid    = color.RGBA{0xec, 0xeb, 0xe7, 0xff}
	chartAxis    = color.RGBA{0xc9, 0xc8, 0xc2, 0xff}
	chartVolume  = color.RGBA{0xdc, 0xdc, 0xd7, 0xff} // --lazu-chart-3
	chartCompare = color.RGBA{0x8b, 0x90, 0x99, 0xff} // --lazu-chart-2
	chartInk     = color.RGBA{0x23, 0x26, 0x2d, 0xff} // --lazu-chart-1
)

type chartPoint struct{ X, Y float64 }

// RenderDigestChart draws the chart. The background is opaque paper so a
// mail client that forces dark mode cannot hide the ink line.
func RenderDigestChart(c DigestChart) ([]byte, error) {
	w, h := digestChartW*digestChartScale, digestChartH*digestChartScale
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{chartPaper}, image.Point{}, draw.Src)
	s := float64(digestChartScale)
	n := len(c.Spend)
	if n == 0 {
		var buf bytes.Buffer
		err := png.Encode(&buf, img)
		return buf.Bytes(), err
	}

	top := 18.0
	plot := float64(digestChartH) - top
	maxSpend := c.RefAvg
	for i, v := range c.Spend {
		maxSpend = math.Max(maxSpend, v)
		if i < len(c.Prev) {
			maxSpend = math.Max(maxSpend, c.Prev[i])
		}
	}
	if maxSpend <= 0 {
		maxSpend = 1
	}
	maxTokens := 0.0
	for _, v := range c.Tokens {
		maxTokens = math.Max(maxTokens, v)
	}
	x := func(i int) float64 { return (float64(i) + 0.5) / float64(n) * digestChartW }
	y := func(v float64) float64 { return digestChartH - v/maxSpend*plot }

	// Grid: half and full scale, then the baseline.
	for _, frac := range []float64{0.5, 1} {
		gy := digestChartH - frac*plot
		fillRect(img, 0, gy*s, float64(w), 2, chartGrid)
	}
	fillRect(img, 0, float64(h)-2, float64(w), 2, chartAxis)

	// Volume bars.
	barW := math.Min(22, digestChartW/float64(n)*0.55)
	if maxTokens > 0 {
		for i, v := range c.Tokens {
			bh := v / maxTokens * 110
			if v > 0 && bh < 2 {
				bh = 2
			}
			fillRect(img, (x(i)-barW/2)*s, (digestChartH-bh)*s, barW*s, bh*s-2, chartVolume)
		}
	}

	// Comparison baseline.
	if len(c.Prev) == n {
		prev := make([]chartPoint, n)
		for i, v := range c.Prev {
			prev[i] = chartPoint{x(i) * s, y(v) * s}
		}
		strokeDashed(img, smoothCurve(prev, 12), 1.5*s, 4*s, 4*s, chartCompare)
	} else if c.RefAvg > 0 {
		ry := y(c.RefAvg) * s
		strokeDashed(img, []chartPoint{{0, ry}, {float64(w), ry}}, 1.5*s, 4*s, 4*s, chartCompare)
	}

	// Spend line and the peak.
	points := make([]chartPoint, n)
	for i, v := range c.Spend {
		points[i] = chartPoint{x(i) * s, y(v) * s}
	}
	curve := smoothCurve(points, 16)
	if n == 1 {
		curve = []chartPoint{{points[0].X - 1, points[0].Y}, {points[0].X + 1, points[0].Y}}
	}
	strokeRibbon(img, curve, 2.25*s, chartInk)
	if c.Peak >= 0 && c.Peak < n {
		fillCircle(img, points[c.Peak], 5.5*s, chartInk)
		fillCircle(img, points[c.Peak], 3.2*s, chartPaper)
	}

	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}

// smoothCurve samples a monotone cubic (Fritsch–Carlson) through the points:
// smooth like the console's line, but it never swings above or below the
// data, so a quiet day is not drawn lower than it was.
func smoothCurve(points []chartPoint, steps int) []chartPoint {
	n := len(points)
	if n < 2 {
		return points
	}
	slopes := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		dx := points[i+1].X - points[i].X
		if dx == 0 {
			dx = 1
		}
		slopes[i] = (points[i+1].Y - points[i].Y) / dx
	}
	tangents := make([]float64, n)
	tangents[0], tangents[n-1] = slopes[0], slopes[n-2]
	for i := 1; i < n-1; i++ {
		if slopes[i-1]*slopes[i] <= 0 {
			tangents[i] = 0
		} else {
			tangents[i] = (slopes[i-1] + slopes[i]) / 2
		}
	}
	for i := 0; i < n-1; i++ {
		if slopes[i] == 0 {
			tangents[i], tangents[i+1] = 0, 0
			continue
		}
		a, b := tangents[i]/slopes[i], tangents[i+1]/slopes[i]
		if h := a*a + b*b; h > 9 {
			t := 3 / math.Sqrt(h)
			tangents[i], tangents[i+1] = t*a*slopes[i], t*b*slopes[i]
		}
	}
	out := []chartPoint{points[0]}
	for i := 0; i < n-1; i++ {
		p1, p2 := points[i], points[i+1]
		dx := (p2.X - p1.X) / 3
		c1 := chartPoint{p1.X + dx, p1.Y + tangents[i]*dx}
		c2 := chartPoint{p2.X - dx, p2.Y - tangents[i+1]*dx}
		for step := 1; step <= steps; step++ {
			t := float64(step) / float64(steps)
			u := 1 - t
			out = append(out, chartPoint{
				X: u*u*u*p1.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*p2.X,
				Y: u*u*u*p1.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*p2.Y,
			})
		}
	}
	return out
}

func newRaster(img *image.RGBA) *vector.Rasterizer {
	b := img.Bounds()
	return vector.NewRasterizer(b.Dx(), b.Dy())
}

func paint(img *image.RGBA, z *vector.Rasterizer, c color.RGBA) {
	z.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{})
}

func fillRect(img *image.RGBA, x0, y0, width, height float64, c color.RGBA) {
	if width <= 0 || height <= 0 {
		return
	}
	z := newRaster(img)
	z.MoveTo(float32(x0), float32(y0))
	z.LineTo(float32(x0+width), float32(y0))
	z.LineTo(float32(x0+width), float32(y0+height))
	z.LineTo(float32(x0), float32(y0+height))
	z.ClosePath()
	paint(img, z, c)
}

func fillCircle(img *image.RGBA, center chartPoint, radius float64, c color.RGBA) {
	z := newRaster(img)
	addCircle(z, center, radius)
	paint(img, z, c)
}

func addCircle(z *vector.Rasterizer, center chartPoint, radius float64) {
	const segments = 48
	for i := 0; i <= segments; i++ {
		a := -float64(i) / segments * 2 * math.Pi
		px, py := float32(center.X+radius*math.Cos(a)), float32(center.Y+radius*math.Sin(a))
		if i == 0 {
			z.MoveTo(px, py)
		} else {
			z.LineTo(px, py)
		}
	}
	z.ClosePath()
}

// addRibbon adds a stroke outline of a polyline: left side forward, right
// side back. The spline is smooth, so the outline does not fold over.
func addRibbon(z *vector.Rasterizer, points []chartPoint, width float64) {
	if len(points) < 2 {
		return
	}
	half := width / 2
	normal := func(i int) chartPoint {
		a, b := points[max(i-1, 0)], points[min(i+1, len(points)-1)]
		dx, dy := b.X-a.X, b.Y-a.Y
		length := math.Hypot(dx, dy)
		if length == 0 {
			return chartPoint{0, 1}
		}
		return chartPoint{-dy / length, dx / length}
	}
	for i, p := range points {
		nrm := normal(i)
		px, py := float32(p.X+nrm.X*half), float32(p.Y+nrm.Y*half)
		if i == 0 {
			z.MoveTo(px, py)
		} else {
			z.LineTo(px, py)
		}
	}
	for i := len(points) - 1; i >= 0; i-- {
		nrm := normal(i)
		z.LineTo(float32(points[i].X-nrm.X*half), float32(points[i].Y-nrm.Y*half))
	}
	z.ClosePath()
}

func strokeRibbon(img *image.RGBA, points []chartPoint, width float64, c color.RGBA) {
	z := newRaster(img)
	addRibbon(z, points, width)
	paint(img, z, c)
	// Round the two ends.
	fillCircle(img, points[0], width/2, c)
	fillCircle(img, points[len(points)-1], width/2, c)
}

// strokeDashed cuts the polyline into dash-length pieces along its length.
func strokeDashed(img *image.RGBA, points []chartPoint, width, dash, gap float64, c color.RGBA) {
	z := newRaster(img)
	var piece []chartPoint
	pos, on := 0.0, true
	for i := 0; i < len(points)-1; i++ {
		a, b := points[i], points[i+1]
		segment := math.Hypot(b.X-a.X, b.Y-a.Y)
		done := 0.0
		for done < segment {
			limit := dash
			if !on {
				limit = gap
			}
			step := math.Min(limit-pos, segment-done)
			t0, t1 := done/segment, (done+step)/segment
			if on {
				if len(piece) == 0 {
					piece = append(piece, chartPoint{a.X + (b.X-a.X)*t0, a.Y + (b.Y-a.Y)*t0})
				}
				piece = append(piece, chartPoint{a.X + (b.X-a.X)*t1, a.Y + (b.Y-a.Y)*t1})
			}
			done += step
			pos += step
			if pos >= limit {
				if on && len(piece) >= 2 {
					addRibbon(z, piece, width)
				}
				piece, pos, on = nil, 0, !on
			}
		}
	}
	if on && len(piece) >= 2 {
		addRibbon(z, piece, width)
	}
	paint(img, z, c)
}
