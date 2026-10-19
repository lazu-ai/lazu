package model

import (
	"database/sql/driver"
	"fmt"

	"github.com/lazu-ai/lazu/internal/common"
)

type ModelCapabilities struct {
	SupportsFunctionCalling         bool `json:"supports_function_calling"`
	SupportsParallelFunctionCalling bool `json:"supports_parallel_function_calling"`
	SupportsVision                  bool `json:"supports_vision"`
	SupportsReasoning               bool `json:"supports_reasoning"`
	SupportsResponseSchema          bool `json:"supports_response_schema"`
	SupportsPromptCaching           bool `json:"supports_prompt_caching"`
	SupportsAudioInput              bool `json:"supports_audio_input"`
	SupportsAudioOutput             bool `json:"supports_audio_output"`
	SupportsWebSearch               bool `json:"supports_web_search"`
	SupportsPDFInput                bool `json:"supports_pdf_input"`
	SupportsComputerUse             bool `json:"supports_computer_use"`
}

func (c ModelCapabilities) Value() (driver.Value, error) {
	bytes, err := common.Marshal(c)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func (c *ModelCapabilities) Scan(value interface{}) error {
	if value == nil {
		*c = ModelCapabilities{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for ModelCapabilities", value)
	}
	if len(bytes) == 0 {
		*c = ModelCapabilities{}
		return nil
	}
	return common.Unmarshal(bytes, c)
}

func (c ModelCapabilities) HasAny() bool {
	return c.SupportsFunctionCalling ||
		c.SupportsParallelFunctionCalling ||
		c.SupportsVision ||
		c.SupportsReasoning ||
		c.SupportsResponseSchema ||
		c.SupportsPromptCaching ||
		c.SupportsAudioInput ||
		c.SupportsAudioOutput ||
		c.SupportsWebSearch ||
		c.SupportsPDFInput ||
		c.SupportsComputerUse
}
