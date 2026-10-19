package router

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// relayRetriesForExhaustedFailover is how many times the fallback channel is
// reached when every channel keeps failing: one cross-channel switch, then the
// attempts left in the retry budget, all against that same channel.
const relayRetriesForExhaustedFailover = 3

func TestFailedStreamsRefundOrFailOverWithoutDoubleCharging(t *testing.T) {
	const content = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"
	const usage = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n"
	for _, tc := range []struct {
		name, prefix                  string
		reset, fallbackReset, perCall bool
		wantFallback                  int32
		wantCharge                    billing.MicroUSD
	}{
		{name: "malformed", prefix: "data: not-json\n\n"},
		{name: "metadata only", prefix: "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"},
		{name: "heartbeat only", prefix: "data: \n\n"},
		{name: "per-call malformed", prefix: "data: not-json\n\n", perCall: true},
		{name: "reset before event", reset: true, wantFallback: 1, wantCharge: 60},
		// Once the single channel switch is spent, the remaining attempts retry
		// the channel in place rather than abandoning the request, so the
		// fallback sees the switch plus the leftover same-channel retries.
		{name: "all channels reset", reset: true, fallbackReset: true, wantFallback: relayRetriesForExhaustedFailover},
		{name: "reset after billable output", prefix: content + usage, reset: true, wantCharge: 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := setupModelCatalogRouteTestDB(t)
			defer cleanup()
			oldLog, oldSQLite, oldMemory := model.ConfiguredTelemetryDB(), common.UsingSQLite, common.MemoryCacheEnabled
			model.SetTelemetryDB(model.CoreDB())
			common.UsingSQLite = true
			common.MemoryCacheEnabled = false
			defer func() {
				drainRelayDiagnosticFixture(t)
				model.SetTelemetryDB(oldLog)
				common.UsingSQLite = oldSQLite
				common.MemoryCacheEnabled = oldMemory
			}()
			if err := model.CoreDB().AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().AutoMigrate(&model.Log{}, &model.WalletLedger{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}, &model.TelemetryEventReceipt{}); err != nil {
				t.Fatal(err)
			}
			var primaryCalls, fallbackCalls atomic.Int32
			respond := func(w http.ResponseWriter, prefix string, reset bool) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				if reset {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close() // No terminating HTTP chunk: upstream reader fails.
				}
			}
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				primaryCalls.Add(1)
				respond(w, tc.prefix, tc.reset)
			}))
			defer primary.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls.Add(1)
				if tc.fallbackReset {
					respond(w, "", true)
					return
				}
				respond(w, content+usage+"data: [DONE]\n\n", false)
			}))
			defer fallback.Close()
			allowLocalRelayUpstream(t, primary.URL)
			allowLocalRelayUpstream(t, fallback.URL)
			if err := model.CoreDB().Create(&model.User{Id: 991, Username: "stream-accounting", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
				t.Fatal(err)
			}
			key := &model.Token{Id: 991, UserId: 991, Key: "stream-accounting-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
			if err := key.Insert(); err != nil {
				t.Fatal(err)
			}
			for i, url := range []string{primary.URL, fallback.URL} {
				priority, noBan := int64(10-i*10), 0
				channel := model.Channel{Id: 991 + i, Type: constant.ChannelTypeOpenAI, Key: "local-upstream", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("stream-%d", i), Models: "stream-fixture", Group: model.ChannelLaneDirect, BaseURL: &url, Priority: &priority, AutoBan: &noBan, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
				if err := model.CoreDB().Create(&channel).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: "stream-fixture", Enabled: true, Priority: &priority}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := model.CoreDB().Create(&model.Model{ModelName: "stream-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
				t.Fatal(err)
			}
			inputPrice, outputPrice, requestPrice := 1.0, 10.0, 0.01
			price := model.ModelSellPrice{ModelName: "stream-fixture", ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}
			if tc.perCall {
				price.InputPerMtok, price.OutputPerMtok, price.RequestUSD = nil, nil, &requestPrice
			}
			if err := model.CoreDB().Create(&price).Error; err != nil {
				t.Fatal(err)
			}
			model.RefreshPricing()
			engine := gin.New()
			engine.Use(middleware.RequestId())
			SetRelayRouter(engine)
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"stream-fixture","messages":[{"role":"user","content":"hello"}],"stream":true,"max_tokens":100}`))
			req.Header.Set("Authorization", "Bearer sk-"+key.Key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := gateway.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if primaryCalls.Load() != 1 || fallbackCalls.Load() != tc.wantFallback {
				t.Fatalf("wrong attempts: %d/%d body=%s", primaryCalls.Load(), fallbackCalls.Load(), body)
			}
			wantSuccess := tc.wantFallback == 1 && !tc.fallbackReset
			if strings.Contains(string(body), `"error"`) == wantSuccess || (wantSuccess && strings.Count(string(body), "[DONE]") != 1) {
				t.Fatalf("wrong terminal response: %s", body)
			}
			// Refunds are asynchronous; wait for the account, key and ledger to
			// agree, not just a handler flag or HTTP status.
			deadline := time.Now().Add(3 * time.Second)
			for {
				var user model.User
				var token model.Token
				var net billing.MicroUSD
				var entries int64
				if err := model.CoreDB().First(&user, 991).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().First(&token, 991).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Model(&model.WalletLedger{}).Select("COALESCE(SUM(delta_microusd),0)").Scan(&net).Error; err != nil {
					t.Fatal(err)
				}
				model.CoreDB().
					Model(&model.WalletLedger{}).Count(&entries)
				if user.Quota == billing.USD(1)-tc.wantCharge && token.RemainQuota == user.Quota && net == -tc.wantCharge && entries == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("reservation leaked or double settled: user=%d key=%d ledger=%d entries=%d want charge=%d", user.Quota, token.RemainQuota, net, entries, tc.wantCharge)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
