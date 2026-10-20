package publicsite

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// SiteOrigin is the public origin every canonical URL is built on.
var SiteOrigin = "https://lazu.ai"

func init() { common.OnAppConfigLoaded(applySiteConfig) }

// applySiteConfig points the public site at this deployment: site.web_origin
// and site.api_origin when configured, otherwise the console's own origin.
// The lazu.ai defaults above only remain in tests that never load config.
func applySiteConfig() {
	SiteOrigin = common.SiteWebOrigin()
	if origin := common.AppConfig.Site.WebOrigin; origin != "" {
		SiteOrigin = origin
	}
	APIOrigin = SiteOrigin
	if origin := common.SiteAPIOrigin(); origin != "" {
		APIOrigin = origin
	}
}

// APIOrigin is the gateway base URL the code samples call.
var APIOrigin = "https://api.lazu.ai"

// ModelPath is the bare path of a model page (/models/anthropic/claude-opus-5-5).
func ModelPath(model Model) string {
	return "/models/" + MakerSlug(model.Maker) + "/" + model.ID
}

// ----- lane view -----------------------------------------------------------

type LaneView struct {
	Key       string
	Label     string
	Off       bool
	OffText   string
	MarkText  string
	MarkKind  string // "list" | "off" | "official"
	Price     string // hero figure
	Unit      string
	OutText   string // "· $20.00 out"
	StatusDot string // success | warning | danger | neutral
	Uptime    string
	Bars      []Bar
	Figures   string

	// Detail table cells.
	Input      string
	Output     string
	CacheRead  string
	CacheWrite string
	Flat       string // per-call / per-second price
}

// EcoHarness is the agent tool a maker's agent lane is meant for. The agent
// lane runs on third-party subscription pools that serve that tool's official
// client, so it is only recommended inside it.
func EcoHarness(model Model) string {
	switch strings.ToLower(model.Maker) {
	case "anthropic":
		return "Claude Code"
	case "openai":
		return "Codex"
	case "google":
		return "Antigravity"
	}
	return ""
}

func laneLabel(lane Lane, locale string) string {
	if lane.Key == "eco" {
		return T(locale, "lane.eco")
	}
	return T(locale, "lane.stable")
}

func statusDot(status string) string {
	switch status {
	case "operational":
		return "success"
	case "degraded":
		return "warning"
	case "outage":
		return "danger"
	}
	return "neutral"
}

func buildLane(lane Lane, model Model, locale string) LaneView {
	view := LaneView{Key: lane.Key, Label: laneLabel(lane, locale)}
	if !LaneOn(lane) {
		view.Off = true
		view.OffText = T(locale, "lane.off")
		return view
	}
	list := LaneList(lane, model)
	if ratio, ok := OfficialRatio(lane, model); ok {
		switch {
		case ratio > 0.995 && ratio < 1.005:
			view.MarkKind, view.MarkText = "list", T(locale, "mark.list")
		case ratio < 1:
			view.MarkKind, view.MarkText = "off", T(locale, "mark.off", int(roundHalf((1-ratio)*100)))
		}
	}
	switch {
	case list.PerCall != nil:
		view.Price, view.Unit = Money(*list.PerCall), T(locale, "unit.call")
		view.Flat = MoneyShort(*list.PerCall)
		if p := laneImagePrices(lane, model); p != nil {
			view.Unit = "/ " + T(locale, "unit.image")
			if p.HighResolutionOutputUSD != nil {
				view.Price += "+"
				view.Flat += "+"
			}
		}
	case list.PerSecond != nil:
		view.Price, view.Unit = Money(*list.PerSecond), T(locale, "unit.second")
		view.Flat = MoneyShort(*list.PerSecond)
		if p := laneVideoPrices(lane, model); p != nil && len(p.ResolutionSecondUSD) > 1 {
			// The cheapest tier, marked as a starting price as image tiers are.
			view.Price += "+"
			view.Flat += "+"
		}
	case list.Input != nil:
		view.Price, view.Unit = Money(*list.Input), T(locale, "unit.in")
		if list.Output != nil {
			view.OutText = fmt.Sprintf("· %s %s", Money(*list.Output), T(locale, "unit.out"))
		}
	case list.Output != nil:
		view.Price, view.Unit = Money(*list.Output), T(locale, "unit.outHero")
	}
	if list.Input != nil {
		view.Input = MoneyShort(*list.Input)
	}
	if list.Output != nil {
		view.Output = MoneyShort(*list.Output)
	}
	read, write := LaneCache(lane, model)
	if read != nil {
		view.CacheRead = MoneyShort(*read)
	}
	if write != nil {
		view.CacheWrite = MoneyShort(*write)
	}
	view.StatusDot = statusDot(LaneStatus(lane.Health))
	view.Uptime = Uptime(lane.Health)
	view.Bars = Bars(lane.Health, locale)
	var figures []string
	if lane.Health != nil {
		if ms := lane.Health.LatencyP95Ms; ms > 0 {
			if ms >= 1000 {
				figures = append(figures, fmt.Sprintf("p95 %.2fs", ms/1000))
			} else {
				figures = append(figures, fmt.Sprintf("p95 %dms", int(ms+0.5)))
			}
		}
		if tps := lane.Health.OutputTPS; tps > 0 {
			figures = append(figures, fmt.Sprintf("%.0f tok/s", tps))
		}
	}
	view.Figures = "—"
	if len(figures) > 0 {
		view.Figures = strings.Join(figures, " · ")
	}
	return view
}

