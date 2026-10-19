package publicsite

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func organizationID() string { return SiteOrigin + "/#organization" }
func websiteID() string      { return SiteOrigin + "/#website" }

func organizationRef() map[string]any { return map[string]any{"@id": organizationID()} }

func breadcrumb(items ...[2]string) map[string]any {
	list := make([]map[string]any, 0, len(items))
	for index, item := range items {
		list = append(list, map[string]any{
			"@type": "ListItem", "position": index + 1, "name": item[0], "item": item[1],
		})
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": list}
}

// RenderModelsIndex renders /models for a locale.
func RenderModelsIndex(catalog Catalog, locale string) ([]byte, error) {
	page := BuildModelsPage(catalog, locale)
	groups := GroupByMaker(catalog.Models)
	makers := Join(locale, topMakers(groups, locale, 4))
	meta := NewMeta(locale, "models", "/models",
		T(locale, "models.metaTitle", len(catalog.Models)),
		T(locale, "models.metaDesc", len(catalog.Models), makers))
	meta.MachineLinks = []MachineLink{
		{Type: "application/json", Href: SiteOrigin + "/models.json"},
		{Type: "text/markdown", Href: SiteOrigin + "/models.md"},
	}
	items := make([]map[string]any, 0, len(catalog.Models))
	for index, model := range catalog.Models {
		items = append(items, map[string]any{
			"@type": "ListItem", "position": index + 1,
			"url":  SiteOrigin + LocalizedPath(ModelPath(model), locale),
			"name": DisplayName(model.ID),
		})
	}
	meta.SetJSONLD(map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			map[string]any{
				"@type": "CollectionPage", "@id": meta.Canonical + "#webpage",
				"name": meta.Title, "description": meta.Description, "url": meta.Canonical,
				"inLanguage": locale, "isPartOf": map[string]any{"@id": websiteID()},
				"publisher": organizationRef(),
				"breadcrumb": breadcrumb(
					[2]string{"Lazu", SiteOrigin + LocalizedPath("/", locale)},
					[2]string{page.Title, meta.Canonical},
				),
				"mainEntity": map[string]any{"@type": "ItemList", "numberOfItems": len(items), "itemListElement": items},
			},
		},
	})
	return Render("models.gohtml", meta, page)
}

// RenderModelPage renders one model page for a locale.
func RenderModelPage(catalog Catalog, model Model, locale string) ([]byte, error) {
	page := BuildModelPage(catalog, model, locale, DocsURL(locale, ""))
	bare := ModelPath(model)
	description := metaDescription(stripTags(string(page.Lede)), locale)
	if description == "" {
		description = page.Description
	}
	if description == "" {
		description = T(locale, "model.metaFallback", page.Name, page.MakerName)
	}
	meta := NewMeta(locale, "models", bare, T(locale, "model.metaTitle", page.Name), description)
	meta.OGType = "product"
	meta.MachineLinks = []MachineLink{
		{Type: "application/json", Href: SiteOrigin + bare + ".json"},
		{Type: "text/markdown", Href: SiteOrigin + bare + ".md"},
	}
	var offers []any
	for _, lane := range Lanes(model) {
		if !LaneOn(lane) {
			continue
		}
		list := LaneList(lane, model)
		if list.Input == nil {
			continue
		}
		offer := map[string]any{
			"@type": "Offer", "name": laneLabel(lane, "en") + " lane",
			"price": fmt.Sprintf("%.4f", *list.Input), "priceCurrency": "USD",
			"availability": "https://schema.org/InStock", "seller": organizationRef(), "url": meta.Canonical,
			"priceSpecification": map[string]any{
				"@type": "UnitPriceSpecification", "price": *list.Input, "priceCurrency": "USD",
				"referenceQuantity": map[string]any{"@type": "QuantitativeValue", "value": 1000000, "unitText": "input tokens"},
			},
		}
		offers = append(offers, offer)
	}
	product := map[string]any{
		"@type": "Product", "@id": meta.Canonical + "#product",
		"name": page.Name, "alternateName": model.ID, "description": description,
		"category": "AI model API", "url": meta.Canonical,
		"brand": map[string]any{"@type": "Brand", "name": MakerName(model.Maker, "en")},
	}
	if len(offers) > 0 {
		product["offers"] = offers
	}
	faqs := make([]any, 0, len(page.FAQs))
	for _, item := range page.FAQs {
		faqs = append(faqs, map[string]any{
			"@type": "Question", "name": item.Q,
			"acceptedAnswer": map[string]any{"@type": "Answer", "text": item.A},
		})
	}
	meta.SetJSONLD(map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			map[string]any{
				"@type": "WebPage", "@id": meta.Canonical + "#webpage", "name": meta.Title,
				"description": description, "url": meta.Canonical, "inLanguage": locale,
				"isPartOf": map[string]any{"@id": websiteID()}, "publisher": organizationRef(),
				"mainEntity": map[string]any{"@id": meta.Canonical + "#product"},
				"breadcrumb": breadcrumb(
					[2]string{T(locale, "model.breadcrumb"), SiteOrigin + LocalizedPath("/models", locale)},
					[2]string{page.MakerName, SiteOrigin + page.MakerHref},
					[2]string{page.Name, meta.Canonical},
				),
			},
			product,
			map[string]any{"@type": "FAQPage", "@id": meta.Canonical + "#faq", "mainEntity": faqs},
		},
	})
	return Render("model.gohtml", meta, page)
}

