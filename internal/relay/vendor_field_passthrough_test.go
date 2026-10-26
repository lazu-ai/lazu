package relay

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// Vendor-specific top-level fields (Qwen, Ollama, Baidu, Doubao/Zhipu,
// Perplexity) reach OpenAI-compatible upstreams byte for byte through the
// unknown-field merge, on both the same-protocol and the degraded-IR path,
// whether or not the request DTO declares them.
var vendorTopLevelFields = map[string]string{
	"vl_high_resolution_images": `true`,
	"enable_thinking":           `false`,
	"think":                     `{"budget":512}`,
	"web_search":                `{"enable":true,"search_mode":"deep"}`,
	"thinking":                  `{"type":"disabled"}`,
	"search_domain_filter":      `["example.org","-spam.example"]`,
	"search_recency_filter":     `"week"`,
	"return_images":             `false`,
	"return_related_questions":  `true`,
	"search_mode":               `"academic"`,
}

func vendorFieldRequestBody(t *testing.T) []byte {
	t.Helper()
	body := map[string]json.RawMessage{
		"model":    json.RawMessage(`"vendor-model"`),
		"messages": json.RawMessage(`[{"role":"user","content":"hi"}]`),
	}
	for key, value := range vendorTopLevelFields {
		body[key] = json.RawMessage(value)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertVendorFieldsKept(t *testing.T, out []byte) {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("outbound body is not an object: %v", err)
	}
	for key, want := range vendorTopLevelFields {
		value, ok := got[key]
		if !ok {
			t.Errorf("%s dropped from the outbound body", key)
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			t.Fatal(err)
		}
		if compact.String() != want {
			t.Errorf("%s = %s, want %s", key, compact.String(), want)
		}
	}
}

func TestVendorTopLevelFieldsSurviveSameProtocolRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := vendorFieldRequestBody(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	c.Set(common.KeyRequestBody, body)

	var request dto.GeneralOpenAIRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	encoded, err := common.Marshal(&request)
	if err != nil {
		t.Fatal(err)
	}
	out, err := mergeClientUnknownFields(c, &relaycommon.RelayInfo{}, encoded, false)
	if err != nil {
		t.Fatal(err)
	}
	assertVendorFieldsKept(t, out)
}

func TestVendorTopLevelFieldsSurviveDegradedIRRelay(t *testing.T) {
	body := vendorFieldRequestBody(t)
	semantic, wire, err := openai_chat.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	for key := range vendorTopLevelFields {
		if _, ok := semantic.Raw.UnknownFields[key]; !ok {
			t.Fatalf("%s is not retained as an unknown field by the chat codec", key)
		}
	}
	encoded, err := common.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	out, err := mergeClientUnknownFields(nil, &relaycommon.RelayInfo{ProtocolRequest: semantic}, encoded, true)
	if err != nil {
		t.Fatal(err)
	}
	assertVendorFieldsKept(t, out)
}
