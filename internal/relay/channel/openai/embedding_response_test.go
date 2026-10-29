package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

// An embedding reply is served by the embedding handler, not the chat one:
// under force_format the chat handler re-marshals the body as a chat
// completion, which has no data field, and the vectors are lost.
func TestDoResponseServesEmbeddingsWithTheirOwnHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatEmbedding,
		RelayMode:   relayconstant.RelayModeEmbeddings,
		Request:     &dto.EmbeddingRequest{Model: "BAAI/bge-m3", Input: "doc"},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "BAAI/bge-m3",
			ChannelSetting:    dto.ChannelSettings{ForceFormat: true},
		},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.25,0.5]}],"model":"BAAI/bge-m3","usage":{"prompt_tokens":3,"total_tokens":3}}`)),
	}
	usage, err := (&Adaptor{}).DoResponse(c, resp, info)
	if err != nil {
		t.Fatal(err)
	}
	if got := usage.(*dto.Usage); got.PromptTokens != 3 || got.CompletionTokens != 0 {
		t.Fatalf("usage = %+v, want 3 input tokens and no output", got)
	}
	written := recorder.Body.String()
	if gjson.Get(written, "data.0.embedding.1").Float() != 0.5 {
		t.Fatalf("vectors did not reach the client: %s", written)
	}
	if gjson.Get(written, "choices").Exists() || gjson.Get(written, "usage.completion_tokens").Exists() {
		t.Fatalf("embedding reply was served as a chat completion: %s", written)
	}
}
