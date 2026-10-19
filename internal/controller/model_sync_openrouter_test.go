package controller

import "testing"

func TestMatchLocalModel_WithVersionAndSuffixNormalization(t *testing.T) {
	orModel := &openRouterModel{ID: "anthropic/claude-haiku-4.5"}
	fullMap := map[string]*openRouterModel{
		"anthropic/claude-haiku-4.5": orModel,
	}
	strippedMap := map[string]*openRouterModel{
		"claude-haiku-4.5": orModel,
	}

	got := matchLocalModel("claude-haiku-4-5-20251001", fullMap, strippedMap)
	if got == nil || got.ID != "anthropic/claude-haiku-4.5" {
		t.Fatalf("expected claude-haiku-4.5 match, got %#v", got)
	}
}

func TestMatchLocalModel_WithLatestSuffixAndDotVersion(t *testing.T) {
	orModel := &openRouterModel{ID: "anthropic/claude-sonnet-4.6"}
	fullMap := map[string]*openRouterModel{
		"anthropic/claude-sonnet-4.6": orModel,
	}
	strippedMap := map[string]*openRouterModel{
		"claude-sonnet-4.6": orModel,
	}

	got := matchLocalModel("claude-sonnet-4-6", fullMap, strippedMap)
	if got == nil || got.ID != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("expected claude-sonnet-4.6 match, got %#v", got)
	}
}

func TestMatchLocalModel_WithThinkSuffix(t *testing.T) {
	orModel := &openRouterModel{ID: "anthropic/claude-opus-4.6"}
	fullMap := map[string]*openRouterModel{
		"anthropic/claude-opus-4.6": orModel,
	}
	strippedMap := map[string]*openRouterModel{
		"claude-opus-4.6": orModel,
	}

	got := matchLocalModel("claude-opus-4-6-think", fullMap, strippedMap)
	if got == nil || got.ID != "anthropic/claude-opus-4.6" {
		t.Fatalf("expected claude-opus-4.6 match, got %#v", got)
	}
}

func TestMatchLocalModel_NoMatch(t *testing.T) {
	fullMap := map[string]*openRouterModel{}
	strippedMap := map[string]*openRouterModel{}

	got := matchLocalModel("non-existent-model", fullMap, strippedMap)
	if got != nil {
		t.Fatalf("expected nil match, got %#v", got)
	}
}
