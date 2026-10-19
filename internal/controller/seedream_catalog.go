package controller

import "github.com/lazu-ai/lazu/internal/dto"

func seedreamCatalogParameters(name string) map[string]dto.CatalogParam {
	params := map[string]dto.CatalogParam{
		"model":                   {Type: "string", Required: true, Description: "Model ID from the catalog."},
		"prompt":                  {Type: "string", Description: "Required for generation; optional for layer decomposition."},
		"image":                   {Type: "string_or_array", Description: "Reference image URL/data URL or array. Use this JSON field for edits on /v1/images/generations."},
		"size":                    {Type: "string", Default: "2K", Description: "Resolution level or valid widthxheight. Pro/Flash: 1K, 1.5K, 2K; Lite: 2K, 3K, 4K; 4.5: 2K, 4K. Pixel limits are model-specific."},
		"response_format":         {Type: "string", Default: "url", Enum: []string{"url", "b64_json"}},
		"watermark":               {Type: "boolean", Default: true},
		"stream":                  {Type: "boolean", Default: false, Description: "Only non-streaming image responses are supported by Lazu."},
		"optimize_prompt_options": {Type: "object", Description: "Native prompt optimization options."},
	}
	if dto.SeedreamSupportsLayers(name) {
		params["layer_decomposition"] = dto.CatalogParam{Type: "boolean", Default: false, Description: "Decompose exactly one input image. Returns a base image and up to 16 layers, each billed separately using its actual size."}
		params["background"] = dto.CatalogParam{Type: "string", Enum: []string{"opaque", "transparent"}, Description: "Transparent mode requires one reference image with an alpha channel."}
		params["n"] = dto.CatalogParam{Type: "integer", Default: 1, Range: []float64{1, 1}}
	} else {
		params["sequential_image_generation"] = dto.CatalogParam{Type: "string", Default: "disabled", Enum: []string{"disabled", "auto"}}
		params["sequential_image_generation_options"] = dto.CatalogParam{Type: "object", Description: "max_images: 1–15; reference images plus max_images must not exceed 15. Only successfully returned outputs are charged."}
		params["n"] = dto.CatalogParam{Type: "integer", Default: 1, Range: []float64{1, 15}, Description: "Convenience alias for sequential generation with max_images=n."}
	}
	return params
}
