package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestCommittedLedgerChargesSendPayerBalanceAndKeyUsageEmails(t *testing.T) {
	assertCommittedLedgerChargeEmails(t, nil)
}

func TestCommittedLedgerChargesMissingPayerLookupOncePerBatch(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	var reads atomic.Int32
	if err := db.Callback().Query().Before("gorm:query").Register("review:count_missing_payer", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			reads.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	events := make([]ledger.Event, 500)
	for i := range events {
		events[i] = ledger.Event{Input: ledger.Input{PayerID: 999, TokenID: i + 1, Amount: 1}, Kind: "settle"}
	}
	notifyCommittedLedgerChargesWithEngine(events, nil)
	if count := reads.Load(); count != 1 {
		t.Fatalf("500 charges with a missing payer made %d payer lookups", count)
	}
}

func TestCommittedLedgerChargesMailAvailableRedisBalanceIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	clear := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	clear()
	t.Cleanup(func() { clear(); _ = client.Close() })
	storage := &backlogTestStorage{balance: int64(billing.USD(20)), spent: map[string]int64{}, applied: map[string]ledger.Event{}}
	engine := ledger.New(client, storage)
	engine.Continuity = common.GenerateRequestID()
	if err := client.Set(ctx, ledger.ContinuityKey, engine.Continuity, 0).Err(); err != nil {
		t.Fatal(err)
	}
	hold := ledger.Input{RequestID: "notification-live-hold", PayerID: 8301, ActorID: 8301, Unlimited: true, Amount: int64(billing.USD(15)), OccurredAt: time.Now().UTC()}
	if err := engine.Reserve(ctx, hold); err != nil {
		t.Fatal(err)
	}
	assertCommittedLedgerChargeEmails(t, engine)
}

func assertCommittedLedgerChargeEmails(t *testing.T, engine *ledger.Engine) {
	t.Helper()
	db := setupSpendBudgetServiceTestDB(t)
	payer := model.User{Id: 8301, Username: "ledger-alert-payer", Email: "payer@example.com", Quota: billing.USD(5)}
	if engine != nil {
		// SQL is above the low-balance trigger while Redis has a newer live
		// reservation. The notification must report the real $5 available.
		payer.Quota = billing.USD(20)
	}
	payer.SetSetting(dto.UserSetting{Language: "en", NotifyType: dto.NotifyTypeEmail})
	actor := model.User{Id: 8302, Username: "ledger-alert-actor", Email: "actor@example.com", Quota: billing.USD(1000)}
	for _, user := range []*model.User{&payer, &actor} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := model.Token{Id: 8303, UserId: actor.Id, ProjectID: 8304, Name: "writer-alert-key", RemainQuota: billing.USD(3), UsedQuota: billing.USD(17), UsageAlertPercent: 80}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	oldTransport, oldKey, oldRecorder := http.DefaultTransport, common.AppConfig.Resend.APIKey, common.MailRecorder
	settings := email_setting.GetEmailSetting()
	oldSettings := *settings
	settings.LowBalanceEnabled, settings.LowBalanceThresholdUSD = true, 20
	common.AppConfig.Resend.APIKey = "test-only"
	resetLowBalanceEmailCooldown(payer.Id)
	releaseTokenUsageAlert(token.Id, 80, billing.USD(20))
	type sentMail struct {
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
	}
	var mu sync.Mutex
	var sent []sentMail
	delivered := make(chan string, 4)
	http.DefaultTransport = tokenAlertTransport(func(request *http.Request) (*http.Response, error) {
		var mail sentMail
		if err := json.NewDecoder(request.Body).Decode(&mail); err != nil {
			return nil, err
		}
		mu.Lock()
		sent = append(sent, mail)
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"test-ledger-alert"}`))}, nil
	})
	common.MailRecorder = func(record common.MailRecord) { delivered <- record.Meta.Kind }
	t.Cleanup(func() {
		waitTokenUsageAlerts()
		http.DefaultTransport, common.AppConfig.Resend.APIKey, common.MailRecorder = oldTransport, oldKey, oldRecorder
		*settings = oldSettings
		resetLowBalanceEmailCooldown(payer.Id)
		releaseTokenUsageAlert(token.Id, 80, billing.USD(20))
	})
	event := ledger.Event{Input: ledger.Input{RequestID: "committed-alert", PayerID: payer.Id, ActorID: actor.Id, ProjectID: token.ProjectID, TokenID: token.Id, Amount: int64(billing.USD(17)), OccurredAt: time.Now().UTC()}, OperationID: "committed-alert", Kind: "settle"}
	var payerReads, keyReads atomic.Int32
	if err := db.Callback().Query().Before("gorm:query").Register("review:count_notification_identity", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "users":
			payerReads.Add(1)
		case "tokens":
			keyReads.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	events := make([]ledger.Event, 500)
	for i := range events {
		events[i] = event
	}
	// This is the callback registered with the model writer. The native model
	// test separately proves the callback occurs only after a new SQL commit.
	notifyCommittedLedgerChargesWithEngine(events, engine)
	kinds := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case kind := <-delivered:
			kinds[kind] = true
		case <-time.After(3 * time.Second):
			t.Fatal("background committed charge did not deliver both balance and Key usage mail")
		}
	}
	waitTokenUsageAlerts()
	// One callback lookup plus the existing asynchronous alert helper lookup,
	// independent of the 500 committed events sharing this payer and Key.
	if payerReads.Load() > 2 || keyReads.Load() > 2 {
		t.Fatalf("notification SQL scaled with batch length: payer=%d Key=%d", payerReads.Load(), keyReads.Load())
	}
	if !kinds[common.MailKindBalance] || !kinds[common.MailKindKeyUsage] {
		t.Fatalf("committed charge mail kinds: %v", kinds)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("repeated events in one batch sent %d messages", len(sent))
	}
	for _, mail := range sent {
		if len(mail.To) != 1 || mail.To[0] != payer.Email {
			t.Fatalf("charge alert sent to actor instead of payer: %+v", mail.To)
		}
		if strings.Contains(mail.Subject, "balance") && !strings.Contains(mail.Subject, "$5.00") {
			t.Fatalf("low balance email subtracted the committed charge twice: %q", mail.Subject)
		}
		if strings.Contains(mail.Subject, "Key") && (!strings.Contains(mail.HTML, "$17.00") || !strings.Contains(mail.HTML, "80%")) {
			t.Fatalf("Key usage email lost current committed usage: %q", mail.Subject)
		}
	}
}