func roundHalf(value float64) float64 { return float64(int(value + 0.5)) }

// ----- catalog page ----------------------------------------------------------

type CardView struct {
	ID      string
	Name    string
	Href    string
	IsNew   bool
	Meta    string
	Search  string
	Lanes   []LaneView
	Summary string
}

type GroupView struct {
	Slug  string
	Name  string
	Count string
	Cards []CardView
}

type VendorChip struct {
	Slug  string
	Name  string
	Count int
}

type ModelsPage struct {
	Eyebrow   string
	Title     string
	Lede      string
	Updated   string
	UpdatedAt string
	Total     int
	Vendors   []VendorChip
	Groups    []GroupView
	JSONHref  string
	MDHref    string
}

func cardMeta(model Model, locale string) string {
	var parts []string
	if date := ReleaseDate(model); date != "" {
		parts = append(parts, T(locale, "models.released", date))
	}
	if window := TokenWindow(model.ContextLength); window != "" {
		parts = append(parts, T(locale, "models.context", window))
	}
	return strings.Join(parts, " · ")
}

func topMakers(groups []MakerGroup, locale string, limit int) []string {
	names := make([]string, 0, limit)
	for _, group := range groups {
		if len(names) == limit {
			break
		}
		names = append(names, MakerName(group.Maker, locale))
	}
	return names
}

// BuildModelsPage lays out the catalog for a locale.
func BuildModelsPage(catalog Catalog, locale string) ModelsPage {
	groups := GroupByMaker(catalog.Models)
	makers := Join(locale, topMakers(groups, locale, 4))
	page := ModelsPage{
		Eyebrow:   T(locale, "models.eyebrow", len(catalog.Models)),
		Title:     T(locale, "models.title"),
		Lede:      T(locale, "models.lede", len(catalog.Models), makers, len(groups)),
		Updated:   T(locale, "models.updated"),
		UpdatedAt: catalog.UpdatedAt.Format("2006-01-02 15:04") + " UTC",
		Total:     len(catalog.Models),
		JSONHref:  "/models.json",
		MDHref:    "/models.md",
	}
	for _, group := range groups {
		name := MakerName(group.Maker, locale)
		page.Vendors = append(page.Vendors, VendorChip{Slug: group.Slug, Name: name, Count: len(group.Models)})
		view := GroupView{Slug: group.Slug, Name: name, Count: T(locale, "models.groupCount", len(group.Models))}
		for _, model := range group.Models {
			card := CardView{
				ID:     model.ID,
				Name:   model.ID,
				Href:   LocalizedPath(ModelPath(model), locale),
				IsNew:  model.IsNewRelease,
				Meta:   cardMeta(model, locale),
				Search: strings.ToLower(model.ID + " " + DisplayName(model.ID) + " " + name + " " + model.Family),
			}
			if strings.HasPrefix(model.ID, "seed-") || strings.HasPrefix(model.ID, "seedream-") {
				card.Name = DisplayName(model.ID)
			}
			for _, lane := range Lanes(model) {
				view := buildLane(lane, model, locale)
				// Sixty cards of 84 slices each: the per-slice tooltip stays
				// on the model page to keep the catalog light.
				for index := range view.Bars {
					view.Bars[index].Title = ""
				}
				card.Lanes = append(card.Lanes, view)
			}
			view.Cards = append(view.Cards, card)
		}
		page.Groups = append(page.Groups, view)
	}
	return page
}

