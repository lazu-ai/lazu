package model

import "strings"

func IsModelRequestable(modelName string) bool {
	name := strings.TrimSpace(modelName)
	if name == "" || IsRetiredModelName(name) {
		return false
	}
	GetPricing()

	modelEnableGroupsLock.RLock()
	defer modelEnableGroupsLock.RUnlock()
	if _, ok := modelEnableGroups[name]; ok {
		return true
	}
	normalized := NormalizePricingModelName(name)
	if normalized == "" || normalized == name {
		return false
	}
	_, ok := modelEnableGroups[normalized]
	return ok
}

func GetModelEnableGroups(modelName string) []string {
	// 确保缓存最新
	GetPricing()

	if modelName == "" {
		return make([]string, 0)
	}

	modelEnableGroupsLock.RLock()
	groups, ok := modelEnableGroups[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return make([]string, 0)
	}
	return groups
}

// GetModelQuotaTypes 返回指定模型的计费类型集合（来自缓存）
func GetModelQuotaTypes(modelName string) []int {
	GetPricing()

	modelEnableGroupsLock.RLock()
	quota, ok := modelQuotaTypeMap[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return []int{}
	}
	return []int{quota}
}
