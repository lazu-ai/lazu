package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// Validate the synthetic non-streaming provider contract, rather than treating
// an arbitrary HTTP 200 body as a successful Decisions response.
func validateFixtureDecision(body io.Reader, api, model string) error {
	var response struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
		Usage   struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Total  int `json:"total_tokens"`
		} `json:"usage"`
	}
	decoder := json.NewDecoder(io.LimitReader(body, 1<<20))
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("invalid decision JSON: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected trailing decision JSON")
	}
	if response.Model != model || response.Usage.Input <= 0 || response.Usage.Output < 0 {
		return fmt.Errorf("decision model or provider usage differs from fixture")
	}
	if api == "decisions-openai" {
		var answers []struct {
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			Probability *float64 `json:"probability"`
		}
		if json.Unmarshal(response.Answers, &answers) != nil || len(answers) != 1 || answers[0].Name != "fixture" || answers[0].Type != "predicate" || answers[0].Probability == nil || *answers[0].Probability != .99 || response.Usage.Total != response.Usage.Input+response.Usage.Output {
			return fmt.Errorf("native decision answers or total usage differ from fixture")
		}
	} else {
		var answers map[string]struct {
			Type string   `json:"type"`
			Noul *float64 `json:"noul"`
		}
		if json.Unmarshal(response.Answers, &answers) != nil || len(answers) != 1 || answers["fixture"].Type != "noul" || answers["fixture"].Noul == nil || *answers["fixture"].Noul != .99 {
			return fmt.Errorf("legacy decision answers differ from fixture")
		}
	}
	return nil
}
