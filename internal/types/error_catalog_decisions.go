package types

import "net/http"

const (
	ErrorCodeMissingQuestions             ErrorCode = "missing_questions"
	ErrorCodeInvalidDecisionState         ErrorCode = "invalid_decision_state"
	ErrorCodeInvalidDecisionQuestions     ErrorCode = "invalid_decision_questions"
	ErrorCodeUnsupportedQuestionType      ErrorCode = "unsupported_question_type"
	ErrorCodeUnsupportedDecisionInput     ErrorCode = "unsupported_decision_input"
	ErrorCodeUnsupportedDecisionParameter ErrorCode = "unsupported_decision_parameter"
	ErrorCodeQuestionGenerationFailed     ErrorCode = "question_generation_failed"
	ErrorCodeUpstreamDecisionInvalid      ErrorCode = "upstream_decision_invalid"
	ErrorCodeUpstreamCapacity             ErrorCode = "upstream_capacity"
)

func init() {
	RegisterErrors(map[ErrorCode]ErrorSpec{
		ErrorCodeMissingQuestions:             {ID: "LZ-2601", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Supply questions or enable question_generation."},
		ErrorCodeInvalidDecisionState:         {ID: "LZ-2602", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "Supply a state containing text or JSON, or embedded images."},
		ErrorCodeInvalidDecisionQuestions:     {ID: "LZ-2603", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "The decision questions are invalid."},
		ErrorCodeUnsupportedQuestionType:      {ID: "LZ-2604", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This decision question type is not supported."},
		ErrorCodeUnsupportedDecisionInput:     {ID: "LZ-2605", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This input is not supported by the decision model."},
		ErrorCodeUnsupportedDecisionParameter: {ID: "LZ-2606", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer, Template: "This decision parameter is not supported. Decisions do not support streaming."},
		ErrorCodeQuestionGenerationFailed:     {ID: "LZ-2607", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer, Template: "Question generation did not produce usable questions. The generation charge will be refunded."},
		ErrorCodeUpstreamDecisionInvalid:      {ID: "LZ-2608", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator, Template: "The decision provider returned an invalid response.", Retry: true},
		ErrorCodeUpstreamCapacity:             {ID: "LZ-2609", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer, Template: "Available upstream capacity is exhausted. Retry after the indicated delay.", Retry: true},
	})
}
