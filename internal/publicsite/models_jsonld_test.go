package publicsite

import (
	"encoding/json"
	"regexp"
	"testing"
)

// modelNode returns the #product node of a rendered model page.
func modelNode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	for _, match := range regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindAllSubmatch(body, -1) {
		var doc map[string]any
		if err := json.Unmarshal(match[1], &doc); err != nil {
			t.Fatalf("JSON-LD does not parse: %v", err)
		}
		graph, _ := doc["@graph"].([]any)
		for _, item := range graph {
			if node, ok := item.(map[string]any); ok && regexp.MustCompile(`#product$`).MatchString(node["@id"].(string)) {
				return node
			}
		}
	}
	t.Fatal("model page has no #product node")
	return nil
}

// Google lists a Product without offers, review or aggregateRating as an
// invalid product snippet in Search Console. Every model page either offers
// its headline price or is not a Product.
func TestModelPageProductAlwaysCarriesOffers(t *testing.T) {
	catalog := loadFixture(t)
	for _, model := range catalog.Models {
		for _, locale := range Locales {
			body, err := RenderModelPage(catalog, model, locale)
			if err != nil {
				t.Fatal(err)
			}
			node := modelNode(t, body)
			if node["@type"] == "Product" && node["offers"] == nil {
				t.Errorf("%s %s: Product without offers", model.ID, locale)
			}
		}
	}

	base, ok := catalog.Find("claude-opus-5-5")
	if !ok {
		t.Fatal("fixture lacks claude-opus-5-5")
	}
	open := true

	// A video model billed by the second offers its per-second price.
	perSecond := 0.103
	video := base
	video.ID, video.Pricing = "seedance-2.5", nil
	video.Lanes = []Lane{{Key: "stable", Available: &open, PerSecond: &perSecond}}
	node := modelNode(t, mustRender(t, catalog, video))
	offers, _ := node["offers"].([]any)
	if node["@type"] != "Product" || len(offers) != 1 {
		t.Fatalf("video model node = %v", node)
	}
	offer := offers[0].(map[string]any)
	spec := offer["priceSpecification"].(map[string]any)["referenceQuantity"].(map[string]any)
	if offer["price"] != "0.103" || spec["unitText"] != "second" || spec["value"] != float64(1) {
		t.Errorf("video offer = %v", offer)
	}

	// A model with no published price on any lane is not a Product.
	unpriced := base
	unpriced.ID, unpriced.Pricing = "unpriced-model", nil
	unpriced.Lanes = []Lane{{Key: "stable", Available: &open}}
	node = modelNode(t, mustRender(t, catalog, unpriced))
	if node["@type"] != "Thing" || node["offers"] != nil || node["brand"] != nil {
		t.Errorf("unpriced model node = %v", node)
	}
}

func mustRender(t *testing.T, catalog Catalog, model Model) []byte {
	t.Helper()
	body, err := RenderModelPage(catalog, model, "en")
	if err != nil {
		t.Fatal(err)
	}
	return body
}
