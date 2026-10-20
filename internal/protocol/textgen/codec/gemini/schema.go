package gemini

// Gemini's function parameters and response schema take a subset of JSON
// Schema; these cut a client's schema down to it.

// CleanFunctionParameters recursively removes unsupported fields from Gemini function parameters.
func CleanFunctionParameters(params interface{}) interface{} {
	if params == nil {
		return nil
	}

	switch v := params.(type) {
	case map[string]interface{}:
		// Create a copy to avoid modifying the original
		cleanedMap := make(map[string]interface{})
		for k, val := range v {
			cleanedMap[k] = val
		}

		// Remove unsupported root-level fields
		delete(cleanedMap, "default")
		delete(cleanedMap, "exclusiveMaximum")
		delete(cleanedMap, "exclusiveMinimum")
		delete(cleanedMap, "$schema")
		delete(cleanedMap, "additionalProperties")

		// Check and clean 'format' for string types
		if propType, typeExists := cleanedMap["type"].(string); typeExists && propType == "string" {
			if formatValue, formatExists := cleanedMap["format"].(string); formatExists {
				if formatValue != "enum" && formatValue != "date-time" {
					delete(cleanedMap, "format")
				}
			}
		}

		// Clean properties
		if props, ok := cleanedMap["properties"].(map[string]interface{}); ok && props != nil {
			cleanedProps := make(map[string]interface{})
			for propName, propValue := range props {
				cleanedProps[propName] = CleanFunctionParameters(propValue)
			}
			cleanedMap["properties"] = cleanedProps
		}

		// Recursively clean items in arrays
		if items, ok := cleanedMap["items"].(map[string]interface{}); ok && items != nil {
			cleanedMap["items"] = CleanFunctionParameters(items)
		}
		// Also handle items if it's an array of schemas
		if itemsArray, ok := cleanedMap["items"].([]interface{}); ok {
			cleanedItemsArray := make([]interface{}, len(itemsArray))
			for i, item := range itemsArray {
				cleanedItemsArray[i] = CleanFunctionParameters(item)
			}
			cleanedMap["items"] = cleanedItemsArray
		}

		// Recursively clean other schema composition keywords
		for _, field := range []string{"allOf", "anyOf", "oneOf"} {
			if nested, ok := cleanedMap[field].([]interface{}); ok {
				cleanedNested := make([]interface{}, len(nested))
				for i, item := range nested {
					cleanedNested[i] = CleanFunctionParameters(item)
				}
				cleanedMap[field] = cleanedNested
			}
		}

		// Recursively clean patternProperties
		if patternProps, ok := cleanedMap["patternProperties"].(map[string]interface{}); ok {
			cleanedPatternProps := make(map[string]interface{})
			for pattern, schema := range patternProps {
				cleanedPatternProps[pattern] = CleanFunctionParameters(schema)
			}
			cleanedMap["patternProperties"] = cleanedPatternProps
		}

		// Recursively clean definitions
		if definitions, ok := cleanedMap["definitions"].(map[string]interface{}); ok {
			cleanedDefinitions := make(map[string]interface{})
			for defName, defSchema := range definitions {
				cleanedDefinitions[defName] = CleanFunctionParameters(defSchema)
			}
			cleanedMap["definitions"] = cleanedDefinitions
		}

		// Recursively clean $defs (newer JSON Schema draft)
		if defs, ok := cleanedMap["$defs"].(map[string]interface{}); ok {
			cleanedDefs := make(map[string]interface{})
			for defName, defSchema := range defs {
				cleanedDefs[defName] = CleanFunctionParameters(defSchema)
			}
			cleanedMap["$defs"] = cleanedDefs
		}

		// Clean conditional keywords
		for _, field := range []string{"if", "then", "else", "not"} {
			if nested, ok := cleanedMap[field]; ok {
				cleanedMap[field] = CleanFunctionParameters(nested)
			}
		}

		return cleanedMap

	case []interface{}:
		// Handle arrays of schemas
		cleanedArray := make([]interface{}, len(v))
		for i, item := range v {
			cleanedArray[i] = CleanFunctionParameters(item)
		}
		return cleanedArray

	default:
		// Not a map or array, return as is (e.g., could be a primitive)
		return params
	}
}

// CleanResponseSchema is a response schema in the subset Gemini's
// responseSchema takes: no title, $schema or additionalProperties, five
// levels deep.
func CleanResponseSchema(schema interface{}, depth int) interface{} {
	if depth >= 5 {
		return schema
	}

	v, ok := schema.(map[string]interface{})
	if !ok || len(v) == 0 {
		return schema
	}
	// 删除所有的title字段
	delete(v, "title")
	delete(v, "$schema")
	// 如果type不为object和array，则直接返回
	if typeVal, exists := v["type"]; !exists || (typeVal != "object" && typeVal != "array") {
		return schema
	}
	switch v["type"] {
	case "object":
		delete(v, "additionalProperties")
		// 处理 properties
		if properties, ok := v["properties"].(map[string]interface{}); ok {
			for key, value := range properties {
				properties[key] = CleanResponseSchema(value, depth+1)
			}
		}
		for _, field := range []string{"allOf", "anyOf", "oneOf"} {
			if nested, ok := v[field].([]interface{}); ok {
				for i, item := range nested {
					nested[i] = CleanResponseSchema(item, depth+1)
				}
			}
		}
	case "array":
		if items, ok := v["items"].(map[string]interface{}); ok {
			v["items"] = CleanResponseSchema(items, depth+1)
		}
	}

	return v
}
