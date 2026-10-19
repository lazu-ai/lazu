package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

func TestInjectedClaudeEffortCarriesRequiredBeta(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, cap := range []string{"low", "minimal"} {
			for _, existing := range []string{"", "other-beta", "effort-2025-11-24,other-beta"} {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if !gjson.GetBytes(body, "output_config.effort").Exists() {
						t.Errorf("missing effort: %s", body)
					}
					betas := strings.Join(r.Header.Values("anthropic-beta"), ",")
					if strings.Count(betas, "effort-2025-11-24") != 1 {
						t.Errorf("required beta absent/duplicated: %s", betas)
					}
					if strings.Contains(existing, "other-beta") && !strings.Contains(betas, "other-beta") {
						t.Error("existing beta removed")
					}
					w.WriteHeader(http.StatusOK)
				}))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("anthropic-beta", existing)
				common.SetContextKey(c, constant.ContextKeyEffortCap, cap)
				info := &relaycommon.RelayInfo{RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic, ChannelType: constant.ChannelTypeAnthropic, UpstreamModelName: "claude-opus-4.6", ChannelBaseUrl: server.URL}}
				a := &Adaptor{}
				a.Init(info)
				response, err := a.DoRequest(c, info, strings.NewReader(`{"model":"claude-opus-4.6","messages":[]}`))
				if err != nil {
					server.Close()
					t.Fatal(err)
				}
				response.(*http.Response).Body.Close()
				server.Close()
			}
		}
	}
}
