package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

func TestGetRequestURLBuildsAzureResponsesRouteFromDialect(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		version string
		want    string
	}{
		{name: "root preview", baseURL: "https://tenant.services.ai.azure.com", want: "https://tenant.services.ai.azure.com/openai/v1/responses?api-version=preview"},
		{name: "root latest", baseURL: "https://tenant.openai.azure.com", version: "latest", want: "https://tenant.openai.azure.com/openai/v1/responses?api-version=latest"},
		{name: "dated API", baseURL: "https://tenant.openai.azure.com", version: "2025-04-01-preview", want: "https://tenant.openai.azure.com/openai/responses?api-version=2025-04-01-preview"},
		{name: "normalized existing prefix", baseURL: "https://tenant.openai.azure.com/openai/v1", want: "https://tenant.openai.azure.com/openai/v1/responses?api-version=preview"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RequestURLPath: "/v1/responses",
				ProtocolPlan:   &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: providercap.DialectAzureOpenAI}},
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:          constant.ChannelTypeOpenAI,
					ChannelBaseUrl:       test.baseURL,
					ChannelOtherSettings: dto.ChannelOtherSettings{AzureResponsesVersion: test.version},
				},
			}
			got, err := (&Adaptor{}).GetRequestURL(info)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("GetRequestURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGetRequestURLKeepsOpenAIResponsesRoute(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/responses",
		ProtocolPlan:   &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "openai"}},
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://api.openai.com"},
	}
	got, err := (&Adaptor{}).GetRequestURL(info)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.openai.com/v1/responses" {
		t.Fatalf("GetRequestURL() = %q", got)
	}
}

func TestGetRequestURLBuildsAzureChatRouteFromDialect(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		version    string
		deployment string
		want       string
	}{
		{name: "root v1", baseURL: "https://tenant.services.ai.azure.com", deployment: "gpt-4o", want: "https://tenant.services.ai.azure.com/openai/v1/chat/completions"},
		{name: "normalized v1 prefix", baseURL: "https://tenant.openai.azure.com/openai/v1", version: "latest", deployment: "gpt-4o", want: "https://tenant.openai.azure.com/openai/v1/chat/completions"},
		{name: "dated deployment API", baseURL: "https://tenant.openai.azure.com", version: "2025-04-01-preview", deployment: "gpt-4o-prod", want: "https://tenant.openai.azure.com/openai/deployments/gpt-4o-prod/chat/completions?api-version=2025-04-01-preview"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RequestURLPath: "/v1/chat/completions",
				ProtocolPlan:   &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: providercap.DialectAzureOpenAI}},
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:          constant.ChannelTypeOpenAI,
					ChannelBaseUrl:       test.baseURL,
					UpstreamModelName:    test.deployment,
					ChannelOtherSettings: dto.ChannelOtherSettings{AzureResponsesVersion: test.version},
				},
			}
			got, err := (&Adaptor{}).GetRequestURL(info)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("GetRequestURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSetupRequestHeaderUsesAzureAPIKeyAndPreservesExplicitCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		ProtocolPlan: &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: providercap.DialectAzureOpenAI}},
		ChannelMeta:  &relaycommon.ChannelMeta{ApiKey: "azure-secret"},
	}

	header := http.Header{}
	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatal(err)
	}
	if header.Get("api-key") != "azure-secret" || header.Get("Authorization") != "" {
		t.Fatalf("Azure API-key credentials were not selected: %#v", header)
	}

	header = http.Header{"Authorization": []string{"Bearer entra-token"}}
	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatal(err)
	}
	if header.Get("Authorization") != "Bearer entra-token" || header.Get("api-key") != "" {
		t.Fatalf("explicit Entra bearer credentials were overwritten: %#v", header)
	}

	header = http.Header{"Api-Key": []string{"explicit-azure-key"}}
	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatal(err)
	}
	if header.Get("api-key") != "explicit-azure-key" || header.Get("Authorization") != "" {
		t.Fatalf("explicit Azure API-key credentials were overwritten: %#v", header)
	}
}