// ----- model page -------------------------------------------------------------

type Fact struct {
	Label string
	Value string
	Mono  bool
	Big   bool
}

type PriceRow struct {
	Label      string
	Tier       bool // a long-context sub-row under the row above
	Chip       string
	ChipKind   string
	Muted      bool
	Input      string
	Output     string
	CacheRead  string
	CacheWrite string
	Flat       string
	Uptime     string
	Bars       []Bar
}

type Sample struct {
	ID       string
	Label    string
	Code     template.HTML
	Raw      string
	Selected bool
}

type FAQ struct {
	Q string
	A string
}

type Related struct {
	ID     string
	Href   string
	Date   string
	Prices string
}

type ModelPage struct {
	ID             string
	Name           string
	MakerName      string
	MakerHref      string
	ModelsHref     string
	Eyebrow        string
	Status         string
	StatusKind     string
	IsNew          bool
	Lede           template.HTML
	Description    string
	Facts          []Fact
	PerImage       bool
	PerToken       bool
	HasCache       bool
	ImagePriceRows []ImagePriceRow
	VideoPriceRows []ImagePriceRow
	PriceRows      []PriceRow
	PricesNote     string
	Updated        string
	JSONHref       string
	MDHref         string
	Capabilities   []string
	Efforts        []string
	Unsupported    []string
	Endpoint       string
	Samples        []Sample
	SampleNote     template.HTML
	// IsVideo marks a model called through the video job API; DocsLabel
	// names the guide its DocsHref points at ("" reads "Full integration docs").
	IsVideo     bool
	DocsLabel   string
	DocsHref    string
	FAQs        []FAQ
	Related     []Related
	AgentPrompt string
	Actions     []ModelAction
}

var capabilityOrder = []string{
	"supports_function_calling", "supports_vision", "supports_reasoning", "supports_response_schema",
	"supports_prompt_caching", "supports_pdf_input", "supports_web_search", "supports_audio_input",
	"supports_audio_output", "supports_computer_use", "supports_parallel_function_calling",
}

// Capabilities lists the model's capability labels in a stable order.
func Capabilities(model Model, locale string) []string {
	var out []string
	for _, key := range capabilityOrder {
		if model.Capabilities[key] {
			out = append(out, T(locale, "cap."+strings.TrimPrefix(key, "supports_")))
		}
	}
	return out
}

func modalities(values []string, locale string) string {
	words := make([]string, 0, len(values))
	for _, value := range values {
		label := T(locale, "modality."+strings.ToLower(value))
		if strings.HasPrefix(label, "modality.") {
			label = value
		}
		words = append(words, label)
	}
	return strings.Join(words, " · ")
}

func endpointPaths(model Model) string {
	seen := map[string]bool{}
	var paths []string
	for _, endpoint := range model.SupportedEndpoints {
		if endpoint.Path == "" || seen[endpoint.Path] {
			continue
		}
		seen[endpoint.Path] = true
		paths = append(paths, endpoint.Path)
	}
	return strings.Join(paths, " · ")
}

