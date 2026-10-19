package types

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var errorLocales = []string{"en", "zh", "zh-TW", "ja"}

// errorLocaleDir is the console's error namespace: one folder per language,
// one JSON file per domain, all merged at runtime.
const errorLocaleDir = "../../apps/web/src/i18n/locales/errors"

func loadErrorLocale(t *testing.T, lang string) map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(errorLocaleDir, lang, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no error locale files for %s: %v", lang, err)
	}
	merged := map[string]any{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var entries map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for key, value := range entries {
			if _, dup := merged[key]; dup {
				t.Errorf("%s: key %q is defined in more than one %s file", file, key, lang)
			}
			merged[key] = value
		}
	}
	return merged
}

// TestEveryPublicCodeIsLocalized fails when a public row lacks console copy
// in any language, or when a locale keeps copy for a code that no longer
// exists. The console renders errors from these files by code; the English
// wire message is never shown there.
func TestEveryPublicCodeIsLocalized(t *testing.T) {
	public := map[string]bool{}
	for _, code := range PublicCatalogCodes() {
		public[string(code)] = true
	}
	for _, lang := range errorLocales {
		entries := loadErrorLocale(t, lang)
		var missing, orphan []string
		for code := range public {
			value, ok := entries[code].(string)
			if !ok || strings.TrimSpace(value) == "" {
				missing = append(missing, code)
			}
		}
		for key := range entries {
			if key == "type" || key == "local" {
				continue
			}
			if !public[key] {
				orphan = append(orphan, key)
			}
		}
		sort.Strings(missing)
		sort.Strings(orphan)
		if len(missing) > 0 {
			t.Errorf("%s: no copy for %s", lang, strings.Join(missing, ", "))
		}
		if len(orphan) > 0 {
			t.Errorf("%s: copy for unknown codes %s", lang, strings.Join(orphan, ", "))
		}
		types, _ := entries["type"].(map[string]any)
		for _, errType := range []string{
			ErrorTypeInvalidRequest, ErrorTypeAuthentication, ErrorTypePermission, ErrorTypeNotFound,
			ErrorTypeRateLimit, ErrorTypeAPI, ErrorTypeOverloaded, ErrorTypeAPIConnection,
			ErrorTypeTimeout, ErrorTypeContentPolicy, ErrorTypeInsufficientQuota, ErrorTypeConflict,
		} {
			if value, _ := types[errType].(string); strings.TrimSpace(value) == "" {
				t.Errorf("%s: no fallback copy for type %s", lang, errType)
			}
		}
	}
}
