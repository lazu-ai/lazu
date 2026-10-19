package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func realtimeSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
		<-closed
		_ = conn.Close()
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	peer := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close(); close(closed); server.Close() })
	return client, peer
}

func TestOpenaiRealtimeTerminationRetainsProviderUsage(t *testing.T) {
	for _, ending := range []string{"client disconnect", "provider malformed event", "request cancellation"} {
		t.Run(ending, func(t *testing.T) {
			client, inbound := realtimeSocketPair(t)
			outbound, provider := realtimeSocketPair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{ClientWs: inbound, TargetWs: outbound, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
			info.UsePrice = true // usage collection does not issue segment charges.
			type result struct {
				err   *types.LazuError
				usage *dto.RealtimeUsage
			}
			done := make(chan result, 1)
			go func() { err, usage := OpenaiRealtimeHandler(c, info); done <- result{err, usage} }()
			if err := provider.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.done","response":{"usage":{"input_tokens":100,"output_tokens":35,"total_tokens":135,"input_token_details":{"text_tokens":100},"output_token_details":{"text_tokens":35}}}}`)); err != nil {
				t.Fatal(err)
			}
			_ = client.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err := client.ReadMessage(); err != nil {
				t.Fatalf("provider usage event was not forwarded: %v", err)
			}
			switch ending {
			case "client disconnect":
				_ = client.Close()
			case "provider malformed event":
				_ = provider.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.done"}`))
			case "request cancellation":
				cancel()
			}
			select {
			case got := <-done:
				if got.usage == nil || got.usage.InputTokens != 100 || got.usage.OutputTokens != 35 || got.usage.TotalTokens != 135 {
					t.Fatalf("termination lost or duplicated provider usage: %+v", got.usage)
				}
				if got.err == nil || !types.IsSkipRetryError(got.err) {
					t.Fatalf("terminated session must not replay client output: %v", got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("termination did not join socket readers")
			}
		})
	}
}

func TestAccumulateRealtimeUsagePreservesAllDetails(t *testing.T) {
	target := &dto.RealtimeUsage{}
	source := &dto.RealtimeUsage{
		InputTokens:  100,
		OutputTokens: 35,
		TotalTokens:  135,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens:           20,
			CachedTokensDetails:    dto.CachedTokenDetails{TextTokens: 12, AudioTokens: 6, ImageTokens: 2},
			CacheWriteTokens:       5,
			CacheMissTokens:        75,
			CachedCreationTokens:   5,
			CachedCreation5mTokens: 3,
			CachedCreation1hTokens: 2,
			TextTokens:             80,
			AudioTokens:            15,
			ImageTokens:            5,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:               20,
			AudioTokens:              10,
			ImageTokens:              5,
			ReasoningTokens:          8,
			AcceptedPredictionTokens: 4,
			RejectedPredictionTokens: 2,
		},
	}

	accumulateRealtimeUsage(target, source)

	if target.TotalTokens != 135 || target.InputTokens != 100 || target.OutputTokens != 35 {
		t.Fatalf("unexpected accumulated totals: %+v", target)
	}
	if target.InputTokenDetails != source.InputTokenDetails {
		t.Fatalf("input details were lost: got %+v want %+v", target.InputTokenDetails, source.InputTokenDetails)
	}
	if target.OutputTokenDetails != source.OutputTokenDetails {
		t.Fatalf("output details were lost: got %+v want %+v", target.OutputTokenDetails, source.OutputTokenDetails)
	}
}

func TestOpenaiRealtimeHandler_RejectsNilConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	err, usage := OpenaiRealtimeHandler(c, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatal("expected error when websocket connections are nil")
	}
	if err.GetErrorCode() != types.ErrorCodeBadResponse {
		t.Fatalf("unexpected error code: %s", err.GetErrorCode())
	}
	if usage != nil {
		t.Fatalf("expected nil usage on invalid websocket state, got %+v", usage)
	}
}
