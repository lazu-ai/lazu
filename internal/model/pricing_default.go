package model

import (
	"strings"
)

type defaultVendorRule struct {
	Pattern string
	Vendor  string
}

// 简化的供应商映射规则。使用有序列表避免 map 迭代导致同一模型品牌不稳定。
var defaultVendorRules = []defaultVendorRule{
	{Pattern: "gemini", Vendor: "Google"},
	{Pattern: "claude", Vendor: "Anthropic"},
	{Pattern: "dall-e", Vendor: "OpenAI"},
	{Pattern: "whisper", Vendor: "OpenAI"},
	{Pattern: "gpt", Vendor: "OpenAI"},
	{Pattern: "o1", Vendor: "OpenAI"},
	{Pattern: "o3", Vendor: "OpenAI"},
	{Pattern: "o4", Vendor: "OpenAI"},
	{Pattern: "grok", Vendor: "xAI"},
}

// 供应商默认图标映射
var defaultVendorIcons = map[string]string{
	"OpenAI":    "OpenAI",
	"Anthropic": "Claude.Color",
	"Google":    "Gemini.Color",
	"xAI":       "XAI",
}

// initDefaultVendorMapping 简化的默认供应商映射。
// 只校正已主动上架的模型；缺失模型应先进入候选池，不能仅凭渠道能力自动公开。
func initDefaultVendorMapping(metaMap map[string]*Model, vendorMap map[int]*Vendor, enableAbilities []AbilityWithChannel) {
	for _, ability := range enableAbilities {
		modelName := ability.Model

		// 根据规则推断正确的 vendorID
		expectedVendorID := 0
		if vendorName, _, ok := InferCanonicalModelBrand(modelName); ok {
			expectedVendorID = findVendorByName(vendorName, vendorMap)
		}

		if existingModel, exists := metaMap[modelName]; exists {
			// 模型已存在，检查 vendor 是否需要校正
			// 只有当 expectedVendorID > 0 且与当前不同时才校正
			if expectedVendorID > 0 && existingModel.VendorID != expectedVendorID {
				existingModel.VendorID = expectedVendorID
			}
		}
	}
}

func InferCanonicalModelBrand(modelName string) (string, string, bool) {
	modelLower := strings.ToLower(strings.TrimSpace(modelName))
	if modelLower == "" {
		return "", "", false
	}
	for _, rule := range defaultVendorRules {
		if strings.Contains(modelLower, rule.Pattern) {
			return rule.Vendor, getDefaultVendorIcon(rule.Vendor), true
		}
	}
	return "", "", false
}

// findVendorByName resolves a vendor row that already exists.
//
// This used to create the row when it was missing, from inside updatePricing —
// which runs at boot and again every 60 seconds. A vendor an operator deleted
// therefore reappeared within the minute, as long as one enabled channel served
// a model whose name matched a rule above. Vendors are now only ever created
// deliberately, through the console; an unmatched name simply leaves the
// model's vendor unset, exactly as it does for every brand not in the rules.
func findVendorByName(vendorName string, vendorMap map[int]*Vendor) int {
	for id, vendor := range vendorMap {
		if vendor.Name == vendorName {
			return id
		}
	}
	return 0
}

// 获取供应商默认图标
func getDefaultVendorIcon(vendorName string) string {
	if icon, exists := defaultVendorIcons[vendorName]; exists {
		return icon
	}
	return ""
}
