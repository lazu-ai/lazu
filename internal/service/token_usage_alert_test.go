package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestTeamTokenAlertUsesMonthlySpendAndResetsEachMonth(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	now := time.Date(2026, 9, 22, 6, 30, 0, 0, time.UTC)
	token := &model.Token{Id: 57, ProjectID: 8, UnlimitedQuota: true, UsedQuota: billing.USD(999), UsageAlertPercent: 80}
	budget := model.SpendBudget{ProjectID: 8, TokenID: 57, Scope: model.SpendBudgetScopeToken, Period: model.SpendBudgetPeriodMonth, LimitMicrousd: billing.USD(20), Enabled: true}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []model.ProjectSpend{
		{EventKey: "this-month", ProjectID: 8, TokenID: 57, ActorUserID: 1, PayerUserID: 2, AmountMicrousd: billing.USD(17), OccurredAt: now},
		{EventKey: "last-month", ProjectID: 8, TokenID: 57, ActorUserID: 1, PayerUserID: 2, AmountMicrousd: billing.USD(99), OccurredAt: now.AddDate(0, -1, 0)},
		{EventKey: "other-key", ProjectID: 8, TokenID: 58, ActorUserID: 1, PayerUserID: 2, AmountMicrousd: billing.USD(10), OccurredAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := readTokenAlertAllowance(token, now)
	if err != nil || got.total != billing.USD(20) || got.used != billing.USD(17) || got.remaining != billing.USD(3) || got.period != "2026-09" {
		t.Fatalf("monthly allowance: %+v %v", got, err)
	}
	releaseTokenUsageAlert(57, 80, got.total, "2026-09")
	releaseTokenUsageAlert(57, 80, got.total, "2026-10")
	t.Cleanup(func() {
		releaseTokenUsageAlert(57, 80, got.total, "2026-09")
		releaseTokenUsageAlert(57, 80, got.total, "2026-10")
	})
	if !claimTokenUsageAlert(57, 80, got.total, "2026-09") || claimTokenUsageAlert(57, 80, got.total, "2026-09") || !claimTokenUsageAlert(57, 80, got.total, "2026-10") {
		t.Fatal("monthly deduplication failed")
	}
	next, err := readTokenAlertAllowance(token, now.AddDate(0, 1, 0))
	if err != nil || next.used != 0 || next.period != "2026-10" {
		t.Fatalf("next month: %+v %v", next, err)
	}
	if err := db.Model(&budget).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	unlimited, err := readTokenAlertAllowance(token, now)
	if err != nil || unlimited.total != 0 {
		t.Fatal("disabled cap still alerts")
	}
}

func TestPersonalTokenAlertRetainsLifetimeQuota(t *testing.T) {
	got, err := readTokenAlertAllowance(&model.Token{RemainQuota: billing.USD(3), UsedQuota: billing.USD(17)}, time.Now())
	if err != nil || got.total != billing.USD(20) || got.used != billing.USD(17) || got.period != "" {
		t.Fatalf("personal allowance changed: %+v %v", got, err)
	}
}

func TestTokenAlertFallbackPrunesExpiredMonthlyMarkers(t *testing.T) {
	redisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = redisEnabled })
	tokenUsageAlertMu.Lock()
	previous := tokenUsageAlertStore
	tokenUsageAlertStore = map[string]time.Time{"expired-prior-month": time.Now().Add(-time.Second), "live-prior-month": time.Now().Add(time.Hour)}
	tokenUsageAlertMu.Unlock()
	t.Cleanup(func() { tokenUsageAlertMu.Lock(); tokenUsageAlertStore = previous; tokenUsageAlertMu.Unlock() })
	if !claimTokenUsageAlert(88, 80, billing.USD(20), "2026-09") {
		t.Fatal("new monthly marker was not claimed")
	}
	tokenUsageAlertMu.Lock()
	defer tokenUsageAlertMu.Unlock()
	if _, exists := tokenUsageAlertStore["expired-prior-month"]; exists {
		t.Fatal("expired monthly marker was retained")
	}
	if _, exists := tokenUsageAlertStore["live-prior-month"]; !exists {
		t.Fatal("live marker was pruned")
	}
	if len(tokenUsageAlertStore) != 2 {
		t.Fatalf("unexpected marker count: %d", len(tokenUsageAlertStore))
	}
}

