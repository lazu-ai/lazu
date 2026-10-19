package publicsite

import (
	"encoding/json"
	"fmt"
	"github.com/lazu-ai/lazu/pkg/billing"
	"math"
	"sort"
	"strings"
	"time"
)

// The shapes below mirror the public catalog (/models.json), the same payload
// the console's model plaza reads (apps/web/src/pages/Routes/catalog.ts), so
// the two surfaces price and colour a lane identically.

type Pricing struct {
	ImagePricing           *billing.ImagePrices `json:"image_pricing,omitempty"`
	BillingType            string               `json:"billing_type,omitempty"`
	Input                  *float64             `json:"input,omitempty"`
	InputPerMillion        *float64             `json:"input_per_million,omitempty"`
	Output                 *float64             `json:"output,omitempty"`
	OutputPerMillion       *float64             `json:"output_per_million,omitempty"`
	CacheRead              *float64             `json:"cache_read,omitempty"`
	CacheReadPerMillion    *float64             `json:"cache_read_per_million,omitempty"`
	CacheWrite5m           *float64             `json:"cache_write_5m,omitempty"`
	CacheWrite5mPerMillion *float64             `json:"cache_write_5m_per_million,omitempty"`
	Request                *float64             `json:"request,omitempty"`
	RequestPrice           *float64             `json:"request_price,omitempty"`
	Second                 *float64             `json:"second,omitempty"`
	SecondPrice            *float64             `json:"second_price,omitempty"`
	PricingVersion         int64                `json:"pricing_version,omitempty"`
	ContextTier            *ContextTier         `json:"context_tier,omitempty"`
}

// ContextTier is a lane's long-context price: once a request's input (cache
// reads and writes included) passes Threshold tokens, the whole request is
// billed at these prices per million tokens. Nil prices keep the base price.
type ContextTier struct {
	Threshold    int64    `json:"threshold"`
	Input        *float64 `json:"input,omitempty"`
	Output       *float64 `json:"output,omitempty"`
	CacheRead    *float64 `json:"cache_read,omitempty"`
	CacheWrite5m *float64 `json:"cache_write_5m,omitempty"`
}

type HealthBucket struct {
	Index       int      `json:"index"`
	StartAt     string   `json:"start_at"`
	Source      string   `json:"source"`
	SuccessRate *float64 `json:"success_rate,omitempty"`
	Tone        string   `json:"tone"`
}

type Health struct {
	Status        string         `json:"status,omitempty"`
	Availability  *float64       `json:"availability,omitempty"`
	LatencyP95Ms  float64        `json:"latency_p95_ms,omitempty"`
	OutputTPS     float64        `json:"output_tps,omitempty"`
	BucketSeconds int            `json:"bucket_seconds,omitempty"`
	Buckets       []HealthBucket `json:"buckets,omitempty"`
}

type Lane struct {
	Key              string   `json:"key"`
	Available        *bool    `json:"available,omitempty"`
	Pricing          *Pricing `json:"pricing,omitempty"`
	InputPerMillion  *float64 `json:"input_per_million,omitempty"`
	OutputPerMillion *float64 `json:"output_per_million,omitempty"`
	PerCall          *float64 `json:"per_call,omitempty"`
	PerSecond        *float64 `json:"per_second,omitempty"`
	Health           *Health  `json:"health,omitempty"`
}

type Endpoint struct {
	Type   string `json:"type,omitempty"`
	Path   string `json:"path,omitempty"`
	Method string `json:"method,omitempty"`
}

