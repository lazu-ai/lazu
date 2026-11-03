package model

import (
	"strings"
	"sync"
)

// An operator retires a name by pointing it at a replacement: the catalog row
// for the old name carries replacement_model, and the serving channel maps the
// old name to the new one, so Keys that still send the old name keep working.
// The old row stays active (status 1, lifecycle active) because the requestable
// set is built from publicly listable rows; marking it retired would make it
// unrequestable. Listings hide it instead, through ListedModelNames.
var (
	catalogReplacementsMu sync.RWMutex
	catalogReplacements   map[string]string
)

// catalogReplacementTargets reads replacement_model from the resolved catalog.
// metaMap holds only publicly listable rows, keyed by requested name; a row
// counts only for its own exact name (a pattern row must not retire a family)
// and only when its replacement is another catalog model.
func catalogReplacementTargets(metaMap map[string]*Model) map[string]string {
	targets := make(map[string]string)
	for name, meta := range metaMap {
		if meta == nil || meta.ModelName != name {
			continue
		}
		target := strings.TrimSpace(meta.ReplacementModel)
		if target == "" || target == name {
			continue
		}
		if _, ok := metaMap[target]; !ok {
			continue
		}
		targets[name] = target
	}
	// Two rows pointing at each other would hide both from a viewer who sees
	// both; neither is retired then.
	for name, target := range targets {
		if targets[target] == name {
			delete(targets, name)
			delete(targets, target)
		}
	}
	return targets
}

func setCatalogReplacements(targets map[string]string) {
	catalogReplacementsMu.Lock()
	catalogReplacements = targets
	catalogReplacementsMu.Unlock()
}

func currentCatalogReplacements() map[string]string {
	// Unit consumers may list names before a database is configured.
	if CoreDB() != nil {
		GetPricing()
	}
	catalogReplacementsMu.RLock()
	defer catalogReplacementsMu.RUnlock()
	return catalogReplacements
}

// compatibilityTarget prefers the catalog's replacement_model. The fixed pair
// below is the fallback for deployments whose rows do not carry it yet.
func compatibilityTarget(replacements map[string]string, name string) string {
	if target, ok := replacements[name]; ok {
		return target
	}
	switch name {
	case "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		return "deepseek-flash"
	}
	return ""
}

// CompatibilityModelTarget returns the replacement for a name an operator has
// retired by pointing it at one, or "". The old name stays a valid request ID;
// this does not rewrite permissions, billing or historical logs.
func CompatibilityModelTarget(name string) string {
	return compatibilityTarget(currentCatalogReplacements(), name)
}

// ListedModelNames hides an alias only when this viewer can also discover its
// replacement. A key scoped solely to an old name must not gain access to the
// replacement or lose discovery of its permitted model.
func ListedModelNames(names []string) []string {
	replacements := currentCatalogReplacements()
	present := make(map[string]bool, len(names))
	for _, name := range names {
		present[name] = true
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if target := compatibilityTarget(replacements, name); target != "" && present[target] {
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
