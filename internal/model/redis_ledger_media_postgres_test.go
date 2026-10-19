package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestRedisMediaUsesTrustedReservationAndDurableFactsPostgres(t *testing.T) {
	dsn, redisURL := os.Getenv("TEST_GATEWAY_CORE_DSN"), os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("isolated Postgres and Redis URLs required")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := "media_ledger_test_" + strings.ToLower(strings.ReplaceAll(common.GenerateRequestID(), "-", ""))
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE"); sqlDB, _ := base.DB(); sqlDB.Close() })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{&User{}, &Token{}, &Project{}, &ProjectMember{}, &EphemeralCredential{}, &WalletLedger{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &ProjectSpend{}, &MediaJob{}, &Channel{}, &ModelSellPrice{}, &ProviderCostPrice{}}
	models = append(models, RedisLedgerModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	oldCore, oldTelemetry, oldRedis, oldEnabled, oldGateway := CoreDB(), ConfiguredTelemetryDB(), common.RDB, common.RedisEnabled, GatewayLedger()
	oldPricing, _ := activePricingSnapshot.Load().(*pricingSnapshot)
	SetCoreDB(db)
	SetTelemetryDB(nil) // Modern media must not need a synchronous telemetry writer.
	common.RDB, common.RedisEnabled = client, true
	clear := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(ctx, keys...)
		}
	}
	clear()
	t.Cleanup(func() {
		clear()
		client.Close()
		SetCoreDB(oldCore)
		SetTelemetryDB(oldTelemetry)
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
		gatewayLedger.Store(oldGateway)
		activePricingSnapshot.Store(oldPricing)
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	engine.SpoolDir = t.TempDir()
	engine.TelemetryStream, engine.TelemetryEncode = ledger.Prefix+"media-test-telemetry", EncodeFinancialTelemetry
	gatewayLedger.Store(engine)
	user := User{Id: 811, Username: "redis-media-payer", Status: common.UserStatusEnabled, Quota: 10000}
	project := Project{ID: 812, PublicID: "prj_media_ledger", OwnerUserID: user.Id, Name: "Media ledger", Settings: JSONValue(`{}`)}
	token := Token{Id: 813, UserId: user.Id, ProjectID: project.ID, Key: "media-ledger-test", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000, Group: ChannelLaneDirect}
	member := ProjectMember{ProjectID: project.ID, UserID: user.Id, Role: ProjectRoleOwner, Status: ProjectMemberActive}
	for _, row := range []interface{}{&user, &project, &token, &member} {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	requestPrice := .0006
	if err := db.Create(&ModelSellPrice{ModelName: "video-model", ChannelGroup: ChannelLaneDirect, RequestUSD: &requestPrice, Active: true, PricingVersion: 17}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RefreshPricingSnapshot(); err != nil {
		t.Fatal(err)
	}
	quote := MediaJobBillingResult{CustomerPriceConfigured: true, CustomerCharge: 600, ChannelGroup: ChannelLaneDirect, CustomerQuote: billing.QuoteResult{TotalMicrousd: 600, PricingVersion: 17}}
	quote.CostSnapshot = buildMediaJobCostSnapshot(quote)
	job := MediaJob{PublicID: "mjob_trusted_hold", UserID: user.Id, ProjectID: project.ID, PayerUserID: user.Id, ActorUserID: user.Id, TokenID: &token.Id, ModelName: "video-model", Kind: MediaJobKindVideo, Provider: "test", Status: MediaJobStatusRunning, RequestID: common.GenerateRequestID(), RequestSnapshot: JSONValue(`{"_billing_reservation_id":"client-chosen","_billing_reserved_quota_microusd":9999,"_billing_budget_occurred_at":"2000-01-01T00:00:00Z"}`)}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot, err := ResolveVerifiedIdentitySnapshot(ctx, token.Id, 0)
	if err != nil {
		t.Fatal(err)
	}
	job.SetAdmission(snapshot, nil)
	if _, err := reserveRedisMediaJob(&job, quote, now); err != nil {
		t.Fatal(err)
	}
	input, err := mediaRedisInput(job)
	if err != nil || input.RequestID != "media:"+job.PublicID || input.Amount != 600 || !input.OccurredAt.Equal(now) {
		t.Fatalf("customer fields selected financial hold: %+v %v", input, err)
	}
	assertAvailable := func(want int64) {
		t.Helper()
		got, err := client.HGet(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id), "avail").Int64()
		if err != nil || got != want {
			t.Fatalf("available=%d want=%d err=%v", got, want, err)
		}
	}
	assertAvailable(9400)
	changedQuote := quote
	changedQuote.CustomerCharge = 900
	if repeated, err := reserveRedisMediaJob(&job, changedQuote, now.Add(time.Second)); err != nil || repeated.CustomerCharge != 600 {
		t.Fatalf("retry repriced immutable hold: %+v %v", repeated, err)
	}
	assertAvailable(9400)
	// Request metadata is not the settlement authority after admission either.
	job.RequestSnapshot = JSONValue(`{"_billing_reserved_quota_microusd":1}`)
	if result, err := SettleMediaJobBilling(job, now); err != nil || result.CustomerCharge != 600 {
		t.Fatalf("settle=%+v %v", result, err)
	}
	if _, err := SettleMediaJobBilling(job, now); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Drain(ctx, "media-test"); err != nil {
		t.Fatal(err)
	}
	var after User
	var afterToken Token
	if err := db.First(&after, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&afterToken, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if after.Quota != 9400 || after.UsedQuota != 600 || afterToken.RemainQuota != 9400 || afterToken.UsedQuota != 600 {
		t.Fatalf("financial projection payer=%+v token=%+v", after, afterToken)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("charged twice: count=%d %v", count, err)
	}
	events, err := client.XRange(ctx, engine.TelemetryStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	complete := 0
	for _, event := range events {
		if event.Values["type"] != RelayEventTypeRelayResult {
			continue
		}
		payload, ok := event.Values["payload"].(string)
		if !ok {
			t.Fatalf("missing full media fact: %+v", event)
		}
		var result relayResultEvent
		if err := json.Unmarshal([]byte(payload), &result); err != nil {
			t.Fatal(err)
		}
		if result.ConsumeLog == nil || result.ConsumeLog.AmountMicrousd != 600 || result.ConsumeLog.RequestID != job.RequestID || result.ConsumeLog.PricingVersion != 17 {
			t.Fatalf("incomplete media fact: %+v", result)
		}
		complete++
	}
	if complete != 1 {
		t.Fatalf("full media envelopes=%d want1", complete)
	}
	// A detached probe's zero-money hold still has trusted identity and can be
	// safely released by the durable helper without legacy SQL ledger lookup.
	free := job
	free.ID, free.PublicID, free.LedgerInputJSON, free.RequestSnapshot = 0, "", "", JSONValue(`{}`)
	free.RequestID = common.GenerateRequestID()
	freeQuote := quote
	freeQuote.CustomerCharge, freeQuote.CustomerQuote.TotalMicrousd = 0, 0
	if _, err := reserveRedisMediaJob(&free, freeQuote, now); err != nil {
		t.Fatal(err)
	}
	if free.LedgerInputJSON == "" {
		t.Fatal("free media skipped admission")
	}
	if err := RefundMediaJobBillingReservationDurably(free, now); err != nil {
		t.Fatal(err)
	}
	assertAvailable(9400)
	if held, err := client.HLen(ctx, ledger.Prefix+"reservations").Result(); err != nil || held != 0 {
		t.Fatalf("leaked media hold=%d %v", held, err)
	}

	t.Run("configured free media admits zero balance but respects holds", func(t *testing.T) {
		wallet := fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id)
		if err := client.HSet(ctx, wallet, "avail", 0).Err(); err != nil {
			t.Fatal(err)
		}
		defer client.HSet(ctx, wallet, "avail", 9400)
		for _, configured := range []bool{false, true} {
			probe := free
			probe.PublicID, probe.LedgerInputJSON = common.GenerateRequestID(), ""
			price := freeQuote
			price.CustomerPriceConfigured = configured
			_, err := reserveRedisMediaJob(&probe, price, now)
			if !configured {
				var rejected *ledger.Rejection
				if !errors.As(err, &rejected) || rejected.Code != "balance" {
					t.Fatalf("unconfigured zero price: %v", err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("configured free media: %v", err)
			}
			input, err := mediaRedisInput(probe)
			if err != nil || !input.AllowZeroBalance {
				t.Fatalf("free admission flag missing: %+v %v", input, err)
			}
			if err := RefundMediaJobBillingReservationDurably(probe, now); err != nil {
				t.Fatal(err)
			}
		}

		probe := free
		probe.PublicID, probe.LedgerInputJSON = common.GenerateRequestID(), ""
		heldAdmission := *probe.admission
		heldAdmission.snapshot.PaymentRiskHold = true
		probe.admission = &heldAdmission
		if _, err := reserveRedisMediaJob(&probe, freeQuote, now); !errors.Is(err, ErrProjectAccessSuspended) {
			t.Fatal("payment hold allowed free media")
		}
	})

	t.Run("verified rate buckets survive the media probe", func(t *testing.T) {
		first := free
		first.PublicID, first.LedgerInputJSON = "mjob_rate_first", ""
		first.SetAdmission(snapshot, []ledger.RateLimit{{Key: "media_test_rpm", Decision: "token_rpm_exceeded", Limit: 1, Cost: 1, WindowSeconds: 60}})
		if _, err := reserveRedisMediaJob(&first, quote, now); err != nil {
			t.Fatal(err)
		}
		second := first
		second.PublicID, second.LedgerInputJSON = "mjob_rate_second", ""
		_, err := reserveRedisMediaJob(&second, quote, now)
		var rejection *ledger.Rejection
		if !errors.As(err, &rejection) || rejection.Code != "rate" || rejection.RateDecision != "token_rpm_exceeded" {
			t.Fatalf("discarded rate admission: %v", err)
		}
		retry := free
		retry.PublicID = first.PublicID
		retry.SetAdmission(snapshot, first.admission.rates)
		if err := CarryMediaJobBillingReservation(&retry, &first); err != nil {
			t.Fatal(err)
		}
		if result, err := reserveRedisMediaJob(&retry, changedQuote, now); err != nil || result.CustomerCharge != 600 {
			t.Fatalf("safe upstream retry lost its first quote/one rate admission: %+v %v", result, err)
		}
		assertAvailable(8800)
		if err := RefundMediaJobBillingReservationDurably(first, now); err != nil {
			t.Fatal(err)
		}
		assertAvailable(9400)
	})
	t.Run("ephemeral revocation fences an already verified media probe", func(t *testing.T) {
		credential := EphemeralCredential{PublicID: "eph_media_private", CredentialDigest: "media-test-digest", UserID: user.Id, TokenID: token.Id, Scope: "relay", ExpiresAt: now.Add(time.Minute).Unix()}
		if err := db.Create(&credential).Error; err != nil {
			t.Fatal(err)
		}
		verified, err := ResolveVerifiedIdentitySnapshot(ctx, token.Id, credential.ID)
		if err != nil {
			t.Fatal(err)
		}
		probe := free
		probe.PublicID, probe.LedgerInputJSON = "mjob_ephemeral_fenced", ""
		probe.SetAdmission(verified, nil)
		if err := InstallIdentityMutationFence(db); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&credential).Update("revoked_at", now.Unix()).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := reserveRedisMediaJob(&probe, quote, now); !errors.Is(err, ledger.ErrStale) {
			t.Fatalf("revoked ephemeral admitted: %v", err)
		}
		assertAvailable(9400)
	})
	// The revocation bumped the directory generation. Subsequent requests use
	// fresh verified server state, while accepted holds retain their own input.
	snapshot, err = ResolveVerifiedIdentitySnapshot(ctx, token.Id, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("artifact imports reserve and never fall back to SQL finance", func(t *testing.T) {
		input := CreateMediaJobInput{ProjectID: project.ID, PayerUserID: user.Id, ActorUserID: user.Id, UserID: user.Id, TokenID: token.Id, ModelName: "video-model", Kind: MediaJobKindVideo, ArtifactURL: "https://example.invalid/synthetic.mp4", RequestID: common.GenerateRequestID(), Now: now}
		if _, err := CreateMediaJob(input); !errors.Is(err, ErrMediaJobAdmissionMissing) {
			t.Fatalf("customer metadata substituted for server admission: %v", err)
		}
		input.SetAdmission(snapshot, nil)
		imported, err := CreateMediaJob(input)
		if err != nil {
			t.Fatal(err)
		}
		if imported.LedgerInputJSON == "" {
			t.Fatal("runnable import lacks its zero/paid hold")
		}
		assertAvailable(8800)
		missing := *imported
		missing.LedgerInputJSON = ""
		sqlCalls := 0
		callback := "test:missing-import-no-sql"
		observe := func(tx *gorm.DB) { sqlCalls++ }
		db.Callback().Query().Before("gorm:query").Register(callback, observe)
		db.Callback().Create().Before("gorm:create").Register(callback, observe)
		db.Callback().Update().Before("gorm:update").Register(callback, observe)
		db.Callback().Raw().Before("gorm:raw").Register(callback, observe)
		if _, err := SettleMediaJobBilling(missing, now); !errors.Is(err, ErrMediaJobLedgerInputMissing) {
			t.Fatalf("legacy settlement fallback: %v", err)
		}
		if err := CheckMediaJobBillingBeforeArtifactFetch(missing, now); !errors.Is(err, ErrMediaJobLedgerInputMissing) {
			t.Fatalf("legacy artifact admission fallback: %v", err)
		}
		if err := RefundMediaJobBillingReservationDurably(missing, now); err != nil {
			t.Fatal(err)
		}
		ReleaseMediaJobSpendBudgetReservation(missing)
		db.Callback().Query().Remove(callback)
		db.Callback().Create().Remove(callback)
		db.Callback().Update().Remove(callback)
		db.Callback().Raw().Remove(callback)
		if sqlCalls != 0 {
			t.Fatalf("missing durable input invoked legacy SQL: %d", sqlCalls)
		}
		assertAvailable(8800)
		if _, err := SettleMediaJobBilling(*imported, now); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Drain(ctx, "media-import-test"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&after, user.Id).Error; err != nil || after.Quota != 8800 {
			t.Fatalf("import finance projection=%d err=%v", after.Quota, err)
		}
		if err := db.Model(&WalletLedger{}).Where("ref_type = ?", MediaJobRefType).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("created legacy financial entry count=%d %v", count, err)
		}
		failure := errors.New("synthetic media row failure")
		callback = "test:reject-import-row"
		if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "media_jobs" {
				tx.AddError(failure)
			}
		}); err != nil {
			t.Fatal(err)
		}
		_, err = CreateMediaJob(input)
		db.Callback().Create().Remove(callback)
		if !errors.Is(err, failure) {
			t.Fatalf("failed row creation unexpectedly succeeded: %v", err)
		}
		assertAvailable(8800)
		if held, err := client.HLen(ctx, ledger.Prefix+"reservations").Result(); err != nil || held != 0 {
			t.Fatalf("failed import leaked a hold: %d %v", held, err)
		}
	})
	t.Run("all active holds beyond five thousand are extended", func(t *testing.T) {
		rows := make([]MediaJob, 5101)
		for i := range rows {
			row := free
			row.ID, row.PublicID, row.LedgerInputJSON = 0, fmt.Sprintf("mjob_extend_%d", i), ""
			row.RequestID = common.GenerateRequestID()
			row.SetAdmission(snapshot, nil)
			if _, err := reserveRedisMediaJob(&row, freeQuote, now); err != nil {
				t.Fatal(err)
			}
			rows[i] = row
		}
		if err := db.CreateInBatches(&rows, 500).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&MediaJob{}).Where("id = ?", rows[2].ID).Update("ledger_input_json", "broken-server-input").Error; err != nil {
			t.Fatal(err)
		}
		if err := ExtendRedisMediaReservations(ctx); err == nil {
			t.Fatal("corrupt hold was silently ignored")
		}
		for _, index := range []int{0, 4999, 5000, 5100} {
			input, err := mediaRedisInput(rows[index])
			if err != nil {
				t.Fatal(err)
			}
			score, err := client.ZScore(ctx, ledger.Prefix+"expiry", input.RequestID).Result()
			if err != nil || score < float64(time.Now().Add(44*time.Minute).UnixMilli()) {
				t.Fatalf("job %d was starved: expiry=%f err=%v", index, score, err)
			}
		}
	})
}
