package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// Redact before truncation: chopping a large inline image first can make the
// JSON invalid and leave a megabyte of base64 in the captured prefix.
func redactLoggedImageData(body []byte) []byte {
	if !bytes.Contains(body, []byte("image")) {
		return body
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value interface{}
	if decoder.Decode(&value) != nil {
		return []byte(`{"capture_omitted":"malformed image request"}`)
	}
	changed := false
	var visit func(interface{}) interface{}
	visit = func(v interface{}) interface{} {
		switch item := v.(type) {
		case string:
			if strings.HasPrefix(strings.ToLower(item), "data:image/") {
				changed = true
				hash := sha256.Sum256([]byte(item))
				return fmt.Sprintf("[inline image omitted: encoded_bytes=%d sha256=%x]", len(item), hash)
			}
		case []interface{}:
			for i, child := range item {
				item[i] = visit(child)
			}
		case map[string]interface{}:
			media, _ := item["media_type"].(string)
			if media == "" {
				media, _ = item["mime_type"].(string)
			}
			if strings.HasPrefix(strings.ToLower(media), "image/") {
				for _, key := range []string{"data", "bytes"} {
					if data, ok := item[key].(string); ok {
						changed = true
						hash := sha256.Sum256([]byte(data))
						item[key] = fmt.Sprintf("[inline image omitted: encoded_bytes=%d sha256=%x]", len(data), hash)
					}
				}
			}
			for key, child := range item {
				item[key] = visit(child)
			}
		}
		return v
	}
	visit(value)
	if !changed {
		return body
	}
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
}
