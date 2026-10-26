package video

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// decodeObject parses a provider body as a generic JSON object. Numbers stay
// json.Number so task ids written as integers survive.
func decodeObject(body []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, false
	}
	return payload, true
}

// lookup walks a dotted path such as "data.result.videos.0.url.0": object
// keys and zero-based array indexes. Missing steps return nil.
func lookup(payload any, path string) any {
	value := payload
	for _, part := range strings.Split(strings.TrimSpace(path), ".") {
		if part == "" {
			return nil
		}
		switch typed := value.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil
			}
			value = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil
			}
			value = typed[index]
		default:
			return nil
		}
	}
	return value
}

// firstString returns the first non-empty string (or number rendered as a
// string) at any of the paths.
func firstString(payload any, paths ...string) string {
	for _, path := range paths {
		if text := stringValue(lookup(payload, path)); text != "" {
			return text
		}
	}
	return ""
}

// firstNumber returns the first finite number at any of the paths.
func firstNumber(payload any, paths ...string) (float64, bool) {
	for _, path := range paths {
		if number, ok := numberValue(lookup(payload, path)); ok {
			return number, true
		}
	}
	return 0, false
}

func firstInt(payload any, paths ...string) int64 {
	number, ok := firstNumber(payload, paths...)
	if !ok || number <= 0 {
		return 0
	}
	return int64(number)
}

func firstBool(payload any, paths ...string) (bool, bool) {
	for _, path := range paths {
		switch typed := lookup(payload, path).(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}
	}
	return false, false
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return 0, false
	}
}
