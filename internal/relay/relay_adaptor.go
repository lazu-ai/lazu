package relay

import (
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	"github.com/lazu-ai/lazu/internal/relay/channel/claude"
	"github.com/lazu-ai/lazu/internal/relay/channel/gemini"
	"github.com/lazu-ai/lazu/internal/relay/channel/openai"
	"github.com/lazu-ai/lazu/internal/relay/channel/xai"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

var adaptorFactories = map[int]func() channel.BaseAdaptor{
	constant.APITypeOpenAI:    func() channel.BaseAdaptor { return &openai.Adaptor{} },
	constant.APITypeAnthropic: func() channel.BaseAdaptor { return &claude.Adaptor{} },
	constant.APITypeGemini:    func() channel.BaseAdaptor { return &gemini.Adaptor{} },
	constant.APITypeXai:       func() channel.BaseAdaptor { return &xai.Adaptor{} },
}

var familyAdaptorFactories = map[providercap.ProtocolFamily]func() channel.BaseAdaptor{
	providercap.ProtocolFamilyOpenAI: func() channel.BaseAdaptor { return &openai.Adaptor{} },
	providercap.ProtocolFamilyClaude: func() channel.BaseAdaptor { return &claude.Adaptor{} },
	providercap.ProtocolFamilyGemini: func() channel.BaseAdaptor { return &gemini.Adaptor{} },
}

func GetAdaptor(apiType int) channel.BaseAdaptor {
	if factory, ok := adaptorFactories[apiType]; ok {
		return factory()
	}
	if familyFactory, ok := familyAdaptorFactories[providercap.ProtocolFamilyOf(apiType)]; ok {
		return familyFactory()
	}
	return nil
}