// DefaultEndpoint is the endpoint the samples call.
func DefaultEndpoint(model Model) Endpoint {
	for _, endpoint := range model.SupportedEndpoints {
		if endpoint.Type == model.DefaultEndpointType {
			return endpoint
		}
	}
	if len(model.SupportedEndpoints) > 0 {
		return model.SupportedEndpoints[0]
	}
	return Endpoint{Type: "openai", Path: "/v1/chat/completions", Method: "POST"}
}

func findLane(model Model, key string) (Lane, bool) {
	for _, lane := range Lanes(model) {
		if lane.Key == key && LaneOn(lane) {
			return lane, true
		}
	}
	return Lane{}, false
}

func bold(value string) string { return "<b>" + template.HTMLEscapeString(value) + "</b>" }
func code(value string) string {
	return `<span class="lazu-site-mono">` + template.HTMLEscapeString(value) + "</span>"
}

func buildLede(model Model, locale string) template.HTML {
	makerName := MakerName(model.Maker, locale)
	stable, hasStable := findLane(model, "stable")
	eco, hasEco := findLane(model, "eco")
	var sentence string
	escape := template.HTMLEscapeString
	if hasStable {
		list := LaneList(stable, model)
		switch {
		case list.Input != nil && list.Output != nil:
			officialNote := ""
			if model.OfficialInput > 0 && sameMoney(*list.Input, model.OfficialInput) &&
				(model.OfficialOutput <= 0 || sameMoney(*list.Output, model.OfficialOutput)) {
				officialNote = escape(T(locale, "model.ledeOfficial", makerName))
			}
			if hasEco {
				ecoList := LaneList(eco, model)
				if ecoList.Input != nil && ecoList.Output != nil {
					cheaper := ""
					if *list.Input > 0 && *ecoList.Input < *list.Input {
						cheaper = escape(T(locale, "model.ledeCheaper", int(roundHalf((1 - *ecoList.Input / *list.Input)*100))))
					}
					sentence = fmt.Sprintf(escape(T(locale, "model.ledeTokens", "%s", "%s", "%s", "%s", "%s", "%s", "%s")),
						code(model.ID), bold(MoneyShort(*list.Input)), bold(MoneyShort(*list.Output)), officialNote,
						bold(MoneyShort(*ecoList.Input)), bold(MoneyShort(*ecoList.Output)), cheaper)
					break
				}
			}
			sentence = fmt.Sprintf(escape(T(locale, "model.ledeStableOnly", "%s", "%s", "%s", "%s")),
				code(model.ID), bold(MoneyShort(*list.Input)), bold(MoneyShort(*list.Output)), officialNote)
		case list.PerCall != nil:
			view := buildLane(stable, model, locale)
			sentence = fmt.Sprintf(escape(T(locale, "model.ledeFlat", "%s", "%s")), code(model.ID), bold(view.Flat+" "+view.Unit))
		case list.PerSecond != nil:
			sentence = fmt.Sprintf(escape(T(locale, "model.ledeFlat", "%s", "%s")), code(model.ID), bold(MoneyShort(*list.PerSecond)+" "+T(locale, "unit.second")))
		}
	}
	specs := ""
	window, output := TokenWindow(model.ContextLength), TokenWindow(model.MaxOutputTokens)
	if window != "" && output != "" {
		supports := ""
		if caps := Capabilities(model, locale); len(caps) > 0 {
			if len(caps) > 4 {
				caps = caps[:4]
			}
			supports = T(locale, "model.ledeSupports", Join(locale, caps))
		}
		specs = escape(T(locale, "model.ledeSpecs", window, output, supports))
		if locale == "en" {
			specs = strings.TrimSpace(specs)
			if sentence != "" {
				specs = " " + specs
			}
		}
	}
	if hasEco && sentence != "" {
		if harness := EcoHarness(model); harness != "" {
			sentence += escape(T(locale, "model.ledeEco", harness))
		}
	}
	return template.HTML(sentence + specs)
}