type tokenAlertTransport func(*http.Request) (*http.Response, error)

func (f tokenAlertTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExactProjectSettlementSendsMonthlyTokenAlert(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	token := model.Token{Id: 71, UserId: 1, ProjectID: 8, Name: "exact-key", UnlimitedQuota: true, UsageAlertPercent: 80}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.SpendBudget{ProjectID: 8, TokenID: token.Id, Scope: model.SpendBudgetScopeToken, Period: model.SpendBudgetPeriodMonth, LimitMicrousd: billing.USD(20), Enabled: true}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	oldTransport, oldKey := http.DefaultTransport, common.AppConfig.Resend.APIKey
	oldRecorder, oldSecret := common.MailRecorder, common.CryptoSecret
	common.AppConfig.Resend.APIKey = "test-only"
	common.CryptoSecret = "test-token-alert-archive" // secret-scan: allow -- deterministic test-only encryption key
	archiveStarted := make(chan struct{})
	releaseArchive := make(chan struct{})
	archiveFinished := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseArchive) }) }
	common.MailRecorder = func(rec common.MailRecord) {
		close(archiveStarted)
		<-releaseArchive
		model.RecordMailDelivery(rec)
		close(archiveFinished)
	}
	messages := make(chan string, 4)
	http.DefaultTransport = tokenAlertTransport(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Subject string `json:"subject"`
			Html    string `json:"html"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		messages <- body.Subject + body.Html
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"test-mail"}`))}, nil
	})
	t.Cleanup(func() {
		release()
		waitTokenUsageAlerts()
		http.DefaultTransport = oldTransport
		common.AppConfig.Resend.APIKey = oldKey
		common.MailRecorder = oldRecorder
		common.CryptoSecret = oldSecret
		releaseTokenUsageAlert(token.Id, 80, billing.USD(20), time.Now().UTC().Format("2006-01"))
	})
	info := &relaycommon.RelayInfo{UserId: 1, ActorUserID: 1, PayerUserID: 1, ProjectID: 8, TokenId: token.Id, TokenUnlimited: true, TokenUsageAlertPercent: 80, UserEmail: "test@example.com", UserQuota: billing.USD(1000), RequestID: "exact-alert"}
	if err := PostConsumeQuota(info, 0, billing.USD(17), true); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "80% of its monthly limit") || !strings.Contains(message, "$17.00") {
			t.Fatalf("wrong alert: %s", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exact settlement did not send monthly token alert")
	}
	select {
	case <-archiveStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("token alert never reached the delivery recorder")
	}
	// Settlement has returned while the real delivery recorder is still gated.
	// Receiving the HTTP request alone is therefore not a teardown boundary.
	var count int64
	if err := db.Model(&model.MailDelivery{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("delivery was archived before release: count=%d err=%v", count, err)
	}
	release()
	waitTokenUsageAlerts()
	select {
	case <-archiveFinished:
	default:
		t.Fatal("token alert completion preceded the delivery recorder")
	}
	var delivery model.MailDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatalf("token alert completion did not include its delivery row: %v", err)
	}
	if delivery.Kind != common.MailKindKeyUsage || delivery.Receiver != info.UserEmail || delivery.Status != model.MailStatusSent || delivery.ProviderID != "test-mail" || !delivery.HasBody {
		t.Fatalf("wrong token alert delivery: %+v", delivery)
	}
	html, err := delivery.HTML()
	if err != nil || !strings.Contains(html, "$17.00") || !strings.Contains(delivery.Subject, "80% of its monthly limit") {
		t.Fatalf("wrong archived alert: subject=%q html=%q err=%v", delivery.Subject, html, err)
	}
}
