package service

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestPrepaidConcurrentReservationsIncludeUnlimitedKeysAndHighBalances(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	sqlDB, _ := db.DB()
	// SQLite serializes writes; limit connections to avoid testing SQLITE_BUSY
	// instead of the account's transactional balance predicate.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "prepaid-concurrency", Quota: billing.USD(20)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "prepaid-test-key", UnlimitedQuota: true}).Error; err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	infos := make(chan *relaycommon.RelayInfo, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{RequestID: fmt.Sprintf("prepaid-%d", i), UserId: 1, TokenId: 2, TokenKey: "prepaid-test-key", TokenUnlimited: true}
			err := PreConsumeQuota(c, billing.USD(5), info)
			if err != nil {
				if err.GetErrorCode() != types.ErrorCodeInsufficientQuota {
					t.Errorf("unexpected rejection: %v", err)
				}
				return
			}
			if info.FinalPreConsumedQuota != billing.USD(5) {
				t.Errorf("reservation skipped: %d", info.FinalPreConsumedQuota)
			}
			accepted.Add(1)
			infos <- info
		}(i)
	}
	wg.Wait()
	close(infos)
	if accepted.Load() != 4 {
		t.Fatalf("accepted %d calls; $20 must fund exactly four $5 reservations", accepted.Load())
	}
	var user model.User
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != 0 {
		t.Fatalf("balance after reservations: %d", user.Quota)
	}
	// Reconcile each call to $1 actual usage: release $4 exactly once.
	for info := range infos {
		if err := PostConsumeQuota(info, -billing.USD(4), info.FinalPreConsumedQuota, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != billing.USD(16) {
		t.Fatalf("unused funds not restored: %d", user.Quota)
	}
	var delta billing.MicroUSD
	if err := db.Model(&model.WalletLedger{}).Select("COALESCE(SUM(delta_microusd),0)").Scan(&delta).Error; err != nil {
		t.Fatal(err)
	}
	if delta != -billing.USD(4) {
		t.Fatalf("wallet does not reconcile to four $1 calls: %d", delta)
	}
}

// A Key with $2.36 left and a $2.56 worst-case estimate (max_tokens x output
// price) must not be locked out while the actual call costs $0.19.
func TestEstimatedReservationClampsToPositiveKeyRemainder(t *testing.T) {
	const remaining, estimate, actual = billing.MicroUSD(2_360_000), billing.MicroUSD(2_560_000), billing.MicroUSD(190_000)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}, &model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	payer := model.User{Username: "clamp-key-payer", Quota: 13_000_000}
	if err := db.Create(&payer).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	db.Where("owner_user_id = ?", payer.Id).First(&project)
	token := model.Token{UserId: payer.Id, ProjectID: project.ID, Key: "clamp-key", RemainQuota: remaining}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	newInfo := func(id string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{ProjectID: project.ID, PayerUserID: payer.Id, ActorUserID: payer.Id, UserId: payer.Id, TokenId: token.Id, TokenKey: token.Key, RequestID: id}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := PreConsumeQuota(c, estimate, newInfo("exact-price")); err == nil || err.GetErrorCode() != types.ErrorCodeKeyBudgetExceeded {
		t.Fatalf("exact reservation must keep the strict Key check: %v", err)
	}
	info := newInfo("estimated")
	if err := PreConsumeEstimatedQuota(c, estimate, info); err != nil {
		t.Fatalf("positive Key remainder locked out by estimate: %v", err)
	}
	if info.FinalPreConsumedQuota != remaining {
		t.Fatalf("hold %d, want the Key remainder %d", info.FinalPreConsumedQuota, remaining)
	}
	if err := PostConsumeQuota(info, actual-info.FinalPreConsumedQuota, info.FinalPreConsumedQuota, false); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&token, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&payer, payer.Id).Error; err != nil {
		t.Fatal(err)
	}
	if token.RemainQuota != remaining-actual || payer.Quota != 13_000_000-actual {
		t.Fatalf("settlement did not record the actual charge: key=%d wallet=%d", token.RemainQuota, payer.Quota)
	}
	if err := db.Model(&model.Token{}).Where("id = ?", token.Id).Update("remain_quota", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := PreConsumeEstimatedQuota(c, estimate, newInfo("exhausted")); err == nil || err.GetErrorCode() != types.ErrorCodeKeyBudgetExceeded {
		t.Fatalf("exhausted Key admitted: %v", err)
	}
}

func TestEstimatedReservationClampsToPositiveWalletBalance(t *testing.T) {
	const balance, estimate = billing.MicroUSD(2_000_000), billing.MicroUSD(2_560_000)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "clamp-wallet", Quota: balance}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "clamp-wallet-key", UnlimitedQuota: true}).Error; err != nil {
		t.Fatal(err)
	}
	newInfo := func(id string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{RequestID: id, UserId: 1, TokenId: 2, TokenKey: "clamp-wallet-key", TokenUnlimited: true}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := PreConsumeQuota(c, estimate, newInfo("exact-price")); err == nil || err.GetErrorCode() != types.ErrorCodeInsufficientQuota {
		t.Fatalf("exact reservation must keep the strict wallet check: %v", err)
	}
	perCall := newInfo("per-call")
	perCall.PriceData.UsePrice = true
	if err := PreConsumeEstimatedQuota(c, estimate, perCall); err == nil || err.GetErrorCode() != types.ErrorCodeInsufficientQuota {
		t.Fatalf("per-call price is exact and must not be clamped: %v", err)
	}
	info := newInfo("estimated")
	if err := PreConsumeEstimatedQuota(c, estimate, info); err != nil {
		t.Fatalf("positive wallet locked out by estimate: %v", err)
	}
	if info.FinalPreConsumedQuota != balance {
		t.Fatalf("hold %d, want the wallet balance %d", info.FinalPreConsumedQuota, balance)
	}
	if err := PreConsumeEstimatedQuota(c, estimate, newInfo("empty")); err == nil || err.GetErrorCode() != types.ErrorCodeInsufficientQuota {
		t.Fatalf("empty wallet admitted: %v", err)
	}
}

func TestClampReservationToRemaining(t *testing.T) {
	if got := clampReservationToRemaining(500, 0, -10); got != 500 {
		t.Fatalf("non-positive remainder clamped the hold: %d", got)
	}
	if got := clampReservationToRemaining(500, 300, 200, 900); got != 200 {
		t.Fatalf("hold not lowered to the smallest positive remainder: %d", got)
	}
}
