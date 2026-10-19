package main

// Closed-loop streaming benchmark: C workers, each sends requests back to back.
// Records TTFT (first content event), time of terminal event, time of EOF.
import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "", "")
	keys := flag.String("keys", "mock", "comma separated bearer keys")
	api := flag.String("api", "responses", "responses|chat|decisions-openai|decisions-legacy")
	model := flag.String("model", "gpt-mock", "")
	conc := flag.Int("c", 1, "")
	rate := flag.Int("rps", 0, "offered requests per second; 0 uses closed-loop workers")
	dur := flag.Duration("d", 20*time.Second, "")
	kb := flag.Int("kb", 1, "prompt size KB")
	stream := flag.Bool("stream", true, "")
	label := flag.String("label", "", "")
	expectedContent := flag.Int("expected-content-events", 100, "expected synthetic content events; 0 validates presence only")
	jsonOutput := flag.Bool("json", false, "emit summary as JSON")
	uniquePrompt := flag.Bool("unique-prompt", false, "give every request unique prompt text; disables exact prompt memo hits")
	sampleFile := flag.String("samples", "", "write request-id and EOF-time samples, without bodies or credentials")
	flag.Parse()
	decisionMode := *api == "decisions-openai" || *api == "decisions-legacy"
	if decisionMode && *stream {
		fmt.Fprintln(os.Stderr, "Decisions requires -stream=false")
		os.Exit(2)
	}
	if *url == "" || *conc < 1 || *dur <= 0 || *kb < 1 || *rate < 0 || *rate > 10000 || *expectedContent < 0 {
		fmt.Fprintln(os.Stderr, "url, concurrency, duration and body size must be positive")
		os.Exit(2)
	}
	ks := strings.Split(*keys, ",")
	nonce := ""
	if *uniquePrompt {
		var err error
		nonce, err = benchmarkRunNonce()
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not initialize unique prompt nonce:", err)
			os.Exit(1)
		}
	}
	body := makeBenchmarkBody(*api, *model, *stream, *kb, nonce)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 4096, DisableCompression: true}, Timeout: 120 * time.Second}
	type res struct {
		ttft, last, eof float64
		code            int
		started         float64
		errorSample     string
		requestID       string
		ended           int64
		events          int
		scheduleLag     float64
		bodyBytes       int
	}
	var mu sync.Mutex
	var all []res
	benchmarkStart := time.Now()
	deadline := benchmarkStart.Add(*dur)
	loadCtx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	var pacer *arrivalPacer
	if *rate > 0 {
		pacer = newArrivalPacer(benchmarkStart, *dur, *rate)
	}
	var wg sync.WaitGroup
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				scheduled := time.Time{}
				if pacer != nil {
					var ok bool
					scheduled, ok = pacer.wait(loadCtx)
					if !ok {
						return
					}
				}
				var requestBytes []byte
				if *uniquePrompt {
					requestBytes = body.payload(w, i)
				} else {
					requestBytes = []byte(body.text)
				}
				req, _ := http.NewRequest("POST", *url, bytes.NewReader(requestBytes))
				req.Header.Set("Authorization", "Bearer "+ks[(w+i)%len(ks)])
				req.Header.Set("Content-Type", "application/json")
				t0 := time.Now()
				if pacer != nil && (!t0.Before(deadline) || loadCtx.Err() != nil) {
					return
				}
				lag := 0.0
				if !scheduled.IsZero() {
					lag = float64(t0.Sub(scheduled).Nanoseconds()) / float64(time.Millisecond)
				}
				resp, err := client.Do(req)
				if err != nil {
					mu.Lock()
					all = append(all, res{code: 0, started: t0.Sub(benchmarkStart).Seconds(), bodyBytes: len(requestBytes), scheduleLag: lag})
					mu.Unlock()
					continue
				}
				r := res{code: resp.StatusCode, started: t0.Sub(benchmarkStart).Seconds(), requestID: resp.Header.Get("X-Request-Id"), bodyBytes: len(requestBytes), scheduleLag: lag}
				if resp.StatusCode != http.StatusOK {
					payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
					var failure struct {
						Error struct {
							Code    interface{} `json:"code"`
							Message string      `json:"message"`
						} `json:"error"`
					}
					_ = json.Unmarshal(payload, &failure)
					r.errorSample = fmt.Sprintf("%v: %s", failure.Error.Code, failure.Error.Message)
					if len(r.errorSample) > 300 {
						r.errorSample = r.errorSample[:300]
					}
				}
				var facts fixtureStreamFacts
				if decisionMode && r.code == http.StatusOK {
					if err := validateFixtureDecision(resp.Body, *api, *model); err != nil {
						r.code, r.errorSample = 0, err.Error()
					}
				} else {
					facts = readFixtureStream(resp.Body, func() float64 { return float64(time.Since(t0).Microseconds()) / 1000 })
				}
				r.ttft, r.last, r.events = facts.first, facts.terminal, facts.events
				if r.code == http.StatusOK {
					if err := facts.completionError(*stream, *expectedContent); err != nil {
						r.code, r.errorSample = 0, err.Error()
					} else if *sampleFile != "" && r.requestID == "" {
						r.code, r.errorSample = 0, "successful gateway response has no request ID"
					}
				}
				resp.Body.Close()
				r.eof = float64(time.Since(t0).Microseconds()) / 1000
				r.ended = time.Now().UnixMilli()
				if !*stream {
					r.ttft, r.last = r.eof, r.eof
				}
				mu.Lock()
				all = append(all, r)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if *sampleFile != "" {
		file, err := os.OpenFile(*sampleFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		encoder := json.NewEncoder(file)
		for _, row := range all {
			if row.code == http.StatusOK && row.requestID != "" {
				if err := encoder.Encode(struct {
					RequestID string  `json:"request_id"`
					EOFMillis int64   `json:"eof_ms"`
					TailMS    float64 `json:"tail_ms"`
					TTFTMS    float64 `json:"ttft_ms"`
				}{RequestID: row.requestID, EOFMillis: row.ended, TailMS: row.eof - row.last, TTFTMS: row.ttft}); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			}
		}
		if err := file.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	pick := func(f func(res) float64) (float64, float64, float64) {
		v := []float64{}
		for _, r := range all {
			if r.code == 200 {
				v = append(v, f(r))
			}
		}
		if len(v) == 0 {
			return 0, 0, 0
		}
		sort.Float64s(v)
		return v[len(v)/2], v[int(0.95*float64(len(v)-1))], v[len(v)-1]
	}
	codes := map[int]int{}
	errorSamples := map[string]int{}
	bodyMin, bodyMax := 0, 0
	for _, r := range all {
		codes[r.code]++
		if bodyMin == 0 || r.bodyBytes < bodyMin {
			bodyMin = r.bodyBytes
		}
		if r.bodyBytes > bodyMax {
			bodyMax = r.bodyBytes
		}
		if r.errorSample != "" {
			errorSamples[r.errorSample]++
		}
	}
	a, b, c := pick(func(r res) float64 { return r.ttft })
	d, e, _ := pick(func(r res) float64 { return r.last })
	g, h, tailMax := pick(func(r res) float64 { return r.eof - r.last })
	if *jsonOutput {
		offered, missed := len(all), int64(0)
		if pacer != nil {
			offered = int(pacer.offered)
			missed = pacer.offered - int64(len(all))
		}
		lags := make([]float64, 0, len(all))
		for _, row := range all {
			lags = append(lags, row.scheduleLag)
		}
		sort.Float64s(lags)
		lag50, lag95, lagMax := 0.0, 0.0, 0.0
		if len(lags) > 0 {
			lag50, lag95, lagMax = lags[len(lags)/2], lags[int(.95*float64(len(lags)-1))], lags[len(lags)-1]
		}
		windows := make([]map[string]interface{}, 0)
		for start := 0.0; start < dur.Seconds(); start += 15 {
			var times []float64
			for _, row := range all {
				if row.code == 200 && row.started >= start && row.started < start+15 {
					times = append(times, row.ttft)
				}
			}
			sort.Float64s(times)
			var p95 interface{}
			if len(times) > 0 {
				p95 = times[int(.95*float64(len(times)-1))]
			}
			windows = append(windows, map[string]interface{}{"start_seconds": start, "samples": len(times), "ttft_p95_ms": p95})
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"label": *label, "concurrency": *conc, "offered_rps": *rate, "offered_requests": offered, "dispatched_requests": len(all), "started_requests": len(all), "missed_arrivals": missed, "schedule_lag_p50_ms": lag50, "schedule_lag_p95_ms": lag95, "schedule_lag_max_ms": lagMax, "duration_seconds": dur.Seconds(), "window_p95": windows, "requests_per_second": float64(len(all)) / dur.Seconds(), "kilobytes": *kb, "unique_prompt": *uniquePrompt, "request_body_bytes_min": bodyMin, "request_body_bytes_max": bodyMax, "requests": len(all), "ttft_p50_ms": a, "ttft_p95_ms": b, "ttft_max_ms": c, "done_p50_ms": d, "done_p95_ms": e, "tail_p50_ms": g, "tail_p95_ms": h, "tail_max_ms": tailMax, "status_codes": codes, "error_samples": errorSamples})
		return
	}
	fmt.Printf("%-28s c=%-4d kb=%-4d n=%-5d ttft p50=%7.1f p95=%7.1f max=%7.1f | done p50=%7.1f p95=%7.1f | tail(eof-done) p50=%6.1f p95=%6.1f | %v\n", *label, *conc, *kb, len(all), a, b, c, d, e, g, h, codes)
}
