package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
)

// Run explicitly with -benchtime=1x when comparing the maximum request size.
// This includes HTTP body read, typed/IR parsing and admission, not upstream I/O.
func BenchmarkRelayImageRequestAdmission(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ size, concurrent int }{{1 << 20, 1}, {(64 << 20) - 1024, 1}, {(64 << 20) - 1024, 2}} {
		size := tc.size
		b.Run(fmt.Sprintf("%dMiB/%d-inflight", (size+1024)/(1<<20), tc.concurrent), func(b *testing.B) {
			body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", size) + `"}}]}]}`
			router := gin.New()
			router.Use(RelayRequestBodyLimit(), TextgenParse(textgen.ProtocolOpenAIChat))
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				parsed, _ := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
				if parsed == nil {
					c.Status(500)
					return
				}
				_, err := service.EstimateRequestTPMTokens(textgen.TokenCountMeta(parsed.Semantic), &relaycommon.RelayInfo{OriginModelName: "gpt-4o-mini"})
				if err != nil {
					c.Status(500)
					return
				}
				c.Status(204)
			})
			b.ReportAllocs()
			b.SetBytes(int64(len(body) * tc.concurrent))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				codes := make(chan int, tc.concurrent)
				start := make(chan struct{})
				for n := 0; n < tc.concurrent; n++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						w := httptest.NewRecorder()
						r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
						r.Header.Set("Content-Type", "application/json")
						router.ServeHTTP(w, r)
						codes <- w.Code
					}()
				}
				close(start)
				wg.Wait()
				close(codes)
				for code := range codes {
					if code != 204 {
						b.Fatalf("request failed: %d", code)
					}
				}
			}
		})
	}
}

func TestCanceledChunkedUploadReleasesBodyReader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entered, exited := make(chan struct{}), make(chan error, 1)
	router := gin.New()
	router.Use(RelayRequestBodyLimit())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		close(entered)
		_, err := common.GetRequestBody(c)
		exited <- err
	})
	server := httptest.NewServer(router)
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		resp, _ := server.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if _, err := io.WriteString(writer, `{"model":"gpt-4o-mini","messages":`); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("server never entered body reader")
	}
	cancel()
	_ = writer.CloseWithError(context.Canceled)
	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("canceled partial upload treated as complete")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("body reader remained blocked after disconnect")
	}
	select {
	case <-clientDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not exit")
	}
}
