package runtime

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// A channel's param_override names the fields of the channel's own wire. A
// Gemini client's override on an OpenAI or Anthropic channel is that
// channel's, applied to the encoded body as it was to the converted one; on a
// Gemini channel it is generateContent's, the client's own.
func TestGeminiClientOverrideIsTheChannelsWire(t *testing.T) {
	gemini := textgen.Protocol{Family: textgen.ProtocolGemini, Dialect: "gemini"}
	for name, tc := range map[string]struct {
		endpoint textgen.FeatureID
		want     bool
	}{
		"openai channel":    {textgen.FeatureEndpointChat, true},
		"anthropic channel": {textgen.FeatureEndpointMessages, true},
		"gemini channel":    {textgen.FeatureEndpointGemini, false},
	} {
		var features textgen.FeatureSet
		features.Add(tc.endpoint)
		if got := OverrideIsUpstreamWire(gemini, textgen.CapabilitySnapshot{Features: features}); got != tc.want {
			t.Errorf("%s: OverrideIsUpstreamWire = %v, want %v", name, got, tc.want)
		}
	}
}
