package textgen

import (
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
)

type ParsedRequest struct {
	Client      Protocol
	Semantic    *Request
	Wire        dto.Request
	Features    FeatureSet
	Uses        []FeatureUse
	InputTokens int

	// tokenMeta caches TokenCountMeta(tokenMetaFor). A copy of the struct
	// keeps the cache; it is reused only while Semantic is still the request
	// it was computed from.
	tokenMeta    *types.TokenCountMeta
	tokenMetaFor *Request
}

// TokenCountMeta returns TokenCountMeta(p.Semantic), computing it once per
// Semantic. Each call returns its own copy (struct and Files slice), so a
// caller that edits the result does not change what the next caller sees.
func (p *ParsedRequest) TokenCountMeta() *types.TokenCountMeta {
	if p == nil || p.Semantic == nil {
		return TokenCountMeta(nil)
	}
	if p.tokenMeta == nil || p.tokenMetaFor != p.Semantic {
		p.tokenMeta, p.tokenMetaFor = TokenCountMeta(p.Semantic), p.Semantic
	}
	meta := *p.tokenMeta
	meta.Files = append([]*types.FileMeta(nil), p.tokenMeta.Files...)
	return &meta
}
