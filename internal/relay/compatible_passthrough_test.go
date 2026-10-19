package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

type captureRawAdaptor struct {
	channel.BaseAdaptor // Deliberately has no ConvertNativeRequest method.
	body                []byte
}

func (a *captureRawAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *captureRawAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (interface{}, error) {
	a.body, _ = io.ReadAll(body)
	return nil, errors.New("stop after outbound capture")
}

func TestRawPassThroughStripsPrivateMetadataWithoutMutatingRetryBody(t *testing.T) {
	for _, metadata := range []string{`{"lazu":{"route":"private"}}`, `{"lazu":{"route":"private"},"caller":"keep","number":9007199254740993}`} {
		t.Run(metadata, func(t *testing.T) {
			raw := []byte(`{"model":"test","messages":[{"role":"user","content":"hi"}],"vendor_number":9007199254740993,"metadata":` + metadata + `}`)
			var request dto.GeneralOpenAIRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			originalMetadata := append([]byte(nil), request.Metadata...)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(raw))
			c.Set(common.KeyRequestBody, raw)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "test")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
			adapter := &captureRawAdaptor{}
			old := adaptorFactories[constant.APITypeOpenAI]
			adaptorFactories[constant.APITypeOpenAI] = func() channel.BaseAdaptor { return adapter }
			defer func() { adaptorFactories[constant.APITypeOpenAI] = old }()
			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "test"}
			for attempt := 0; attempt < 2; attempt++ {
				_ = TextHelper(c, info)
				if len(adapter.body) == 0 {
					t.Fatal("pass-through did not reach the adapter")
				}
				if gjson.GetBytes(adapter.body, "metadata.lazu").Exists() {
					t.Fatalf("private metadata leaked: %s", adapter.body)
				}
				if gjson.GetBytes(adapter.body, "vendor_number").Raw != "9007199254740993" {
					t.Fatal("unknown field changed")
				}
				if gjson.Get(metadata, "caller").Exists() {
					if gjson.GetBytes(adapter.body, "metadata.caller").Str != "keep" || gjson.GetBytes(adapter.body, "metadata.number").Raw != "9007199254740993" {
						t.Fatal("caller metadata changed")
					}
				} else if gjson.GetBytes(adapter.body, "metadata").Exists() {
					t.Fatal("empty metadata retained")
				}
				cached, _ := common.GetRequestBody(c)
				if !bytes.Equal(cached, raw) || !bytes.Equal(request.Metadata, originalMetadata) {
					t.Fatal("retry input mutated")
				}
			}
		})
	}
}
