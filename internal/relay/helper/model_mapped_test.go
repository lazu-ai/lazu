package helper

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func mappedAttempt(c *gin.Context, mapping string) *relaycommon.RelayInfo {
	c.Set("model_mapping", mapping)
	info := &relaycommon.RelayInfo{
		OriginModelName: "BAAI/bge-m3",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "BAAI/bge-m3"},
	}
	return info
}

// The public name is recorded for a mapped attempt and cleared for the next
// attempt on a channel without the mapping, so a retry stops rewriting.
func TestModelMappedHelperRecordsThePublicModelPerAttempt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	request := &dto.EmbeddingRequest{Model: "BAAI/bge-m3", Input: "x"}
	if err := ModelMappedHelper(c, mappedAttempt(c, `{"BAAI/bge-m3":"@cf/baai/bge-m3"}`), request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "@cf/baai/bge-m3" {
		t.Fatalf("upstream request model = %q", request.Model)
	}
	if got := c.GetString(string(constant.ContextKeyPublicModel)); got != "BAAI/bge-m3" {
		t.Fatalf("public model = %q, want the caller's name", got)
	}
	if err := StringData(c, `{"object":"list","model":"@cf/baai/bge-m3"}`); err != nil {
		t.Fatal(err)
	}
	if body := recorder.Body.String(); strings.Contains(body, "@cf/") || !strings.Contains(body, `"model":"BAAI/bge-m3"`) {
		t.Fatalf("stream frame still names the upstream model: %q", body)
	}

	if err := ModelMappedHelper(c, mappedAttempt(c, ""), &dto.EmbeddingRequest{Model: "BAAI/bge-m3"}); err != nil {
		t.Fatal(err)
	}
	if got := c.GetString(string(constant.ContextKeyPublicModel)); got != "" {
		t.Fatalf("public model = %q after an unmapped attempt, want none", got)
	}
}
