package openai

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"gorm.io/gorm"
)

// withEmptyModelCatalog gives the catalog an empty database, so sampling
// policy resolves for unknown models without a seeded catalog.
func withEmptyModelCatalog(tb testing.TB) {
	tb.Helper()
	previousDB := model.CoreDB()
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		tb.Fatal(err)
	}
	model.SetCoreDB(db)
	tb.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
		model.SetCoreDB(previousDB)
	})
}

// The top-level index must not change a single outbound byte: every body in
// this corpus, under every channel setting combination, converts exactly as
// the frozen pre-index implementation does.
func nativeRequestCorpus() [][]byte {
	long := strings.Repeat("lorem ipsum ", 4000)
	bodies := []string{
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		`{ "model" : "m", "stream":true, "messages":[{"role":"system","content":"s \u4e16 \/ x"},{"role":"user","content":"` + long + `"}], "vendor_number":9007199254740993 }`,
		`{"model":"m","stream":true,"stream_options":{"include_usage":false,"include_obfuscation":true},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","stream":true,"stream_options":{"include_obfuscation":true},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","metadata":{"lazu":{"route":"x"}},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","metadata":{"lazu":{"route":"x"},"caller":"keep"},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","max_tokens":4096,"temperature":0.4,"top_p":0.9,"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`,
		`{"model":"m","max_tokens":10,"max_completion_tokens":20,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","reasoning_effort":"high","temperature":1,"top_p":1,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","reasoning":{"effort":"low"},"temperature":1,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"none","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"1","extra_content":{"google":{"thought_signature":"x"}},"function":{"name":"f","arguments":"{}"}},{"id":"2","function":{"name":"g"}}]},{"role":"tool","content":"extra_content"}]}`,
		`{"model":"m","service_tier":"priority","store":true,"safety_identifier":"u","speed":"fast","inference_geo":"us","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","model":"dup","temperature":0.1,"temperature":0.2,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","metadata":{},"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","max_tokens":10,"max_tokens":99,"reasoning_effort":"low","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"m","input":"hello","temperature":0.5,"top_p":0.5,"metadata":{"lazu":{}},"stream_options":{"include_usage":true}}`,
		`{"model":"m","input":[{"role":"user","content":"` + long + `"}],"reasoning":{"effort":"medium"},"top_p":0.2}`,
		`[]`,
		`{"model":"m"`,
	}
	out := make([][]byte, len(bodies))
	for i, body := range bodies {
		out[i] = []byte(body)
	}
	return out
}

func nativeRequestInfos() []*relaycommon.RelayInfo {
	var infos []*relaycommon.RelayInfo
	for _, model := range []string{"", "m", "gpt-4o-mini", "gpt-5", "gpt-5-chat-latest", "o3", "o1-mini", "gpt-6-sol"} {
		for _, stream := range []bool{true, false} {
			for _, supports := range []bool{true, false} {
				for _, other := range []dto.ChannelOtherSettings{{}, {AllowServiceTier: true, AllowSafetyIdentifier: true, AllowIncludeObfuscation: true, DisableStore: true, AllowSpeed: true, AllowInferenceGeo: true}} {
					info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{
						ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: model, SupportStreamOptions: supports,
						ChannelOtherSettings: other,
					}}
					if model == "gpt-6-sol" {
						info.ProtocolPlan = &textgen.RelayPlan{Native: true, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"}}
					}
					infos = append(infos, info)
				}
			}
		}
	}
	return infos
}

func cloneInfo(info *relaycommon.RelayInfo) *relaycommon.RelayInfo {
	copied := *info
	meta := *info.ChannelMeta
	copied.ChannelMeta = &meta
	if info.ProtocolPlan != nil {
		plan := *info.ProtocolPlan
		copied.ProtocolPlan = &plan
	}
	return &copied
}

func TestNativeRequestIndexMatchesReference(t *testing.T) {
	withEmptyModelCatalog(t)
	previous := constant.ForceStreamOption
	defer func() { constant.ForceStreamOption = previous }()
	cases := 0
	for _, force := range []bool{true, false} {
		constant.ForceStreamOption = force
		for i, body := range nativeRequestCorpus() {
			for j, info := range nativeRequestInfos() {
				for _, responses := range []bool{false, true} {
					name := fmt.Sprintf("force=%t/body=%d/info=%d/responses=%t", force, i, j, responses)
					wantInfo, gotInfo := cloneInfo(info), cloneInfo(info)
					want, wantErr := convertNativeRequestReference(&Adaptor{}, append([]byte(nil), body...), wantInfo, responses)
					got, gotErr := (&Adaptor{}).ConvertNativeRequest(append([]byte(nil), body...), gotInfo, responses)
					if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(want, got) {
						t.Fatalf("%s:\nwant %s (%v)\ngot  %s (%v)", name, want, wantErr, got, gotErr)
					}
					if wantInfo.ReasoningEffort != gotInfo.ReasoningEffort {
						t.Fatalf("%s: reasoning effort %q, want %q", name, gotInfo.ReasoningEffort, wantInfo.ReasoningEffort)
					}
					cases++
				}
			}
		}
	}
	t.Logf("%d conversions identical", cases)
}

func benchmarkNativeBody(responses bool) []byte {
	text := strings.Repeat("lorem ipsum dolor sit amet ", (600<<10)/27)
	if responses {
		return []byte(`{"model":"gpt-4o-mini","stream":true,"input":"` + text + `"}`)
	}
	return []byte(`{"model":"gpt-4o-mini","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"` + text + `"}]}`)
}

func benchmarkNative(b *testing.B, responses bool, convert func([]byte, *relaycommon.RelayInfo) ([]byte, error)) {
	withEmptyModelCatalog(b)
	body := benchmarkNativeBody(responses)
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o-mini", SupportStreamOptions: true}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := convert(body, info); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNativeChat600KB(b *testing.B) {
	b.Run("reference", func(b *testing.B) {
		benchmarkNative(b, false, func(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
			return convertNativeRequestReference(&Adaptor{}, body, info, false)
		})
	})
	b.Run("indexed", func(b *testing.B) {
		benchmarkNative(b, false, func(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
			return (&Adaptor{}).ConvertNativeRequest(body, info, false)
		})
	})
}

func BenchmarkNativeResponses600KB(b *testing.B) {
	b.Run("reference", func(b *testing.B) {
		benchmarkNative(b, true, func(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
			return convertNativeRequestReference(&Adaptor{}, body, info, true)
		})
	})
	b.Run("indexed", func(b *testing.B) {
		benchmarkNative(b, true, func(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
			return (&Adaptor{}).ConvertNativeRequest(body, info, true)
		})
	})
}
