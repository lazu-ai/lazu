package service

import (
	"encoding/json"
	"fmt"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
)

func ValidateDecisionInputLimits(request *dto.DecisionRequest) error {
	caps := dto.DecisionCapabilitiesForModel(request.Model)
	if caps == nil {
		return types.NewError(fmt.Errorf("unsupported decision model"), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
	}
	state := request.InputText()
	stateTokens := CountTextToken(state, request.Model)
	// Clef silently truncates state. Until its tokenizer is exposed, use a
	// conservative UTF-8 byte ceiling, never a guessed exact token count.
	if caps.MaxStateBytes > 0 && len([]byte(state)) > caps.MaxStateBytes {
		return decisionInputTooLarge("Clef state exceeds the conservative 2000-byte safety limit")
	}
	if stateTokens > caps.MaxStateTokens {
		return decisionInputTooLarge("state exceeds the model input limit")
	}
	total := stateTokens
	for _, q := range request.QuestionTexts() {
		tokens := CountTextToken(q, request.Model)
		if stateTokens+tokens > 32768 {
			return decisionInputTooLarge("state plus question exceeds the model context")
		}
		total += tokens
	}
	if total > caps.MaxInputTokens {
		return decisionInputTooLarge("decision input exceeds the model input limit")
	}
	// Official Decisions is initially bounded to the standard short-context
	// price. Reserve image tokens as well so images cannot bypass that guard.
	if dto.IsOpenAIDecisionModel(request.Model) {
		estimated, err := EstimateRequestTPMTokens(request.GetTokenCountMeta(), nil)
		if err != nil {
			return err
		}
		if estimated > caps.MaxInputTokens {
			return decisionInputTooLarge("decision text and images exceed the model input limit")
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(body) > dto.DecisionMaxBodyBytes {
		return decisionInputTooLarge("encoded decision request exceeds 13 MiB")
	}
	return nil
}

func decisionInputTooLarge(message string) error {
	return types.NewError(fmt.Errorf("%s", message), types.ErrorCodeUnsupportedDecisionInput, types.ErrOptionWithSkipRetry())
}
