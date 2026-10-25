package claude

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
)

// samplingFields are the Messages sampling fields a model may refuse. Only
// these are ever dropped, and only when the catalog row lists them in
// unsupported_params.
var samplingFields = []string{"temperature", "top_p", "top_k"}

// refusedSampling is the part of a catalog row's unsupported_params that
// names a Messages sampling field, in samplingFields order; nil when the row
// lists none of them.
func refusedSampling(unsupportedParams []string) []string {
	var refused []string
	for _, field := range samplingFields {
		for _, param := range unsupportedParams {
			if strings.EqualFold(strings.TrimSpace(param), field) {
				refused = append(refused, field)
				break
			}
		}
	}
	return refused
}

// DropRefusedSampling leaves out of request the sampling fields its model's
// policy refuses (ModelPolicy.RefusedSampling, read from the catalog row's
// unsupported_params) and names the ones the request carried, for the
// client's warning. Messages takes an absent temperature with or without
// thinking, so nothing is put in their place.
//
// Unlike OpenAI's reasoning models, which take temperature again at effort
// "none" (openai.samplingRefusedWhileReasoning), a Claude model that refuses
// a sampling field refuses it with thinking off as well: a 5.5-generation
// Haiku answered 400 to temperature 0.2 with thinking disabled and 200 to
// temperature 1 or none (2026-10-25). So the drop does not depend on the
// request's thinking. A row that lists nothing, or a model the catalog does
// not know, drops nothing: the provider's own answer stands, as before.
func DropRefusedSampling(request *dto.ClaudeRequest, resolve PolicyResolver) []string {
	if request == nil || resolve == nil {
		return nil
	}
	var dropped []string
	for _, field := range resolve(request.Model).RefusedSampling {
		switch field {
		case "temperature":
			if request.Temperature != nil {
				dropped = append(dropped, field)
			}
			request.Temperature = nil
		case "top_p":
			if request.TopP != 0 {
				dropped = append(dropped, field)
			}
			request.TopP = 0
		case "top_k":
			if request.TopK != 0 {
				dropped = append(dropped, field)
			}
			request.TopK = 0
		}
	}
	return dropped
}