func stripTags(value string) string {
	var b strings.Builder
	inTag := false
	for _, r := range value {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return strings.TrimSpace(replacer.Replace(b.String()))
}

// ----- machine-readable siblings ---------------------------------------------

// ModelJSON is the /models/<maker>/<id>.json document.
func ModelJSON(catalog Catalog, model Model) ([]byte, error) {
	type tierDoc struct {
		ThresholdTokens  int64    `json:"threshold_tokens"`
		InputPerMillion  *float64 `json:"input_per_million,omitempty"`
		OutputPerMillion *float64 `json:"output_per_million,omitempty"`
		CacheReadPerM    *float64 `json:"cache_read_per_million,omitempty"`
		CacheWrite5mPerM *float64 `json:"cache_write_5m_per_million,omitempty"`
	}
	tierOf := func(tier *ContextTier, base ListPrice, read, write *float64) *tierDoc {
		if tier == nil || tier.Threshold <= 0 {
			return nil
		}
		input, output, cacheRead, cacheWrite := TierPrices(tier, base, read, write)
		return &tierDoc{ThresholdTokens: tier.Threshold, InputPerMillion: input, OutputPerMillion: output,
			CacheReadPerM: cacheRead, CacheWrite5mPerM: cacheWrite}
	}
	type laneDoc struct {
		Lane             string   `json:"lane"`
		Available        bool     `json:"available"`
		InputPerMillion  *float64 `json:"input_per_million,omitempty"`
		OutputPerMillion *float64 `json:"output_per_million,omitempty"`
		CacheReadPerM    *float64 `json:"cache_read_per_million,omitempty"`
		CacheWrite5mPerM *float64 `json:"cache_write_5m_per_million,omitempty"`
		PerCall          *float64 `json:"per_call,omitempty"`
		PerSecond        *float64 `json:"per_second,omitempty"`
		Availability7d   *float64 `json:"availability_7d,omitempty"`
		Status           string   `json:"status,omitempty"`
		// ContextTier: past threshold input tokens (cache included) the whole
		// request is billed at these per-million prices.
		ContextTier *tierDoc `json:"context_tier,omitempty"`
	}
	doc := map[string]any{
		"id":                 model.ID,
		"name":               DisplayName(model.ID),
		"maker":              MakerName(model.Maker, "en"),
		"url":                SiteOrigin + ModelPath(model),
		"description":        DescriptionOf(model, "en"),
		"release_date":       ReleaseDate(model),
		"context_length":     model.ContextLength,
		"max_output":         model.MaxOutputTokens,
		"knowledge_cutoff":   model.KnowledgeCutoff,
		"input_modalities":   model.InputModalities,
		"output_modalities":  model.OutputModalities,
		"capabilities":       Capabilities(model, "en"),
		"endpoints":          model.SupportedEndpoints,
		"unsupported_params": model.UnsupportedParams,
		"currency":           "USD",
		"prices_updated_at":  catalog.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		"api_base_url":       APIOrigin + "/v1",
	}
	if model.OfficialInput > 0 {
		official := map[string]any{"input_per_million": model.OfficialInput, "output_per_million": model.OfficialOutput}
		if model.OfficialTier != nil {
			base := ListPrice{Input: &model.OfficialInput, Output: &model.OfficialOutput}
			if tier := tierOf(model.OfficialTier, base, nil, nil); tier != nil {
				official["context_tier"] = tier
			}
		}
		doc["official_price"] = official
	}
	var lanes []laneDoc
	for _, lane := range Lanes(model) {
		list := LaneList(lane, model)
		read, write := LaneCache(lane, model)
		entry := laneDoc{Lane: lane.Key, Available: LaneOn(lane), InputPerMillion: list.Input, OutputPerMillion: list.Output,
			CacheReadPerM: read, CacheWrite5mPerM: write, PerCall: list.PerCall, PerSecond: list.PerSecond,
			Status: LaneStatus(lane.Health), ContextTier: tierOf(LaneTier(lane, model), list, read, write)}
		if lane.Health != nil {
			entry.Availability7d = lane.Health.Availability
		}
		lanes = append(lanes, entry)
	}
	doc["lanes"] = lanes
	return json.MarshalIndent(doc, "", "  ")
}

func mdMoney(value *float64) string {
	if value == nil {
		return "—"
	}
	return MoneyShort(*value)
}

// ModelMarkdown is the /models/<maker>/<id>.md document.
func ModelMarkdown(catalog Catalog, model Model) string {
	page := BuildModelPage(catalog, model, "en", SiteOrigin+"/docs")
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Lazu · %s · %s -->\n\n# %s (`%s`)\n\n", page.Name, SiteOrigin+ModelPath(model), page.Name, model.ID)
	fmt.Fprintf(&b, "%s\n\n", stripTags(string(page.Lede)))
	if page.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", page.Description)
	}
	b.WriteString("## Facts\n\n")
	for _, fact := range page.Facts {
		fmt.Fprintf(&b, "- **%s:** %s\n", fact.Label, fact.Value)
	}
	b.WriteString("\n## Prices (USD per million tokens)\n\n| Lane | Input | Output | Cache read | Cache write 5m | 7-day availability |\n|---|---|---|---|---|---|\n")
	if model.OfficialInput > 0 {
		out := "—"
		if model.OfficialOutput > 0 {
			out = MoneyShort(model.OfficialOutput)
		}
		fmt.Fprintf(&b, "| %s official price | %s | %s | — | — | — |\n", page.MakerName, MoneyShort(model.OfficialInput), out)
		if tier := model.OfficialTier; tier != nil && tier.Threshold > 0 {
			base := ListPrice{Input: &model.OfficialInput, Output: &model.OfficialOutput}
			input, output, _, _ := TierPrices(tier, base, nil, nil)
			fmt.Fprintf(&b, "| %s official, input over %s tokens | %s | %s | — | — | — |\n", page.MakerName, TokenWindow(tier.Threshold), mdMoney(input), mdMoney(output))
		}
	}
	tiered := false
	for _, lane := range Lanes(model) {
		list := LaneList(lane, model)
		read, write := LaneCache(lane, model)
		label := laneLabel(lane, "en")
		if !LaneOn(lane) {
			label += " (not enabled)"
		}
		input, output := mdMoney(list.Input), mdMoney(list.Output)
		if list.PerCall != nil {
			input, output = mdMoney(list.PerCall)+" per call", "—"
		} else if list.PerSecond != nil {
			input, output = mdMoney(list.PerSecond)+" per second", "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", label, input, output, mdMoney(read), mdMoney(write), Uptime(lane.Health))
		if tier := LaneTier(lane, model); tier != nil && LaneOn(lane) && list.PerCall == nil && list.PerSecond == nil {
			tierInput, tierOutput, tierRead, tierWrite := TierPrices(tier, list, read, write)
			fmt.Fprintf(&b, "| %s, input over %s tokens | %s | %s | %s | %s | — |\n", label, TokenWindow(tier.Threshold),
				mdMoney(tierInput), mdMoney(tierOutput), mdMoney(tierRead), mdMoney(tierWrite))
			tiered = true
		}
	}
	if tiered || model.OfficialTier != nil {
		b.WriteString("\nA request whose input tokens (cache reads and writes included) pass the threshold is billed entirely at the long-context prices, not only for the tokens above it.\n")
	}
	fmt.Fprintf(&b, "\n%s\n", page.Updated)
	if len(page.Capabilities) > 0 {
		fmt.Fprintf(&b, "\n## Capabilities\n\n%s\n", strings.Join(page.Capabilities, ", "))
	}
	if len(page.Efforts) > 0 {
		fmt.Fprintf(&b, "\nReasoning effort: %s\n", strings.Join(page.Efforts, ", "))
	}
	if len(page.Unsupported) > 0 {
		fmt.Fprintf(&b, "\nUnsupported parameters: %s\n", strings.Join(page.Unsupported, ", "))
	}
	endpoint := DefaultEndpoint(model)
	fmt.Fprintf(&b, "\n## Example request (%s)\n\n```bash\n%s\n```\n", page.Endpoint, BuildSnippet("curl", endpoint, model.ID, APIOrigin))
	fmt.Fprintf(&b, "\nAny OpenAI SDK works: set `base_url` to `%s/v1`. Docs: %s/docs\n", APIOrigin, SiteOrigin)
	b.WriteString("\n## Questions\n\n")
	for _, item := range page.FAQs {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", item.Q, item.A)
	}
	return b.String()
}