func sameMoney(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.0005
}

func relatedPrices(model Model, locale string) string {
	stable, hasStable := findLane(model, "stable")
	eco, hasEco := findLane(model, "eco")
	pair := func(lane Lane) string {
		list := LaneList(lane, model)
		switch {
		case list.Input != nil && list.Output != nil:
			return MoneyShort(*list.Input) + " / " + MoneyShort(*list.Output)
		case list.PerCall != nil:
			view := buildLane(lane, model, locale)
			return view.Flat + " " + view.Unit
		case list.PerSecond != nil:
			return MoneyShort(*list.PerSecond) + " " + T(locale, "unit.second")
		}
		return "—"
	}
	if hasStable && hasEco {
		return T(locale, "related.prices", pair(stable), pair(eco))
	}
	if hasStable {
		return T(locale, "related.pricesStable", pair(stable))
	}
	if hasEco {
		return pair(eco)
	}
	return ""
}

// RelatedModels are same-maker, same-family models, newest first.
func RelatedModels(catalog Catalog, model Model, limit int) []Model {
	var out []Model
	for _, other := range catalog.Models {
		if other.ID == model.ID || MakerSlug(other.Maker) != MakerSlug(model.Maker) {
			continue
		}
		if model.Family != "" && other.Family != model.Family {
			continue
		}
		out = append(out, other)
	}
	sort.SliceStable(out, func(i, j int) bool { return ReleaseDate(out[i]) > ReleaseDate(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// BuildModelPage lays out one model for a locale.
func BuildModelPage(catalog Catalog, model Model, locale string, docsBase string) ModelPage {
	makerName := MakerName(model.Maker, locale)
	path := ModelPath(model)
	page := ModelPage{
		ID:          model.ID,
		Name:        DisplayName(model.ID),
		MakerName:   makerName,
		MakerHref:   LocalizedPath("/models", locale) + "#" + MakerSlug(model.Maker),
		ModelsHref:  LocalizedPath("/models", locale),
		IsNew:       model.IsNewRelease,
		Lede:        buildLede(model, locale),
		Description: DescriptionOf(model, locale),
		Updated:     T(locale, "prices.updated", catalog.UpdatedAt.Format("2006-01-02 15:04")+" UTC"),
		PricesNote:  T(locale, "prices.note"),
		JSONHref:    path + ".json",
		MDHref:      path + ".md",
		DocsHref:    docsBase,
		Actions:     modelPageActions(model, locale),
	}
	if date := ReleaseDate(model); date != "" {
		page.Eyebrow = T(locale, "model.eyebrow", makerName, date)
	} else {
		page.Eyebrow = T(locale, "model.eyebrowNoDate", makerName)
	}
	status := "unknown"
	for _, lane := range Lanes(model) {
		if !LaneOn(lane) {
			continue
		}
		laneStatus := LaneStatus(lane.Health)
		if laneStatus == "operational" || status == "unknown" {
			status = laneStatus
		}
		if status == "operational" {
			break
		}
	}
	switch status {
	case "outage":
		page.Status, page.StatusKind = T(locale, "status.unavailable"), "danger"
	case "degraded":
		page.Status, page.StatusKind = T(locale, "status.degraded"), "warning"
	default:
		page.Status, page.StatusKind = T(locale, "status.available"), "success"
	}

	if window := TokenWindow(model.ContextLength); window != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.context"), Value: window, Big: true})
	}
	if output := TokenWindow(model.MaxOutputTokens); output != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.maxOutput"), Value: output, Big: true})
	}
	if model.KnowledgeCutoff != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.cutoff"), Value: model.KnowledgeCutoff, Big: true})
	}
	if input := modalities(model.InputModalities, locale); input != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.input"), Value: input})
	}
	if output := modalities(model.OutputModalities, locale); output != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.output"), Value: output})
	}
	if paths := endpointPaths(model); paths != "" {
		page.Facts = append(page.Facts, Fact{Label: T(locale, "fact.endpoints"), Value: paths, Mono: true})
	}

	// Price rows: the maker's official price (when the table knows it), then
	// each lane with its week.
	lanes := Lanes(model)
	page.PerToken = true
	for _, lane := range lanes {
		list := LaneList(lane, model)
		if list.Input == nil && list.Output == nil && (list.PerCall != nil || list.PerSecond != nil) {
			page.PerToken = false
		}
	}
	tiered := false
	page.HasCache = model.OfficialCacheRead != nil || model.OfficialCacheWrite != nil
	if page.PerToken && model.OfficialInput > 0 {
		row := PriceRow{Label: T(locale, "prices.official", makerName), Muted: true,
			Input: MoneyShort(model.OfficialInput), CacheRead: publicPrice(model.OfficialCacheRead), CacheWrite: publicPrice(model.OfficialCacheWrite), Uptime: "—"}
		row.Output = "—"
		if model.OfficialOutput > 0 {
			row.Output = MoneyShort(model.OfficialOutput)
		}
		page.PriceRows = append(page.PriceRows, row)
		if tier := model.OfficialTier; tier != nil && tier.Threshold > 0 {
			base := ListPrice{Input: &model.OfficialInput}
			if model.OfficialOutput > 0 {
				base.Output = &model.OfficialOutput
			}
			row := tierRow(tier, base, model.OfficialCacheRead, model.OfficialCacheWrite, true, locale)
			page.PriceRows = append(page.PriceRows, row)
			tiered = true
		}
	}
	for _, lane := range lanes {
		view := buildLane(lane, model, locale)
		page.ImagePriceRows = append(page.ImagePriceRows, ImagePriceRows(laneImagePrices(lane, model), locale)...)
		if len(page.VideoPriceRows) == 0 {
			page.VideoPriceRows = VideoPriceRows(laneVideoPrices(lane, model), locale)
		}
		page.PerImage = page.PerImage || laneImagePrices(lane, model) != nil
		row := PriceRow{Label: view.Label, Uptime: view.Uptime, Bars: view.Bars,
			Input: dash(view.Input), Output: dash(view.Output), CacheRead: dash(view.CacheRead),
			CacheWrite: dash(view.CacheWrite), Flat: dash(view.Flat)}
		if view.Off {
			row.Chip, row.ChipKind = view.OffText, "muted"
			row.Uptime = "—"
			row.Bars = nil
		}
		list := LaneList(lane, model)
		if !view.Off && model.OfficialInput > 0 && list.Input != nil {
			ratio := *list.Input / model.OfficialInput
			switch {
			case ratio > 0.995 && ratio < 1.005:
				row.Chip, row.ChipKind = T(locale, "mark.official"), "plain"
			case ratio < 1:
				row.Chip, row.ChipKind = T(locale, "mark.off", int(roundHalf((1-ratio)*100))), "ink"
			}
		} else if !view.Off && view.MarkKind == "off" {
			row.Chip, row.ChipKind = view.MarkText, "ink"
		}
		if view.CacheRead != "" || view.CacheWrite != "" {
			page.HasCache = true
		}
		page.PriceRows = append(page.PriceRows, row)
		if tier := LaneTier(lane, model); tier != nil && !view.Off && page.PerToken {
			read, write := LaneCache(lane, model)
			page.PriceRows = append(page.PriceRows, tierRow(tier, list, read, write, false, locale))
			tiered = true
		}
	}
	if tiered {
		page.PricesNote += " " + T(locale, "prices.tierNote")
	}

	page.Capabilities = Capabilities(model, locale)
	if model.ReasoningOptions != nil {
		page.Efforts = model.ReasoningOptions.Efforts
	}
	page.Unsupported = model.UnsupportedParams

	endpoint := DefaultEndpoint(model)
	method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if method == "" {
		method = "POST"
	}
	page.Endpoint = method + " " + endpoint.Path
	for index, language := range SampleLanguages {
		raw := BuildSnippet(language.Key, endpoint, model.ID, APIOrigin)
		page.Samples = append(page.Samples, Sample{
			ID: "sample-" + language.Key, Label: language.Label, Raw: raw,
			Code: Highlight(raw, language.Key), Selected: index == 0,
		})
	}
	page.SampleNote = template.HTML(fmt.Sprintf(template.HTMLEscapeString(T(locale, "sample.note", "%s", "%s")),
		code("base_url"), code(APIOrigin+"/v1")))

	native := ""
	if endpoint.Type != "openai" && endpoint.Path != "" && endpoint.Type != "" {
		native = T(locale, "faq.sdkNative", endpointLabel(endpoint.Type), endpoint.Path)
	}
	page.FAQs = []FAQ{
		{Q: T(locale, "faq.lanesQ"), A: T(locale, "faq.lanesA")},
		{Q: T(locale, "faq.sdkQ", model.ID), A: strings.TrimSpace(T(locale, "faq.sdkA", model.ID, native))},
		{Q: T(locale, "faq.billingQ"), A: T(locale, "faq.billingA")},
	}
	if IsVideoEndpoint(endpoint) {
		// A video is a job billed by the second, not a chat call billed by the
		// token: the note, the guide and the questions say how it really works.
		page.IsVideo = true
		page.SampleNote = template.HTML(template.HTMLEscapeString(T(locale, "sample.noteVideo")))
		page.DocsLabel = T(locale, "sample.docsVideo")
		page.DocsHref = docsBase + "/" + VideoGuideSlug(model.ID)
		page.FAQs = []FAQ{
			{Q: T(locale, "faq.videoHowQ", model.ID), A: T(locale, "faq.videoHowA")},
			{Q: T(locale, "faq.videoSdkQ"), A: T(locale, "faq.videoSdkA")},
			{Q: T(locale, "faq.videoBillingQ"), A: T(locale, "faq.videoBillingA")},
		}
	}
	for _, other := range RelatedModels(catalog, model, 3) {
		page.Related = append(page.Related, Related{
			ID: other.ID, Href: LocalizedPath(ModelPath(other), locale),
			Date: ReleaseDate(other), Prices: relatedPrices(other, locale),
		})
	}
	page.AgentPrompt = T(locale, "model.agentPrompt", SiteOrigin+page.MDHref, model.ID)
	return page
}

