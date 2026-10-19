package common

import (
	"bytes"
	"encoding/json"

	"github.com/gin-gonic/gin"
	common2 "github.com/lazu-ai/lazu/internal/common"
)

// MergeUnknownTopLevelFields merges unknown top-level fields from originalBody into convertedBody.
// Existing keys in convertedBody always win.
func MergeUnknownTopLevelFields(originalBody, convertedBody []byte) ([]byte, error) {
	originalMap, originalObject, err := parseJSONObject(originalBody)
	if err != nil {
		return nil, err
	}
	convertedMap, convertedObject, err := parseJSONObject(convertedBody)
	if err != nil {
		return nil, err
	}

	if !convertedObject {
		return convertedBody, nil
	}
	if !originalObject {
		return convertedBody, nil
	}

	for key, value := range originalMap {
		if _, exists := convertedMap[key]; !exists {
			convertedMap[key] = value
		}
	}
	return common2.Marshal(convertedMap)
}

func MergeUnknownTopLevelFieldsFromContext(c *gin.Context, convertedBody []byte) ([]byte, error) {
	originalBody, err := common2.GetRequestBody(c)
	if err != nil {
		return nil, err
	}
	return MergeUnknownTopLevelFields(originalBody, convertedBody)
}

func parseJSONObject(data []byte) (map[string]json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}

	var result map[string]json.RawMessage
	if err := common2.Unmarshal(trimmed, &result); err != nil {
		return nil, false, err
	}
	return result, true, nil
}
