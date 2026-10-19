package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func fullShadowCount(task protocolTokenShadowTask) (int, error) {
	return countRequestTokens(&gin.Context{}, task.meta, &relaycommon.RelayInfo{OriginModelName: task.model, RelayFormat: task.format, IsStream: task.stream})
}

func shadowMemoFlags(t testing.TB) {
	t.Helper()
	previousEnabled, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() { constant.GetMediaToken, constant.GetMediaTokenNotStream = previousEnabled, previousNonStream })
}

func TestProtocolTokenShadowMemoMatchesFullCountAndSeparatesInputs(t *testing.T) {
	shadowMemoFlags(t)
	memo := newProtocolTokenShadowMemo()
	base := protocolTokenShadowTask{model: "gpt-4o", format: types.RelayFormatOpenAI, stream: true, meta: &types.TokenCountMeta{CombineText: "Hello 世界 👋", MessagesCount: 1}}
	variants := []struct {
		name string
		edit func(*protocolTokenShadowTask)
	}{
		{"base", func(*protocolTokenShadowTask) {}},
		{"text", func(task *protocolTokenShadowTask) { task.meta.CombineText += "!" }},
		{"model", func(task *protocolTokenShadowTask) { task.model = "gpt-3.5-turbo" }},
		{"format", func(task *protocolTokenShadowTask) { task.format = types.RelayFormatOpenAIResponses }},
		{"stream", func(task *protocolTokenShadowTask) { task.stream = false }},
		{"token_type", func(task *protocolTokenShadowTask) { task.meta.TokenType = types.TokenTypeTextNumber }},
		{"tools", func(task *protocolTokenShadowTask) { task.meta.ToolsCount++ }},
		{"messages", func(task *protocolTokenShadowTask) { task.meta.MessagesCount++ }},
		{"names", func(task *protocolTokenShadowTask) { task.meta.NameCount++ }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
			task := base
			meta := *base.meta
			task.meta = &meta
			variant.edit(&task)
			want, err := fullShadowCount(task)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			for i := 0; i < 2; i++ {
				got, err := memo.count(task, func() (int, error) { calls++; return fullShadowCount(task) })
				if err != nil || got != want {
					t.Fatalf("count=%d want=%d err=%v", got, want, err)
				}
			}
			if calls != 1 {
				t.Fatalf("different count input aliased an earlier entry, or repeat missed: %d computations", calls)
			}
		})
	}
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	base.requestID, base.admission = "another-request", 900
	if _, err := memo.count(base, func() (int, error) { t.Fatal("request attribution should not change exact count"); return 0, nil }); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolTokenShadowIgnoresLegacyBillingCountSwitches(t *testing.T) {
	shadowMemoFlags(t)
	for _, stream := range []bool{false, true} {
		memo := newProtocolTokenShadowMemo()
		task := protocolTokenShadowTask{model: "gpt-4o", format: types.RelayFormatOpenAI, stream: stream, meta: &types.TokenCountMeta{CombineText: "background shadow still counts this text", MessagesCount: 1}}
		want, err := fullShadowCount(task)
		if err != nil || want <= 0 {
			t.Fatalf("full shadow count=%d err=%v", want, err)
		}
		computations := 0
		for _, flags := range [][2]bool{{true, true}, {false, true}, {true, false}, {false, false}} {
			constant.GetMediaToken, constant.GetMediaTokenNotStream = flags[0], flags[1]
			got, err := memo.count(task, func() (int, error) {
				computations++
				return fullShadowCount(task)
			})
			if err != nil || got != want {
				t.Fatalf("legacy flags changed shadow: %v stream=%v count=%d want=%d err=%v", flags, stream, got, want, err)
			}
			if !flags[0] || !flags[1] && !stream {
				legacy, err := CountRequestToken(&gin.Context{}, task.meta, &relaycommon.RelayInfo{OriginModelName: task.model, RelayFormat: task.format, IsStream: stream})
				if err != nil || legacy != 0 {
					t.Fatalf("legacy billing fallback switch changed: count=%d err=%v", legacy, err)
				}
			}
		}
		if computations != 1 {
			t.Fatalf("legacy flags invalidated shadow memo: %d computations", computations)
		}
		constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	}
}

