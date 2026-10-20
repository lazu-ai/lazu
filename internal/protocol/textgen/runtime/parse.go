package runtime

import (
	"errors"
	"fmt"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
)

// ErrNoDecoder says the protocol has no request decoder.
var ErrNoDecoder = errors.New("no request decoder for protocol")

// Parse decodes a client body in the named protocol into the immutable
// ParsedRequest every later step plans from. The relay middleware and the
// channel probe share it, so a probe is planned exactly as a request is.
func Parse(family textgen.ProtocolFamily, body []byte) (*textgen.ParsedRequest, error) {
	var (
		semantic *textgen.Request
		wire     dto.Request
		err      error
	)
	switch family {
	case textgen.ProtocolOpenAIChat:
		semantic, wire, err = openai_chat.Decode(body)
	case textgen.ProtocolOpenAIResponses:
		semantic, wire, err = openai_responses.Decode(body)
	case textgen.ProtocolAnthropic:
		semantic, wire, err = anthropic_messages.Decode(body)
	default:
		return nil, fmt.Errorf("%w %q", ErrNoDecoder, family)
	}
	if err != nil {
		return nil, err
	}
	features, uses := textgen.ExtractFeatures(semantic)
	dialect := "openai"
	if family == textgen.ProtocolAnthropic {
		dialect = "anthropic"
	}
	return &textgen.ParsedRequest{Client: textgen.Protocol{Family: family, Dialect: dialect}, Semantic: semantic, Wire: wire, Features: features, Uses: uses}, nil
}
