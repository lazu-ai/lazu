package model

// ModelParamsSchema describes parameter constraints for a specific model.
type ModelParamsSchema struct {
	Duration    *ParamConstraint `json:"duration,omitempty"`
	AspectRatio *ParamConstraint `json:"aspect_ratio,omitempty"`
}

// ParamConstraint describes a single parameter's allowed values.
type ParamConstraint struct {
	Type    string   `json:"type"`              // "enum" | "range"
	Options []string `json:"options,omitempty"` // type=enum: allowed values
	Default string   `json:"default,omitempty"` // default value
	Min     int      `json:"min,omitempty"`     // type=range: minimum
	Max     int      `json:"max,omitempty"`     // type=range: maximum
}

// GetModelParamsSchema returns parameter constraints for the given model,
// or nil if the model has no special constraints.
func GetModelParamsSchema(modelName string) *ModelParamsSchema {
	return nil
}
