package constant

const (
	APITypeOpenAI = iota
	APITypeAnthropic
	APITypeGemini
	APITypeXai
	APITypeDummy // this one is only for count, do not add any channel after this
)

// responsesCapableAPITypes 列出支持 OpenAI Responses API (/v1/responses) 的 APIType。
// 此集合必须与 packages/domain/providercap/profile.go 中 SupportsResponses=true 的条目保持一致。
var responsesCapableAPITypes = map[int]bool{
	APITypeOpenAI: true,
}

// APITypeSupportsResponses 返回指定 APIType 是否支持 Responses API。
func APITypeSupportsResponses(apiType int) bool {
	return responsesCapableAPITypes[apiType]
}