type Model struct {
	ID                  string          `json:"id"`
	Maker               string          `json:"maker,omitempty"`
	MakerRank           *int            `json:"maker_rank,omitempty"`
	Family              string          `json:"family,omitempty"`
	Description         string          `json:"description,omitempty"`
	DescriptionEn       string          `json:"description_en,omitempty"`
	DescriptionZh       string          `json:"description_zh,omitempty"`
	ContextLength       int64           `json:"context_length,omitempty"`
	MaxOutputTokens     int64           `json:"max_output_tokens,omitempty"`
	KnowledgeCutoff     string          `json:"knowledge_cutoff,omitempty"`
	ReleaseDate         string          `json:"release_date,omitempty"`
	ReleasedAt          int64           `json:"released_at,omitempty"`
	IsNewRelease        bool            `json:"is_new_release,omitempty"`
	InputModalities     []string        `json:"input_modalities,omitempty"`
	OutputModalities    []string        `json:"output_modalities,omitempty"`
	Capabilities        map[string]bool `json:"capabilities,omitempty"`
	SupportedEndpoints  []Endpoint      `json:"supported_endpoints,omitempty"`
	DefaultEndpointType string          `json:"default_endpoint_type,omitempty"`
	UnsupportedParams   []string        `json:"unsupported_params,omitempty"`
	ReasoningOptions    *struct {
		Efforts []string `json:"efforts,omitempty"`
	} `json:"reasoning_options,omitempty"`
	Pricing *Pricing `json:"pricing,omitempty"`
	Lanes   []Lane   `json:"lanes,omitempty"`
	Health  *Health  `json:"health,omitempty"`

	// Official list price of the model's maker, USD per million tokens, from
	// the pricing table (not part of /models.json).
	OfficialInput      float64  `json:"-"`
	OfficialOutput     float64  `json:"-"`
	OfficialCacheRead  *float64 `json:"-"`
	OfficialCacheWrite *float64 `json:"-"`
	// OfficialTier is the maker's long-context list price, when it has one.
	OfficialTier *ContextTier `json:"-"`
	// LastModified is when the model's page content last changed (catalog
	// row, sell price or discount); zero when unknown.
	LastModified time.Time `json:"-"`
}

// Catalog is the parsed public catalog plus when it was read.
type Catalog struct {
	Models    []Model
	UpdatedAt time.Time
}

// ParseCatalog reads the /models.json body.
func ParseCatalog(body []byte) (Catalog, error) {
	var payload struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Catalog{}, err
	}
	return Catalog{Models: payload.Data, UpdatedAt: time.Now().UTC()}, nil
}

