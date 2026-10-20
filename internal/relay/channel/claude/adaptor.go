package claude

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2
)

type Adaptor struct {
	RequestMode int
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return request, nil
}

// usesCompletionAPI says whether the model is served by the legacy
// /v1/complete API rather than Messages. Those models are retired upstream;
// the path stays for channels that still serve them.
func usesCompletionAPI(upstreamModel string) bool {
	return strings.HasPrefix(upstreamModel, "claude-2") || strings.HasPrefix(upstreamModel, "claude-instant")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	if usesCompletionAPI(info.UpstreamModelName) {
		a.RequestMode = RequestModeCompletion
	} else {
		a.RequestMode = RequestModeMessage
	}
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := ""
	if a.RequestMode == RequestModeMessage {
		baseURL = fmt.Sprintf("%s/v1/messages", info.ChannelBaseUrl)
	} else {
		baseURL = fmt.Sprintf("%s/v1/complete", info.ChannelBaseUrl)
	}
	if info.IsClaudeBetaQuery {
		baseURL = baseURL + "?beta=true"
	}
	return baseURL, nil
}

func CommonClaudeHeadersOperation(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) {
	// common headers operation
	anthropicBeta := strings.Join(c.Request.Header.Values("anthropic-beta"), ",")
	if anthropicBeta != "" {
		filtered := channel.FilterAnthropicBeta(anthropicBeta)
		if filtered != "" {
			req.Set("anthropic-beta", filtered)
		}
	}
	model_setting.GetClaudeSettings().WriteHeaders(info.OriginModelName, req)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("x-api-key", info.ApiKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Set("anthropic-version", anthropicVersion)
	CommonClaudeHeadersOperation(c, req, info)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if a.RequestMode == RequestModeCompletion {
		return RequestOpenAI2ClaudeComplete(*request), nil
	}
	// A Chat client on the Messages API is planned as a bridge and encoded by
	// the hub (BridgesToMessages, EncodeBridgedRequest); nothing converts the
	// Chat DTO here any more. What still arrives is a request no plan was
	// made for: the legacy /v1/completions route, which this model's API
	// never served (it has no prompt), so the answer is the same 4xx.
	return nil, errors.New("this model is served through /v1/chat/completions or /v1/messages; the legacy completions endpoint has no Messages equivalent")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.LazuError) {
	if BridgesToMessages(info) {
		if info.IsStream {
			return HubStreamHandler(c, resp, info)
		}
		return HubHandler(c, resp, info)
	}
	if info.IsStream {
		return ClaudeStreamHandler(c, resp, info, a.RequestMode)
	}
	return ClaudeHandler(c, resp, info, a.RequestMode)
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