func dash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

// UpdatedLabel is the time the catalog was read, for "updated" stamps.
func UpdatedLabel(at time.Time) string { return at.UTC().Format("2006-01-02 15:04") + " UTC" }

// tierRow is the long-context line printed under a price row.
func tierRow(tier *ContextTier, base ListPrice, read, write *float64, muted bool, locale string) PriceRow {
	input, output, cacheRead, cacheWrite := TierPrices(tier, base, read, write)
	money := func(value *float64) string {
		if value == nil {
			return "—"
		}
		return MoneyShort(*value)
	}
	return PriceRow{Label: T(locale, "prices.tier", TokenWindow(tier.Threshold)), Tier: true, Muted: muted,
		Input: money(input), Output: money(output), CacheRead: money(cacheRead), CacheWrite: money(cacheWrite)}
}

// ModelAction is an extra button on a model page, after "Get an API key".
type ModelAction struct {
	Href  string
	Label string
}

var modelActionBuilders []func(model Model, locale string) *ModelAction

// RegisterModelAction adds a button that extensions offer on model pages;
// the builder returns nil for models it does not apply to.
func RegisterModelAction(build func(model Model, locale string) *ModelAction) {
	modelActionBuilders = append(modelActionBuilders, build)
}

func modelPageActions(model Model, locale string) []ModelAction {
	var actions []ModelAction
	for _, build := range modelActionBuilders {
		if action := build(model, locale); action != nil {
			actions = append(actions, *action)
		}
	}
	return actions
}
