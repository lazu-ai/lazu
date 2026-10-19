package textgen

import "testing"

func TestExtractFeaturesIncludesConversationDeveloperRoleAndStrictTool(t *testing.T) {
	request := minimalRequest()
	request.Conversation[0].Role = "developer"
	request.Tools = []ToolDefinition{{Type: "function", Name: "lookup", Strict: Some(true)}}

	features, uses := ExtractFeatures(request)
	if !features.Has(FeatureDeveloperRole) || !features.Has(FeatureStrictSchema) {
		t.Fatalf("missing semantic features: %v", features.Names())
	}
	paths := map[FeatureID]string{}
	for _, use := range uses {
		paths[use.Feature] = use.Path
	}
	if paths[FeatureDeveloperRole] != "input[0].role" {
		t.Fatalf("developer role path=%q", paths[FeatureDeveloperRole])
	}
	if paths[FeatureStrictSchema] != "tools[0].strict" {
		t.Fatalf("strict tool path=%q", paths[FeatureStrictSchema])
	}
}

func TestExtractFeaturesIgnoresNoOpLogprobs(t *testing.T) {
	request := minimalRequest()
	request.Sampling.LogProbs = Some(false)
	request.Sampling.TopLogProbs = Some(0)
	features, _ := ExtractFeatures(request)
	if features.Has(FeatureLogProbs) {
		t.Fatalf("explicit no-op logprobs options must not require bridge support: %v", features.Names())
	}

	request.Sampling.TopLogProbs = Some(1)
	features, _ = ExtractFeatures(request)
	if !features.Has(FeatureLogProbs) {
		t.Fatalf("positive top_logprobs must require logprobs support: %v", features.Names())
	}
}