func TestProtocolTokenShadowMemoBoundsRetentionAndBypassesMediaAndErrors(t *testing.T) {
	shadowMemoFlags(t)
	memo := newProtocolTokenShadowMemo()
	for i := 0; i <= protocolTokenShadowMemoCapacity; i++ {
		task := protocolTokenShadowTask{meta: &types.TokenCountMeta{CombineText: fmt.Sprint(i)}}
		if _, err := memo.count(task, func() (int, error) { return i, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(memo.counts) != protocolTokenShadowMemoCapacity {
		t.Fatalf("unbounded cache: %d", len(memo.counts))
	}
	miss := 0
	_, _ = memo.count(protocolTokenShadowTask{meta: &types.TokenCountMeta{CombineText: "0"}}, func() (int, error) { miss++; return 0, nil })
	if miss != 1 {
		t.Fatal("oldest text was not evicted")
	}
	for _, task := range []protocolTokenShadowTask{
		{meta: &types.TokenCountMeta{Files: []*types.FileMeta{nil}}},
		{meta: &types.TokenCountMeta{Files: []*types.FileMeta{{}}}},
		{meta: nil},
		{model: strings.Repeat("m", 257), meta: &types.TokenCountMeta{}},
		{meta: &types.TokenCountMeta{CombineText: "error"}},
	} {
		calls := 0
		for i := 0; i < 2; i++ {
			_, err := memo.count(task, func() (int, error) { calls++; return 0, errors.New("count failed") })
			if err == nil {
				t.Fatal("lost full count error")
			}
		}
		if calls != 2 {
			t.Fatal("media or errors were cached")
		}
	}
	// Media bypasses even when its current count succeeds (for example when
	// disabled); it must never retain a media result for subsequent work.
	media := protocolTokenShadowTask{meta: &types.TokenCountMeta{Files: []*types.FileMeta{nil}}}
	calls := 0
	for i := 0; i < 2; i++ {
		_, _ = memo.count(media, func() (int, error) { calls++; return 0, nil })
	}
	if calls != 2 {
		t.Fatal("successful media count cached")
	}
}

func BenchmarkProtocolTokenShadowMemo(b *testing.B) {
	shadowMemoFlags(b)
	for _, size := range []int{100 << 10, 600 << 10} {
		base := protocolTokenShadowTask{model: "gpt-4o", format: types.RelayFormatOpenAI, stream: true, meta: &types.TokenCountMeta{CombineText: strings.Repeat("hello world ", size/12), MessagesCount: 1}}
		for _, mode := range []string{"full", "repeat", "unique"} {
			b.Run(fmt.Sprintf("%dKB/%s", size>>10, mode), func(b *testing.B) {
				memo := newProtocolTokenShadowMemo()
				_, _ = memo.count(base, func() (int, error) { return fullShadowCount(base) })
				var unique []*types.TokenCountMeta
				if mode == "unique" {
					unique = make([]*types.TokenCountMeta, b.N)
					for i := range unique {
						meta := *base.meta
						meta.CombineText += fmt.Sprint(i)
						unique[i] = &meta
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					task := base
					if mode == "unique" {
						task.meta = unique[i]
					}
					var tokens int
					var err error
					if mode == "full" {
						tokens, err = fullShadowCount(task)
					} else {
						tokens, err = memo.count(task, func() (int, error) { return fullShadowCount(task) })
					}
					if err != nil || tokens <= 0 {
						b.Fatalf("full text count lost: tokens=%d err=%v", tokens, err)
					}
				}
			})
		}
	}
}
