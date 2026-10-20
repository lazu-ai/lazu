package model

// CompatibilityModelTarget describes upstream-retired names that remain valid
// request IDs. It does not rewrite permissions, billing or historical logs.
func CompatibilityModelTarget(name string) string {
	switch name {
	case "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		return "deepseek-flash"
	}
	return ""
}

// ListedModelNames hides an alias only when this viewer can also discover its
// replacement. A key scoped solely to an old name must not gain access to the
// replacement or lose discovery of its permitted model.
func ListedModelNames(names []string) []string {
	present := make(map[string]bool, len(names))
	for _, name := range names {
		present[name] = true
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if target := CompatibilityModelTarget(name); target != "" && present[target] {
			continue
		}
		result = append(result, name)
	}
	return result
}

func ListedPricing(rows []Pricing) []Pricing {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.ModelName)
	}
	listed := make(map[string]bool, len(names))
	for _, name := range ListedModelNames(names) {
		listed[name] = true
	}
	result := make([]Pricing, 0, len(rows))
	for _, row := range rows {
		if listed[row.ModelName] {
			result = append(result, row)
		}
	}
	return result
}