// Find returns the model with this id.
func (c Catalog) Find(id string) (Model, bool) {
	for _, model := range c.Models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

// ----- makers --------------------------------------------------------------

// Labels copied from apps/web/src/lib/model-makers.ts (MAKER_LABEL) and the
// page.console.makers.* translations.
var makerNames = map[string]string{
	"alibaba":   "Qwen",
	"anthropic": "Anthropic",
	"bytedance": "ByteDance · Seed",
	"cohere":    "Cohere",
	"deepseek":  "DeepSeek",
	"google":    "Google",
	"meta":      "Meta",
	"minimax":   "MiniMax",
	"mistral":   "Mistral",
	"moonshot":  "Kimi",
	"openai":    "OpenAI",
	"stepfun":   "StepFun",
	"xai":       "xAI",
	"xiaomi":    "Xiaomi",
	"zhipu":     "Zhipu",
}

var makerNamesLocal = map[string]map[string]string{
	"zh":    {"zhipu": "智谱", "bytedance": "字节 · Seed", "xiaomi": "小米", "stepfun": "阶跃星辰"},
	"zh-TW": {"zhipu": "智譜", "bytedance": "字節 · Seed", "xiaomi": "小米", "stepfun": "階躍星辰"},
}

// Same order as apps/web/src/lib/model-makers.ts DEFAULT_MAKER_ORDER.
var makerOrder = []string{"anthropic", "openai", "google", "deepseek", "moonshot", "zhipu", "alibaba", "minimax", "xai"}

// MakerSlug is the URL segment for a maker ("anthropic").
func MakerSlug(maker string) string {
	slug := strings.ToLower(strings.TrimSpace(maker))
	if slug == "" {
		return "other"
	}
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// MakerName is how a maker reads in the locale.
func MakerName(maker, locale string) string {
	key := MakerSlug(maker)
	if name, ok := makerNamesLocal[locale][key]; ok {
		return name
	}
	if name, ok := makerNames[key]; ok {
		return name
	}
	if maker == "" {
		return "Other"
	}
	return strings.ToUpper(maker[:1]) + maker[1:]
}

func makerRank(model Model) int {
	if model.MakerRank != nil {
		return *model.MakerRank
	}
	slug := MakerSlug(model.Maker)
	for index, maker := range makerOrder {
		if maker == slug {
			return index
		}
	}
	return len(makerOrder)
}

// MakerGroup is one maker's models, in catalog order.
type MakerGroup struct {
	Slug   string
	Maker  string
	Models []Model
}

// GroupByMaker keeps the catalog's order within a maker and orders makers by
// their published rank, like the console plaza.
func GroupByMaker(models []Model) []MakerGroup {
	index := map[string]int{}
	var groups []MakerGroup
	for _, model := range models {
		slug := MakerSlug(model.Maker)
		position, ok := index[slug]
		if !ok {
			position = len(groups)
			index[slug] = position
			groups = append(groups, MakerGroup{Slug: slug, Maker: model.Maker})
		}
		groups[position].Models = append(groups[position].Models, model)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return makerRank(groups[i].Models[0]) < makerRank(groups[j].Models[0])
	})
	return groups
}

// ----- display name --------------------------------------------------------

// DisplayName turns an id into how the maker writes it: claude-opus-5-5 ->
// "Claude Opus 5.5", gpt-5.6-sol -> "GPT-5.6 Sol", gemini-3.5-flash ->
// "Gemini 3.5 Flash". Unknown shapes keep the id.
func DisplayName(id string) string {
	lower := strings.ToLower(id)
	parts := strings.Split(id, "-")
	title := func(word string) string {
		if word == "" {
			return word
		}
		return strings.ToUpper(word[:1]) + word[1:]
	}
	switch {
	case strings.HasPrefix(lower, "claude-"):
		// claude-opus-5-5 -> Claude Opus 5.5 ; claude-haiku-4-5-20251001 -> Claude Haiku 4.5
		words := []string{"Claude"}
		var version []string
		for _, part := range parts[1:] {
			if isDigits(part) && len(part) < 3 {
				version = append(version, part)
				continue
			}
			if isDigits(part) {
				continue // a dated snapshot suffix
			}
			words = append(words, title(part))
		}
		if len(version) > 0 {
			words = append(words, strings.Join(version, "."))
		}
		return strings.Join(words, " ")
	case strings.HasPrefix(lower, "gpt-"):
		// gpt-5.6-sol -> GPT-5.6 Sol ; gpt-4o-mini -> GPT-4o Mini
		if len(parts) < 2 {
			return strings.ToUpper(id)
		}
		out := "GPT-" + parts[1]
		for index, part := range parts[2:] {
			if isDigits(part) && len(part) == 4 {
				// A dated snapshot: gpt-5.4-nano-2026-03-17.
				return out + " (" + strings.Join(parts[2+index:], "-") + ")"
			}
			out += " " + title(part)
		}
		return out
	case strings.HasPrefix(lower, "gemini-"), strings.HasPrefix(lower, "deepseek-"),
		strings.HasPrefix(lower, "kimi-"), strings.HasPrefix(lower, "qwen"), strings.HasPrefix(lower, "seed-"), strings.HasPrefix(lower, "seedream-"):
		words := make([]string, 0, len(parts))
		for index, part := range parts {
			if index == 0 {
				switch strings.ToLower(part) {
				case "deepseek":
					words = append(words, "DeepSeek")
				default:
					words = append(words, title(part))
				}
				continue
			}
			if strings.HasPrefix(part, "k") && isDigits(strings.TrimPrefix(part, "k")) {
				words = append(words, strings.ToUpper(part[:1])+part[1:])
				continue
			}
			words = append(words, title(part))
		}
		return strings.Join(words, " ")
	case strings.HasPrefix(lower, "glm-"):
		return "GLM-" + strings.Join(parts[1:], " ")
	}
	return id
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ----- descriptions, windows, dates ---------------------------------------

// DescriptionOf prefers the locale's copy, like catalog.ts descriptionOf.
func DescriptionOf(model Model, locale string) string {
	ordered := []string{model.DescriptionEn, model.Description, model.DescriptionZh}
	if strings.HasPrefix(locale, "zh") {
		ordered = []string{model.DescriptionZh, model.DescriptionEn, model.Description}
	}
	for _, text := range ordered {
		if strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// TokenWindow reads "1M", "1.05M", "128k" (catalog.ts formatTokenWindow).
func TokenWindow(value int64) string {
	if value <= 0 {
		return ""
	}
	if value >= 1_000_000 {
		if value%1_048_576 == 0 {
			return fmt.Sprintf("%dM", value/1_048_576)
		}
		millions := math.Round(float64(value)/1_000_000*100) / 100
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", millions), "0"), ".") + "M"
	}
	if value >= 1000 {
		return fmt.Sprintf("%dk", int64(math.Round(float64(value)/1000)))
	}
	return fmt.Sprint(value)
}

// ReleaseDate is the model's release day, YYYY-MM-DD.
func ReleaseDate(model Model) string {
	if model.ReleaseDate != "" {
		return model.ReleaseDate
	}
	if model.ReleasedAt > 0 {
		return time.Unix(model.ReleasedAt, 0).UTC().Format("2006-01-02")
	}
	return ""
}

// ----- prices --------------------------------------------------------------

func firstOf(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// ListPrice is a pricing's per-token / per-call / per-second price.
type ListPrice struct {
	Input, Output, PerCall, PerSecond *float64
}

func listPriceOf(p *Pricing) ListPrice {
	if p == nil {
		return ListPrice{}
	}
	return ListPrice{
		Input:     firstOf(p.InputPerMillion, p.Input),
		Output:    firstOf(p.OutputPerMillion, p.Output),
		PerCall:   firstOf(p.Request, p.RequestPrice),
		PerSecond: firstOf(p.Second, p.SecondPrice),
	}
}

// LaneList is what a lane charges (catalog.ts laneListPrice).
func LaneList(lane Lane, model Model) ListPrice {
	price := listPriceOf(lane.Pricing)
	fallback := ListPrice{}
	if lane.Key == "stable" {
		fallback = listPriceOf(model.Pricing)
	}
	return ListPrice{
		Input:     firstOf(price.Input, lane.InputPerMillion, fallback.Input),
		Output:    firstOf(price.Output, lane.OutputPerMillion, fallback.Output),
		PerCall:   firstOf(price.PerCall, lane.PerCall, fallback.PerCall),
		PerSecond: firstOf(price.PerSecond, lane.PerSecond, fallback.PerSecond),
	}
}

// LaneTier returns the lane's long-context price, or nil. The stable lane
// falls back to the model-level price like LaneList does.
func LaneTier(lane Lane, model Model) *ContextTier {
	if lane.Pricing != nil && lane.Pricing.ContextTier != nil && lane.Pricing.ContextTier.Threshold > 0 {
		return lane.Pricing.ContextTier
	}
	if lane.Key == "stable" && lane.Pricing == nil && model.Pricing != nil &&
		model.Pricing.ContextTier != nil && model.Pricing.ContextTier.Threshold > 0 {
		return model.Pricing.ContextTier
	}
	return nil
}

// TierPrices fills a tier's missing dimensions from the base prices.
func TierPrices(tier *ContextTier, base ListPrice, read, write *float64) (input, output, cacheRead, cacheWrite *float64) {
	return firstOf(tier.Input, base.Input), firstOf(tier.Output, base.Output),
		firstOf(tier.CacheRead, read), firstOf(tier.CacheWrite5m, write)
}

// LaneCache returns the lane's cache read and 5-minute write prices.
func LaneCache(lane Lane, model Model) (read, write *float64) {
	pick := func(p *Pricing) (*float64, *float64) {
		if p == nil {
			return nil, nil
		}
		return firstOf(p.CacheReadPerMillion, p.CacheRead), firstOf(p.CacheWrite5mPerMillion, p.CacheWrite5m)
	}
	read, write = pick(lane.Pricing)
	if lane.Key == "stable" {
		r, w := pick(model.Pricing)
		if read == nil {
			read = r
		}
		if write == nil {
			write = w
		}
	}
	return read, write
}

// Lanes resolves the model's lanes (catalog.ts resolveLanes).
func Lanes(model Model) []Lane {
	if len(model.Lanes) > 0 {
		return model.Lanes
	}
	list := listPriceOf(model.Pricing)
	available := true
	return []Lane{{
		Key: "stable", Available: &available, Health: model.Health,
		InputPerMillion: list.Input, OutputPerMillion: list.Output,
		PerCall: list.PerCall, PerSecond: list.PerSecond,
	}}
}

// LaneOn reports whether the lane is open.
func LaneOn(lane Lane) bool { return lane.Available == nil || *lane.Available }

// Money formats USD like the plaza: $0.920 under a dollar, $4.00 above.
func Money(value float64) string {
	if value < 1 {
		return fmt.Sprintf("$%.3f", value)
	}
	return fmt.Sprintf("$%.2f", value)
}

// MoneyShort formats for sentences and tables: $0.92, $0.046, $4.00.
func MoneyShort(value float64) string {
	if value >= 1 {
		return fmt.Sprintf("$%.2f", value)
	}
	text := strings.TrimRight(fmt.Sprintf("%.5f", value), "0")
	if decimals := len(text) - strings.Index(text, ".") - 1; decimals < 2 {
		return fmt.Sprintf("$%.2f", value)
	}
	return "$" + text
}

// OfficialRatio is the lane price as a share of the model's list price
// (catalog.ts laneOfficialRatio): 0.23 reads "−77%".
func OfficialRatio(lane Lane, model Model) (float64, bool) {
	official := listPriceOf(model.Pricing)
	list := LaneList(lane, model)
	pairs := [][2]*float64{
		{list.PerCall, official.PerCall},
		{list.PerSecond, official.PerSecond},
		{list.Input, official.Input},
		{list.Output, official.Output},
	}
	for _, pair := range pairs {
		if pair[0] != nil && pair[1] != nil && *pair[1] > 0 {
			return *pair[0] / *pair[1], true
		}
	}
	return 0, false
}

// ----- health --------------------------------------------------------------

// Bar is one two-hour slice of a lane's week.
type Bar struct {
	Tone  string
	Title string
}

const weekSlices = 84

// Bars lays out the lane's week as 84 slices (lane-block.tsx LaneBars).
func Bars(health *Health, locale string) []Bar {
	count := weekSlices
	byIndex := map[int]HealthBucket{}
	if health != nil {
		if len(health.Buckets) > 0 {
			count = len(health.Buckets)
		}
		for _, bucket := range health.Buckets {
			byIndex[bucket.Index] = bucket
		}
	}
	bars := make([]Bar, count)
	for index := range bars {
		bucket, ok := byIndex[index]
		tone := "none"
		if ok && bucket.Source != "none" && bucket.Tone != "" {
			tone = bucket.Tone
		}
		title := T(locale, "legend."+tone)
		if ok && bucket.StartAt != "" {
			if at, err := time.Parse(time.RFC3339, bucket.StartAt); err == nil {
				title = at.UTC().Format("01-02 15:04") + " UTC · " + title
			}
		}
		if tone != "none" && bucket.SuccessRate != nil {
			title += fmt.Sprintf(" · %.1f%%", *bucket.SuccessRate*100)
		}
		bars[index] = Bar{Tone: tone, Title: title}
	}
	return bars
}

// LaneStatus is operational / degraded / outage / unknown.
func LaneStatus(health *Health) string {
	if health == nil {
		return "unknown"
	}
	switch strings.ToLower(health.Status) {
	case "unavailable", "down", "cooldown", "circuit_open":
		return "outage"
	case "degraded", "warning":
		return "degraded"
	case "available", "ok", "healthy":
		return "operational"
	}
	return "unknown"
}

// Uptime formats availability like status-bars.tsx formatUptime.
func Uptime(health *Health) string {
	if health == nil || health.Availability == nil {
		return "—"
	}
	value := *health.Availability
	if value >= 1 {
		return "100.0%"
	}
	percent := math.Min(value*100, 99.99)
	if value >= 0.99 {
		return fmt.Sprintf("%.2f%%", percent)
	}
	return fmt.Sprintf("%.1f%%", percent)
}