// ModelsMarkdown is /models.md: every model with its prices and page.
func ModelsMarkdown(catalog Catalog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Lazu · Model catalog · %s/models -->\n\n# Lazu model catalog\n\n", SiteOrigin)
	fmt.Fprintf(&b, "%d models callable with one OpenAI-compatible key at `%s/v1`. Prices in USD per million tokens; updated %s.\n", len(catalog.Models), APIOrigin, UpdatedLabel(catalog.UpdatedAt))
	for _, group := range GroupByMaker(catalog.Models) {
		fmt.Fprintf(&b, "\n## %s\n\n| Model | Stable (in / out) | Discount (in / out) | Context | Page |\n|---|---|---|---|---|\n", MakerName(group.Maker, "en"))
		for _, model := range group.Models {
			pair := func(key string) string {
				lane, ok := findLane(model, key)
				if !ok {
					return "—"
				}
				list := LaneList(lane, model)
				switch {
				case list.Input != nil && list.Output != nil:
					return MoneyShort(*list.Input) + " / " + MoneyShort(*list.Output)
				case list.PerCall != nil:
					return MoneyShort(*list.PerCall) + " per call"
				case list.PerSecond != nil:
					return MoneyShort(*list.PerSecond) + " per second"
				}
				return "—"
			}
			window := TokenWindow(model.ContextLength)
			if window == "" {
				window = "—"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | [page](%s) · [md](%s.md) |\n", model.ID, pair("stable"), pair("eco"), window,
				SiteOrigin+ModelPath(model), SiteOrigin+ModelPath(model))
		}
	}
	return b.String()
}

