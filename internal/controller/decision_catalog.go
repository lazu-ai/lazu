package controller

import (
	"fmt"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/publicsite"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func decisionCatalogParameters(name string) map[string]dto.CatalogParam {
	if name == common.OpenAIDecisionModel {
		return map[string]dto.CatalogParam{
			"model":     {Type: "string", Required: true},
			"input":     {Type: "string | array", Required: true, Description: "Text or user messages with input_text and inline input_image parts. Lazu currently limits total text to 8192 estimated tokens and images to 4."},
			"questions": {Type: "array", Required: true, Description: "1 to 64 ordered predicate, choice or score questions. Names are optional; choice values retain string or boolean types. Score levels form an ordered rubric."},
		}
	}
	return map[string]dto.CatalogParam{"model": {Type: "string", Required: true}, "state": {Type: "string | object | array", Required: true, Description: "Material to evaluate. Clef currently has a conservative 2000 UTF-8 byte state limit."}, "questions": {Type: "object", Description: "1 to 64 keyed noul, choice or score questions; required unless question generation is enabled."}, "images": {Type: "array", Description: "Clef only: up to 4 inline PNG, JPEG or WebP data URLs."}, "question_generation": {Type: "object", Description: "Optional enabled and model fields; disabled by default."}}
}

func officialDecisionCatalogUsage(pricing dto.ModelCatalogPricing) dto.ModelCatalogUsageCapabilities {
	// Reported usage is independent of its price. Zero-priced cache and output
	// facts remain visible without advertising a positive charge for them.
	billable := make([]string, 0, 7)
	for _, entry := range []struct {
		dimension string
		price     *float64
	}{
		{usagepkg.DimensionInput, pricing.Input},
		{usagepkg.DimensionOutput, pricing.Output},
		{usagepkg.DimensionImageInput, pricing.ImageInput},
		{usagepkg.DimensionImageOutput, pricing.ImageOutput},
		{usagepkg.DimensionCacheRead, pricing.CacheRead},
		{usagepkg.DimensionCacheWrite5m, pricing.CacheWrite5m},
		{usagepkg.DimensionCacheWrite1h, pricing.CacheWrite1h},
	} {
		if entry.price != nil && *entry.price > 0 {
			billable = append(billable, entry.dimension)
		}
	}
	fields := []string{"usage.input_tokens", "usage.output_tokens", "usage.total_tokens", "usage.input_tokens_details.image_tokens", "usage.input_tokens_details.cached_tokens", "usage.input_tokens_details.cache_write_tokens", "usage.output_tokens_details.reasoning_tokens"}
	return dto.ModelCatalogUsageCapabilities{
		ReportedDimensions: []string{usagepkg.DimensionInput, usagepkg.DimensionOutput, usagepkg.DimensionCacheRead, usagepkg.DimensionCacheWrite, usagepkg.DimensionImageInput},
		BillableDimensions: billable,
		ResponseFields:     map[string][]string{"openai_compatible": fields, "provider_native": fields, "request_detail": {"usage.dimensions", "billing.line_items"}},
	}
}

func decisionCatalogExample(name string) string {
	if name == common.OpenAIDecisionModel {
		return fmt.Sprintf("curl "+publicsite.SiteOrigin+"/v1/decisions \\\n  -H 'Authorization: Bearer $LAZU_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"input\":\"The customer cannot log in.\",\"questions\":[{\"name\":\"login_issue\",\"type\":\"predicate\",\"instructions\":\"Is this a login problem?\"}]}'", common.OpenAIDecisionRequestModel)
	}
	return fmt.Sprintf("curl "+publicsite.SiteOrigin+"/v1/decisions \\\n  -H 'Authorization: Bearer $LAZU_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"state\":\"The customer cannot log in.\",\"questions\":{\"login_issue\":{\"type\":\"noul\",\"instructions\":\"Is this a login problem?\"}}}'", name)
}
