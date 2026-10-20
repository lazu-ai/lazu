package publicsite

import (
	"os"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T) Catalog {
	t.Helper()
	body, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseCatalog(body)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":           "Claude Opus 5.5",
		"claude-haiku-4-5-20251001": "Claude Haiku 4.5",
		"gpt-5.6-sol":               "GPT-5.6 Sol",
		"gpt-4o-mini":               "GPT-4o Mini",
		"gpt-5.4-nano-2026-03-17":   "GPT-5.4 Nano (2026-03-17)",
		"gemini-3.5-flash":          "Gemini 3.5 Flash",
		"deepseek-v4-flash":         "DeepSeek V4 Flash",
		"kimi-k2.6":                 "Kimi K2.6",
		"glm-5.3":                   "GLM-5.3",
		"MiniMax-M2.7":              "MiniMax-M2.7",
		"seed-2.0-pro":              "Seed 2.0 Pro",
		"seed-2.1-turbo":            "Seed 2.1 Turbo",
		"seedance-2.5":              "Seedance 2.5",
	}
	for id, want := range cases {
		if got := DisplayName(id); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestModelPageCarriesPricesForCrawlers(t *testing.T) {
	catalog := loadFixture(t)
	model, ok := catalog.Find("claude-opus-5-5")
	if !ok {
		t.Fatal("fixture lacks claude-opus-5-5")
	}
	model.OfficialInput, model.OfficialOutput = 4, 20
	body, err := RenderModelPage(catalog, model, "zh")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`<html lang="zh-CN">`,
		`<link rel="canonical" href="https://lazu.ai/zh/models/anthropic/claude-opus-5-5" />`,
		`hreflang="x-default" href="https://lazu.ai/models/anthropic/claude-opus-5-5"`,
		`<!--# include virtual="/__chrome/header.zh.models.html" -->`,
		`<!--# include virtual="/__chrome/head.html" -->`,
		"Claude Opus 5.5",
		"$4.00", "$20.00", "$0.92", "$4.60",
		"与 Anthropic 官方价相同",
		`"@id":"https://lazu.ai/#organization"`,
		`"@type":"Product"`,
		`"@type":"FAQPage"`,
		"同官方",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("model page lacks %q", want)
		}
	}
}

func TestModelPageListsLongContextPrices(t *testing.T) {
	catalog := loadFixture(t)
	model, ok := catalog.Find("claude-opus-5-5")
	if !ok {
		t.Fatal("fixture lacks claude-opus-5-5")
	}
	long := func(v float64) *float64 { return &v }
	model.OfficialInput, model.OfficialOutput = 4, 20
	model.OfficialTier = &ContextTier{Threshold: 272_000, Input: long(8), Output: long(30)}
	lanes := append([]Lane(nil), Lanes(model)...)
	for index := range lanes {
		if lanes[index].Key != "stable" {
			continue
		}
		pricing := Pricing{}
		if lanes[index].Pricing != nil {
			pricing = *lanes[index].Pricing
		}
		pricing.ContextTier = &ContextTier{Threshold: 272_000, Input: long(7.25), Output: long(27.5)}
		lanes[index].Pricing = &pricing
	}
	model.Lanes = lanes

	body, err := RenderModelPage(catalog, model, "zh")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{"lazu-prices__row--tier", "输入超过 272k", "$8.00", "$30.00", "$7.25", "$27.50", "整单按长上下文价计费"} {
		if !strings.Contains(html, want) {
			t.Errorf("model page lacks %q", want)
		}
	}

	md := ModelMarkdown(catalog, model)
	for _, want := range []string{"input over 272k tokens | $7.25 | $27.50", "official, input over 272k tokens | $8.00 | $30.00", "billed entirely at the long-context prices"} {
		if !strings.Contains(md, want) {
			t.Errorf("model markdown lacks %q", want)
		}
	}
	raw, err := ModelJSON(catalog, model)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"threshold_tokens": 272000`) {
		t.Errorf("model json lacks the context tier: %s", raw)
	}
}

func TestModelsIndexGroupsByMaker(t *testing.T) {
	catalog := loadFixture(t)
	body, err := RenderModelsIndex(catalog, "en")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`<link rel="canonical" href="https://lazu.ai/models" />`,
		`id="anthropic"`, `id="openai"`,
		`href="/models/anthropic/claude-opus-5-5"`,
		`"@type":"CollectionPage"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("models index lacks %q", want)
		}
	}
	if strings.Index(html, `id="anthropic"`) > strings.Index(html, `id="openai"`) {
		t.Error("makers are not in catalog rank order")
	}
}