// ModelsSitemap lists /models and every model page with their hreflang set.
// <lastmod> is each page's own last content change; a page whose date is
// unknown carries none rather than a made-up one, since search engines stop
// trusting lastmod on a site where it is often wrong.
func ModelsSitemap(catalog Catalog) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	write := func(bare string, lastmod time.Time, priority string) {
		for _, locale := range Locales {
			fmt.Fprintf(&b, "  <url>\n    <loc>%s</loc>\n", xmlEscape(SiteOrigin+LocalizedPath(bare, locale)))
			if !lastmod.IsZero() {
				fmt.Fprintf(&b, "    <lastmod>%s</lastmod>\n", lastmod.UTC().Format("2006-01-02"))
			}
			for _, other := range Locales {
				fmt.Fprintf(&b, "    <xhtml:link rel=\"alternate\" hreflang=\"%s\" href=\"%s\" />\n", other, xmlEscape(SiteOrigin+LocalizedPath(bare, other)))
			}
			fmt.Fprintf(&b, "    <xhtml:link rel=\"alternate\" hreflang=\"x-default\" href=\"%s\" />\n", xmlEscape(SiteOrigin+bare))
			fmt.Fprintf(&b, "    <changefreq>daily</changefreq>\n    <priority>%s</priority>\n  </url>\n", priority)
		}
	}
	var newest time.Time
	for _, model := range catalog.Models {
		if model.LastModified.After(newest) {
			newest = model.LastModified
		}
	}
	write("/models", newest, "0.9")
	for _, model := range catalog.Models {
		write(ModelPath(model), model.LastModified, "0.7")
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(value)
}

// metaDescription keeps the lede's leading sentences that fit in what a
// search result shows (about 160 Latin characters, or 110 CJK ones); the
// page itself still carries the whole lede. The first sentence is always
// kept, however long.
func metaDescription(text string, locale string) string {
	limit := 200
	if locale != "en" {
		limit = 120
	}
	text = strings.TrimSpace(text)
	var out strings.Builder
	for text != "" {
		end := len(text)
		for _, stop := range []string{". ", "。"} {
			if index := strings.Index(text, stop); index >= 0 && index+len(stop) < end {
				end = index + len(stop)
			}
		}
		sentence := text[:end]
		if out.Len() > 0 && utf8.RuneCountInString(out.String()+sentence) > limit {
			break
		}
		out.WriteString(sentence)
		text = strings.TrimSpace(text[end:])
	}
	return strings.TrimSpace(out.String())
}
