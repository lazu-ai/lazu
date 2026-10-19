package controller

import (
	"github.com/lazu-ai/lazu/internal/model"
	"reflect"
	"testing"
)

func TestParseUpstreamModelIDsSupportsOpenAIResponse(t *testing.T) {
	got, err := parseUpstreamModelIDs([]byte(`{
		"data": [
			{"id": "gpt-5.4"},
			{"id": "gpt-5.4"}
		]
	}`))
	if err != nil {
		t.Fatalf("parseUpstreamModelIDs returned error: %v", err)
	}
	want := []string{"gpt-5.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestParseUpstreamModelIDsSupportsGeminiResponse(t *testing.T) {
	got, err := parseUpstreamModelIDs([]byte(`{
		"models": [
			{"name": "models/gemini-3.1-pro-preview"},
			{"name": "models/gemini-flash-latest"},
			{"name": "models/gemini-3.1-pro-preview"}
		]
	}`))
	if err != nil {
		t.Fatalf("parseUpstreamModelIDs returned error: %v", err)
	}
	want := []string{"gemini-3.1-pro-preview", "gemini-flash-latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

// The retired-name filter used to guard a table of model names compiled into
// the binary. The names are gone; the rules now apply to the catalog, so the
// brand inference that replaced the table is what is worth pinning here.
func TestModelOwnedByUsesCatalogBrandRules(t *testing.T) {
	cases := map[string]string{
		"gpt-5.4":                "OpenAI",
		"claude-fable-5-1":       "Anthropic",
		"gemini-3.1-pro-preview": "Google",
		"grok-4":                 "xAI",
		"some-inhouse-model":     "custom",
	}
	for modelName, want := range cases {
		if got := modelOwnedBy(modelName); got != want {
			t.Errorf("modelOwnedBy(%q) = %q, want %q", modelName, got, want)
		}
	}
}

func TestModelOwnershipUsesConfiguredCatalogVendor(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	vendor := model.Vendor{Name: "Configured Vendor"}
	if err := model.CoreDB().Create(&vendor).Error; err != nil {
		t.Fatal(err)
	}
	names := []string{"text-embedding-3-small", "inhouse-model", "gpt-5.4"}
	for _, name := range names {
		if err := model.CoreDB().Create(&model.Model{ModelName: name, VendorID: vendor.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if got := modelOwnedBy(name); got != vendor.Name {
			t.Fatalf("%s owner = %q", name, got)
		}
	}
	for _, item := range buildOpenAIModels(names) {
		if item.OwnedBy != vendor.Name {
			t.Fatalf("list owner = %q", item.OwnedBy)
		}
	}
	if err := model.CoreDB().Delete(&vendor).Error; err != nil {
		t.Fatal(err)
	}
	if got := modelOwnedBy("gpt-5.4"); got != "OpenAI" {
		t.Fatalf("fallback owner = %q", got)
	}
}
