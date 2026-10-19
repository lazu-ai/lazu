package textgen

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
)

// RetainTopLevelFields references immutable inbound bytes. The DTO decoder has
// already validated JSON; retaining wire extensions must not decode the entire
// request into a second object graph or copy large inputs again.
func RetainTopLevelFields(body []byte) (map[string]json.RawMessage, error) {
	fields := make(map[string]json.RawMessage)
	var duplicate string
	hasDuplicate := false
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		if _, exists := fields[key.Str]; exists {
			duplicate = key.Str
			hasDuplicate = true
			return false
		}
		if value.Index >= 0 && value.Index+len(value.Raw) <= len(body) {
			fields[key.Str] = json.RawMessage(body[value.Index : value.Index+len(value.Raw)])
		} else {
			fields[key.Str] = json.RawMessage(value.Raw)
		}
		return true
	})
	if hasDuplicate {
		return nil, fmt.Errorf("duplicate JSON field %q", duplicate)
	}
	return fields, nil
}
