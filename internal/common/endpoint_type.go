package common

import (
	"github.com/lazu-ai/lazu/internal/constant"
)

// GetEndpointTypesByChannelType 获取渠道支持的端点类型列表。
//
// 端点来源有三层：
//  1. 渠道类型决定基础端点（如 Anthropic 渠道 → anthropic + openai）
//  2. 渠道的 APIType 若支持 Responses API → 追加 openai-response
//  3. 模型名称若属于 Claude/Gemini 家族 → 追加对应原生端点
//
// 第 3 点确保：即使 Claude 模型配置在 OpenAI 兼容渠道上，模型广场也会显示
// /v1/messages 端点，因为系统的 adaptor 层已实现跨格式转换。
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	if IsDecisionModel(modelName) {
		return []constant.EndpointType{constant.EndpointTypeDecisions}
	}
	var endpointTypes []constant.EndpointType
	switch channelType {
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}

	// 对于支持 Responses API 的渠道，追加 openai-response 端点。
	// 渠道类型无法映射到 APIType 时，保持基础端点不变。
	apiType, ok := ChannelType2APIType(channelType)
	if ok && constant.APITypeSupportsResponses(apiType) {
		endpointTypes = appendEndpointIfMissing(endpointTypes, constant.EndpointTypeOpenAIResponse)
	}

	// 根据模型名称追加原生端点（adaptor 层已实现跨格式转换）
	if IsClaudeModel(modelName) {
		endpointTypes = appendEndpointIfMissing(endpointTypes, constant.EndpointTypeAnthropic)
	}
	if IsGeminiModel(modelName) {
		endpointTypes = appendEndpointIfMissing(endpointTypes, constant.EndpointTypeGemini)
	}

	if IsImageGenerationModel(modelName) {
		endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
	}
	return endpointTypes
}

func appendEndpointIfMissing(endpoints []constant.EndpointType, ep constant.EndpointType) []constant.EndpointType {
	for _, e := range endpoints {
		if e == ep {
			return endpoints
		}
	}
	return append(endpoints, ep)
}

// EndpointInfo 描述单个端点的默认请求信息
// path: 上游路径
// method: HTTP 请求方式，例如 POST/GET
// 目前均为 POST，后续可扩展
//
// json 标签用于直接序列化到 API 输出
// 例如：{"path":"/v1/chat/completions","method":"POST"}

type EndpointInfo struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

// defaultEndpointInfoMap 保存内置端点的默认 Path 与 Method
var defaultEndpointInfoMap = map[constant.EndpointType]EndpointInfo{
	constant.EndpointTypeDecisions:       {Path: "/v1/decisions", Method: "POST"},
	constant.EndpointTypeOpenAI:          {Path: "/v1/chat/completions", Method: "POST"},
	constant.EndpointTypeOpenAIResponse:  {Path: "/v1/responses", Method: "POST"},
	constant.EndpointTypeAnthropic:       {Path: "/v1/messages", Method: "POST"},
	constant.EndpointTypeGemini:          {Path: "/v1beta/models/{model}:generateContent", Method: "POST"},
	constant.EndpointTypeJinaRerank:      {Path: "/v1/rerank", Method: "POST"},
	constant.EndpointTypeImageGeneration: {Path: "/v1/images/generations", Method: "POST"},
	constant.EndpointTypeVideoGeneration: {Path: "/v1/videos", Method: "POST"},
	constant.EndpointTypeEmbeddings:      {Path: "/v1/embeddings", Method: "POST"},
}

// GetDefaultEndpointInfo 返回指定端点类型的默认信息以及是否存在
func GetDefaultEndpointInfo(et constant.EndpointType) (EndpointInfo, bool) {
	info, ok := defaultEndpointInfoMap[et]
	return info, ok
}
