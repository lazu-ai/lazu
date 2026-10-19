package textgen

import "github.com/lazu-ai/lazu/internal/dto"

type ParsedRequest struct {
	Client      Protocol
	Semantic    *Request
	Wire        dto.Request
	Features    FeatureSet
	Uses        []FeatureUse
	InputTokens int
}
