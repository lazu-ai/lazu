package anthropic_messages

// GoldenChatRequests are the Chat Completions requests the encoder is held
// to the legacy converter's output on (testdata/golden/<name>.json, written
// by the golden generator in internal/relay/channel/claude while the legacy
// converter existed). They avoid what the encoder deliberately does
// differently: empty text blocks, http image URLs (now url sources), and
// thinking (now the channel's policy).
func GoldenChatRequests() map[string]string {
	return map[string]string{
		"plain_text":             `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"Say hello in five words."}],"max_tokens":200,"temperature":0.3,"top_p":0.9}`,
		"system_and_turns":       `{"model":"claude-haiku-5-5","messages":[{"role":"system","content":"You are terse."},{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello."},{"role":"user","content":"Again?"}],"stop":["END","STOP"]}`,
		"tools_and_results":      `{"model":"claude-haiku-5-5","tools":[{"type":"function","function":{"name":"get_weather","description":"Weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],"tool_choice":"required","messages":[{"role":"user","content":"Weather in Paris?"},{"role":"assistant","content":null,"tool_calls":[{"id":"toolu_01","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"toolu_01","content":"{\"temp\":18}"},{"role":"user","content":"And in Rome?"}]}`,
		"image_data_url":         `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":[{"type":"text","text":"What is this?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"}}]}]}`,
		"consecutive_user_turns": `{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"first"},{"role":"user","content":"second"}]}`,
		"assistant_first":        `{"model":"claude-haiku-5-5","messages":[{"role":"assistant","content":"I start."},{"role":"user","content":"Go on."}]}`,
		"tool_choice_named":      `{"model":"claude-haiku-5-5","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}},"parallel_tool_calls":false,"messages":[{"role":"user","content":"look"}]}`,
	}
}
