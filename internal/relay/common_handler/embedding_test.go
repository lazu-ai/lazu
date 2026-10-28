package common_handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

const embeddingReply = `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]},{"object":"embedding","index":1,"embedding":[0.4,0.5,0.6]}],"model":"BAAI/bge-m3"%s}`

func embeddingContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	return c, recorder
}

func embeddingInfo(forceFormat bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatEmbedding,
		RelayMode:   relayconstant.RelayModeEmbeddings,
		Request:     &dto.EmbeddingRequest{Model: "BAAI/bge-m3", Input: []any{"doc one", "doc two"}},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "BAAI/bge-m3",
			ChannelSetting:    dto.ChannelSettings{ForceFormat: forceFormat},
		},
	}
}

func upstreamReply(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func setSwitch(t *testing.T, target *bool, value bool) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// The upstream's count is billed and the vectors reach the client untouched,
// force_format included: the chat handler that used to serve these replies
// re-marshalled them as a chat completion, which has no data field.
func TestEmbeddingHandlerKeepsVectorsAndUpstreamUsage(t *testing.T) {
	for _, forceFormat := range []bool{false, true} {
		c, recorder := embeddingContext()
		body := strings.Replace(embeddingReply, "%s", `,"usage":{"prompt_tokens":11,"total_tokens":11}`, 1)
		usage, err := EmbeddingHandler(c, embeddingInfo(forceFormat), upstreamReply(body))
		if err != nil {
			t.Fatalf("force_format=%t: %v", forceFormat, err)
		}
		if usage.PromptTokens != 11 || usage.CompletionTokens != 0 || usage.TotalTokens != 11 {
			t.Fatalf("force_format=%t: usage = %+v, want the provider's 11 input tokens and no output", forceFormat, usage)
		}
		if usage.Measurement().Input != dto.UsageSourceProvider {
			t.Fatalf("force_format=%t: input source = %s, want provider", forceFormat, usage.Measurement().Input)
		}
		written := recorder.Body.String()
		if got := gjson.Get(written, "data.1.embedding.2").Float(); got != 0.6 {
			t.Fatalf("force_format=%t: vectors did not pass through: %s", forceFormat, written)
		}
		if gjson.Get(written, "usage.prompt_tokens").Int() != 11 || gjson.Get(written, "usage.total_tokens").Int() != 11 {
			t.Fatalf("force_format=%t: usage not reported: %s", forceFormat, written)
		}
		for _, chatOnly := range []string{"usage.completion_tokens", "usage.prompt_tokens_details", "usage.completion_tokens_details", "choices"} {
			if gjson.Get(written, chatOnly).Exists() {
				t.Fatalf("force_format=%t: %s is a chat field and does not belong on an embedding reply: %s", forceFormat, chatOnly, written)
			}
		}
	}
}

// Without upstream usage the input is estimated locally when the switches
// allow it; an embedding never has output, whatever the switches say.
func TestEmbeddingHandlerEstimatesInputWhenUpstreamOmitsUsage(t *testing.T) {
	for _, mediaToken := range []bool{true, false} {
		setSwitch(t, &constant.GetMediaToken, mediaToken)
		setSwitch(t, &constant.GetMediaTokenNotStream, true)
		c, recorder := embeddingContext()
		usage, err := EmbeddingHandler(c, embeddingInfo(false), upstreamReply(strings.Replace(embeddingReply, "%s", "", 1)))
		if err != nil {
			t.Fatal(err)
		}
		if mediaToken {
			if usage.PromptTokens <= 0 || usage.Measurement().Input != dto.UsageSourceLocalEstimate {
				t.Fatalf("GET_MEDIA_TOKEN=%t: usage = %+v, want a local estimate", mediaToken, usage)
			}
		} else if usage.PromptTokens != 0 {
			t.Fatalf("GET_MEDIA_TOKEN=%t: usage = %+v, want no prompt tokens", mediaToken, usage)
		}
		if usage.CompletionTokens != 0 || usage.TotalTokens != usage.PromptTokens {
			t.Fatalf("GET_MEDIA_TOKEN=%t: usage = %+v, want no output", mediaToken, usage)
		}
		if got := gjson.Get(recorder.Body.String(), "usage.total_tokens").Int(); int(got) != usage.PromptTokens {
			t.Fatalf("GET_MEDIA_TOKEN=%t: reported total %d, billed %d", mediaToken, got, usage.PromptTokens)
		}
	}
}

// An upstream that reports output tokens on an embedding (some count the
// vectors) is not billed for them; the Responses-style input_tokens alias is
// read as the prompt.
func TestEmbeddingHandlerNormalisesUpstreamUsageAliases(t *testing.T) {
	c, _ := embeddingContext()
	body := strings.Replace(embeddingReply, "%s", `,"usage":{"input_tokens":7,"output_tokens":2048,"total_tokens":2055}`, 1)
	usage, err := EmbeddingHandler(c, embeddingInfo(false), upstreamReply(body))
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v, want 7 input tokens and no output", usage)
	}
}

func TestEmbeddingHandlerSurfacesUpstreamError(t *testing.T) {
	c, _ := embeddingContext()
	resp := upstreamReply(`{"error":{"message":"model not found","type":"invalid_request_error","code":"model_not_found"}}`)
	resp.StatusCode = http.StatusNotFound
	if _, err := EmbeddingHandler(c, embeddingInfo(false), resp); err == nil {
		t.Fatal("an upstream error body must be returned as an error, not served as a reply")
	}
}
