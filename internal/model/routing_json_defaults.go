package model

func ensureJSONDefault(fields ...*string) {
	for _, f := range fields {
		if *f == "" {
			*f = "{}"
		}
	}
}
