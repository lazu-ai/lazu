package textgen

import "fmt"

type ParameterSpec struct {
	ID              string
	RequiredFeature FeatureID
	Conflicts       []string
	Validate        func(any) error
}

var ParameterRegistry = map[string]ParameterSpec{
	"temperature": {ID: "temperature", RequiredFeature: FeatureTemperature, Validate: rangeFloat(0, 2)},
	"top_p":       {ID: "top_p", RequiredFeature: FeatureTopP, Validate: rangeFloat(0, 1)},
	"top_k":       {ID: "top_k", RequiredFeature: FeatureTopK}, "seed": {ID: "seed", RequiredFeature: FeatureSeed},
	"stop": {ID: "stop", RequiredFeature: FeatureStop}, "n": {ID: "n", RequiredFeature: FeatureN},
	"logprobs": {ID: "logprobs", RequiredFeature: FeatureLogProbs}, "max_output_tokens": {ID: "max_output_tokens", RequiredFeature: FeatureMaxOutput},
}

func rangeFloat(min, max float64) func(any) error {
	return func(value any) error {
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("expected number")
		}
		if number < min || number > max {
			return fmt.Errorf("must be between %g and %g", min, max)
		}
		return nil
	}
}