func TestMachineSiblings(t *testing.T) {
	catalog := loadFixture(t)
	model, _ := catalog.Find("claude-opus-5-5")
	md := ModelMarkdown(catalog, model)
	if !strings.Contains(md, "| Stable | $4.00 | $20.00 |") {
		t.Errorf("markdown price table missing:\n%s", md)
	}
	doc, err := ModelJSON(catalog, model)
	if err != nil || !strings.Contains(string(doc), `"input_per_million": 0.92`) {
		t.Errorf("json lacks the agent lane: %v\n%s", err, doc)
	}
	sitemap := ModelsSitemap(catalog)
	if !strings.Contains(sitemap, "<loc>https://lazu.ai/ja/models/openai/gpt-5.6-sol</loc>") {
		t.Error("sitemap lacks a localized model page")
	}
	if strings.Contains(sitemap, "<lastmod>") {
		t.Error("sitemap invents a lastmod for models whose change date is unknown")
	}
	for index := range catalog.Models {
		if catalog.Models[index].ID == "claude-opus-5-5" {
			catalog.Models[index].LastModified = time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
		}
	}
	sitemap = ModelsSitemap(catalog)
	for _, want := range []string{
		"<loc>https://lazu.ai/zh/models/anthropic/claude-opus-5-5</loc>\n    <lastmod>2026-09-12</lastmod>",
		"<loc>https://lazu.ai/models</loc>\n    <lastmod>2026-09-12</lastmod>",
		"<loc>https://lazu.ai/models/openai/gpt-5.6-sol</loc>\n    <xhtml:link",
	} {
		if !strings.Contains(sitemap, want) {
			t.Errorf("sitemap lacks %q", want)
		}
	}
}

func TestMoneyShort(t *testing.T) {
	cases := map[float64]string{4: "$4.00", 0.92: "$0.92", 0.046: "$0.046", 0.00375: "$0.00375", 20: "$20.00", 0.2: "$0.20"}
	for value, want := range cases {
		if got := MoneyShort(value); got != want {
			t.Errorf("MoneyShort(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestDiscountLaneDisclosure(t *testing.T) {
	catalog := loadFixture(t)
	model, _ := catalog.Find("claude-opus-5-5")
	page, err := RenderModelPage(catalog, model, "zh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Agent 专线由第三方订阅池提供，不是官方 API，只建议在 Claude Code 里使用。", "API 价格与国内调用"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("zh model page lacks %q", want)
		}
	}
	if md := ModelMarkdown(catalog, model); !strings.Contains(md, "use it only in Claude Code") {
		t.Errorf("model markdown lacks the discount-lane note:\n%s", md)
	}
}

func TestMetaDescription(t *testing.T) {
	lede := "Call x on Lazu: the stable lane costs $5 per million input tokens and $25 per million output tokens, the same as the Anthropic official price; the agent lane costs $1 / $5, 80% less. The agent lane runs on third-party subscription pools, not the official API; use it only in Claude Code. 1M context, up to 128k output tokens."
	got := metaDescription(lede, "en")
	if !strings.HasPrefix(got, "Call x on Lazu") || strings.Contains(got, "1M context") || len([]rune(got)) > 260 {
		t.Errorf("en meta = %q", got)
	}
	zh := "在 Lazu 上调用 x：稳定线路 $5 / 百万输入 token、$25 / 百万输出 token，与 Anthropic 官方价相同；Agent 专线 $1 / $5，便宜 80%。Agent 专线由第三方订阅池提供，不是官方 API，只建议在 Claude Code 里使用。1M 上下文，最长输出 128k。"
	if got := metaDescription(zh, "zh"); !strings.HasSuffix(got, "便宜 80%。") {
		t.Errorf("zh meta = %q", got)
	}
}
